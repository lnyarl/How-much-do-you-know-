package engine

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"slices"
	"sort"
	"strings"
)

type pageCursor struct {
	Profile  string `json:"profile"`
	Revision int64  `json:"revision"`
	Offset   int    `json:"offset"`
	Query    string `json:"query"`
}

func cursorFor(m Metadata, offset int, query string) string {
	body, _ := json.Marshal(pageCursor{Profile: m.ProfileID, Revision: m.Revision, Offset: offset, Query: query})
	return base64.RawURLEncoding.EncodeToString(body)
}

func cursorOffset(encoded string, m Metadata, query string) (int, error) {
	if encoded == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return 0, fail(InvalidInput, "invalid cursor")
	}
	c, err := DecodeStrict[pageCursor](raw)
	if err != nil {
		return 0, err
	}
	if c.Profile != m.ProfileID || c.Query != query || c.Offset < 0 {
		return 0, fail(InvalidInput, "cursor belongs to another request or store")
	}
	if c.Revision != m.Revision {
		return 0, fail(StaleCursor, "store changed; restart the query")
	}
	return c.Offset, nil
}

func fingerprint(v any) string { b, _ := json.Marshal(v); return digest(b) }

func (e *Engine) SearchConcepts(ctx context.Context, r SearchRequest) (SearchResult, error) {
	if !text(r.Query) || r.Limit < 1 || r.Limit > 1000 {
		return SearchResult{}, fail(InvalidInput, "query and limit between 1 and 1000 are required")
	}
	return readTransaction(ctx, e, func(tx *sql.Tx) (SearchResult, error) {
		m, err := metadata(ctx, tx)
		if err != nil {
			return SearchResult{}, err
		}
		key := r
		key.Cursor = ""
		hash := fingerprint(key)
		offset, err := cursorOffset(r.Cursor, m, hash)
		if err != nil {
			return SearchResult{}, err
		}
		rows, err := tx.QueryContext(ctx, `SELECT c.data FROM concepts c
WHERE (?='' OR c.namespace=?) AND EXISTS(SELECT 1 FROM concept_aliases a WHERE a.concept_id=c.id AND instr(a.alias,?)>0)
ORDER BY CASE WHEN EXISTS(SELECT 1 FROM concept_aliases a WHERE a.concept_id=c.id AND a.alias=?) THEN 0 ELSE 1 END,c.id
LIMIT ? OFFSET ?`, r.Namespace, r.Namespace, r.Query, r.Query, r.Limit+1, offset)
		if err != nil {
			return SearchResult{}, err
		}
		concepts, err := decodeRows[Concept](rows)
		if err != nil {
			return SearchResult{}, err
		}
		out := SearchResult{Concepts: concepts, Revision: m.Revision}
		if len(concepts) > r.Limit {
			out.Concepts = concepts[:r.Limit]
			out.NextCursor = cursorFor(m, offset+r.Limit, hash)
		}
		return out, nil
	})
}

func decodeRows[T any](rows *sql.Rows) ([]T, error) {
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		var v T
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func bindingsFor(ctx context.Context, tx *sql.Tx, observationID string) ([]ConceptBinding, error) {
	rows, err := tx.QueryContext(ctx, "SELECT data FROM observation_concepts WHERE observation_id=? ORDER BY id", observationID)
	if err != nil {
		return nil, err
	}
	return decodeRows[ConceptBinding](rows)
}

func bindingActive(ctx context.Context, tx *sql.Tx, b ConceptBinding) (bool, error) {
	ok, err := active(ctx, tx, BindingTarget, b.ID)
	if err != nil || !ok {
		return ok, err
	}
	if b.Binding == UserConfirmed {
		return active(ctx, tx, ObservationTarget, b.ConfirmationObservationID)
	}
	return true, nil
}

func observationView(ctx context.Context, tx *sql.Tx, id string, includeInactive bool) (ObservationView, error) {
	o, err := get[Observation](ctx, tx, "observations", id)
	if err != nil {
		return ObservationView{}, err
	}
	message, err := get[Message](ctx, tx, "messages", o.MessageID)
	if err != nil {
		return ObservationView{}, err
	}
	var sessionID, recorded string
	if err := tx.QueryRowContext(ctx, "SELECT session_id FROM messages WHERE id=?", message.ID).Scan(&sessionID); err != nil {
		return ObservationView{}, err
	}
	session, err := get[sessionRecord](ctx, tx, "sessions", sessionID)
	if err != nil {
		return ObservationView{}, err
	}
	if err := tx.QueryRowContext(ctx, "SELECT recorded_at FROM observations WHERE id=?", id).Scan(&recorded); err != nil {
		return ObservationView{}, err
	}
	t, err := parseRecorded(recorded)
	if err != nil {
		return ObservationView{}, err
	}
	bindings, err := bindingsFor(ctx, tx, id)
	if err != nil {
		return ObservationView{}, err
	}
	for _, b := range bindings {
		if !includeInactive {
			ok, err := bindingActive(ctx, tx, b)
			if err != nil {
				return ObservationView{}, err
			}
			if !ok {
				continue
			}
		}
		o.Concepts = append(o.Concepts, b)
	}
	return ObservationView{Observation: o, Message: message, SessionID: sessionID, ProducerID: session.ProducerID, RecordedAt: t}, nil
}

func (e *Engine) GetContext(ctx context.Context, r ContextRequest) (ContextResult, error) {
	if len(r.ConceptIDs) == 0 || len(r.ConceptIDs) > 1000 || r.MaxItems < 1 || r.MaxItems > 1000 || r.MaxBytes < 1 || r.MaxBytes > 16*1024*1024 {
		return ContextResult{}, fail(InvalidInput, "concept_ids and bounded positive budgets are required")
	}
	for _, id := range r.ConceptIDs {
		if !validID(id) {
			return ContextResult{}, fail(InvalidInput, "invalid concept id")
		}
	}
	ids := slices.Clone(r.ConceptIDs)
	sort.Strings(ids)
	ids = slices.Compact(ids)
	r.ConceptIDs = ids
	return readTransaction(ctx, e, func(tx *sql.Tx) (ContextResult, error) {
		m, err := metadata(ctx, tx)
		if err != nil {
			return ContextResult{}, err
		}
		key := r
		key.Cursor = ""
		hash := fingerprint(key)
		offset, err := cursorOffset(r.Cursor, m, hash)
		if err != nil {
			return ContextResult{}, err
		}
		out := ContextResult{Observations: []ObservationView{}, UnobservedConceptIDs: []string{}, UnknownConceptIDs: []string{}, Signals: []Signal{}, OmittedEvidence: []OmittedEvidence{}, Revision: m.Revision}
		wanted := map[string]bool{}
		for _, id := range ids {
			_, err := get[Concept](ctx, tx, "concepts", id)
			if HasCode(err, UnknownReference) {
				out.UnknownConceptIDs = append(out.UnknownConceptIDs, id)
				continue
			}
			if err != nil {
				return out, err
			}
			wanted[id] = true
		}
		views := []ObservationView{}
		observed := map[string]bool{}
		if len(wanted) > 0 {
			args := []any{}
			marks := []string{}
			for _, id := range ids {
				if wanted[id] {
					args = append(args, id)
					marks = append(marks, "?")
				}
			}
			rows, err := tx.QueryContext(ctx, "SELECT DISTINCT observation_id FROM observation_concepts WHERE concept_id IN("+strings.Join(marks, ",")+") ORDER BY observation_id", args...)
			if err != nil {
				return out, err
			}
			observationIDs := []string{}
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return out, err
				}
				observationIDs = append(observationIDs, id)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return out, err
			}
			for _, id := range observationIDs {
				ok, err := active(ctx, tx, ObservationTarget, id)
				if err != nil {
					return out, err
				}
				if !ok {
					continue
				}
				v, err := observationView(ctx, tx, id, false)
				if err != nil {
					return out, err
				}
				selected := []ConceptBinding{}
				for _, b := range v.Observation.Concepts {
					if !wanted[b.ConceptID] {
						continue
					}
					if b.Binding != Candidate {
						observed[b.ConceptID] = true
					}
					if b.Binding != Candidate || r.IncludeCandidates {
						selected = append(selected, b)
					}
				}
				if len(selected) > 0 {
					v.Observation.Concepts = selected
					views = append(views, v)
				}
			}
		}
		for _, id := range ids {
			if wanted[id] && !observed[id] {
				out.UnobservedConceptIDs = append(out.UnobservedConceptIDs, id)
			}
		}
		sort.Slice(views, func(i, j int) bool {
			if views[i].Message.ObservedAt.Equal(views[j].Message.ObservedAt) {
				return views[i].Observation.ID < views[j].Observation.ID
			}
			return views[i].Message.ObservedAt.After(views[j].Message.ObservedAt)
		})
		if offset > len(views) {
			return out, fail(InvalidInput, "cursor offset out of range")
		}
		finish := func(v ContextResult, pos int) ContextResult {
			v.NextCursor = ""
			v.Truncated = len(v.OmittedEvidence) > 0 || pos < len(views)
			if pos < len(views) {
				v.NextCursor = cursorFor(m, pos, hash)
			}
			return v
		}
		size := func(v ContextResult) int { b, _ := json.Marshal(v); return len(b) }
		if size(finish(out, offset)) > r.MaxBytes {
			return out, fail(InvalidInput, "budget cannot fit response metadata")
		}
		pos, count := offset, 0
		for pos < len(views) && count < r.MaxItems {
			v := views[pos]
			trial := out
			trial.Observations = append(slices.Clone(out.Observations), v)
			conceptIDs := []string{}
			for _, b := range v.Observation.Concepts {
				if b.Binding == Candidate {
					trial.CandidateBindings = append(slices.Clone(trial.CandidateBindings), b)
				} else {
					conceptIDs = append(conceptIDs, b.ConceptID)
				}
			}
			sort.Strings(conceptIDs)
			conceptIDs = slices.Compact(conceptIDs)
			if len(conceptIDs) > 0 && slices.Contains([]ObservationKind{UserReportsForgotten, UserReportsRecalled, UserRequestsExplanation}, v.Observation.Kind) {
				trial.Signals = append(slices.Clone(out.Signals), Signal{ObservationID: v.Observation.ID, Kind: v.Observation.Kind, ConceptIDs: conceptIDs, ObservedAt: v.Message.ObservedAt})
			}
			if size(finish(trial, pos+1)) <= r.MaxBytes {
				out = trial
				pos++
				count++
				continue
			}
			if count > 0 {
				break
			}
			omitted := out
			omitted.OmittedEvidence = append(slices.Clone(out.OmittedEvidence), OmittedEvidence{ObservationID: v.Observation.ID, Reason: "evidence_exceeds_byte_budget"})
			if size(finish(omitted, pos+1)) > r.MaxBytes {
				return out, fail(InvalidInput, "budget cannot fit evidence reference")
			}
			out = omitted
			pos++
			count++
		}
		return finish(out, pos), nil
	})
}

func (e *Engine) GetEvidence(ctx context.Context, ids []string) ([]EvidenceResult, error) {
	if len(ids) == 0 || len(ids) > 1000 {
		return nil, fail(InvalidInput, "1 to 1000 observation ids are required")
	}
	return readTransaction(ctx, e, func(tx *sql.Tx) ([]EvidenceResult, error) {
		amendments, err := list[Amendment](ctx, tx, "amendments")
		if err != nil {
			return nil, err
		}
		out := []EvidenceResult{}
		for _, id := range ids {
			view, err := observationView(ctx, tx, id, true)
			if err != nil {
				return nil, err
			}
			ok, err := active(ctx, tx, ObservationTarget, id)
			if err != nil {
				return nil, err
			}
			item := EvidenceResult{Observation: view, Active: ok, ContextMessages: []Message{}, Assertions: []PersonalAssertion{}, Amendments: []Amendment{}}
			for _, mid := range view.Observation.ContextMessageIDs {
				m, err := get[Message](ctx, tx, "messages", mid)
				if err != nil {
					return nil, err
				}
				item.ContextMessages = append(item.ContextMessages, m)
			}
			rows, err := tx.QueryContext(ctx, "SELECT data FROM personal_assertions WHERE observation_id=? ORDER BY id", id)
			if err != nil {
				return nil, err
			}
			item.Assertions, err = decodeRows[PersonalAssertion](rows)
			if err != nil {
				return nil, err
			}
			for _, a := range amendments {
				matches := a.TargetType == ObservationTarget && a.TargetID == id
				if a.TargetType == BindingTarget {
					for _, b := range view.Observation.Concepts {
						matches = matches || b.ID == a.TargetID
					}
				}
				if a.TargetType == AssertionTarget {
					for _, b := range item.Assertions {
						matches = matches || b.ID == a.TargetID
					}
				}
				if matches {
					item.Amendments = append(item.Amendments, a)
				}
			}
			out = append(out, item)
		}
		return out, nil
	})
}

func (e *Engine) ExploreOntology(ctx context.Context, r ExploreRequest) (ExploreResult, error) {
	if !validID(r.ConceptID) || r.Depth < 0 || r.Depth > 20 || r.Limit < 1 || r.Limit > 1000 || !slices.Contains([]string{"out", "in", "both"}, r.Direction) {
		return ExploreResult{}, fail(InvalidInput, "concept_id, direction, depth 0..20 and limit 1..1000 are required")
	}
	return readTransaction(ctx, e, func(tx *sql.Tx) (ExploreResult, error) {
		m, err := metadata(ctx, tx)
		if err != nil {
			return ExploreResult{}, err
		}
		root, err := get[Concept](ctx, tx, "concepts", r.ConceptID)
		if err != nil {
			return ExploreResult{}, err
		}
		out := ExploreResult{Concepts: []Concept{root}, Relations: []Relation{}, Revision: m.Revision}
		nodes := map[string]bool{root.ID: true}
		edges := map[string]bool{}
		type visit struct {
			id    string
			depth int
		}
		queue := []visit{{root.ID, 0}}
		for len(queue) > 0 {
			v := queue[0]
			queue = queue[1:]
			if v.depth >= r.Depth {
				continue
			}
			rows, err := tx.QueryContext(ctx, "SELECT data FROM concept_relations WHERE subject_id=? OR object_id=? ORDER BY id", v.id, v.id)
			if err != nil {
				return out, err
			}
			rels, err := decodeRows[Relation](rows)
			if err != nil {
				return out, err
			}
			for _, rel := range rels {
				if edges[rel.ID] || len(r.RelationTypes) > 0 && !slices.Contains(r.RelationTypes, rel.TypeID) {
					continue
				}
				ok, err := active(ctx, tx, RelationTarget, rel.ID)
				if err != nil {
					return out, err
				}
				if !ok {
					continue
				}
				rt, err := get[RelationType](ctx, tx, "relation_types", rel.TypeID)
				if err != nil {
					return out, err
				}
				if !rt.Symmetric && ((r.Direction == "out" && rel.SubjectID != v.id) || (r.Direction == "in" && rel.ObjectID != v.id)) {
					continue
				}
				next := rel.ObjectID
				if next == v.id {
					next = rel.SubjectID
				}
				if len(out.Relations) >= r.Limit || !nodes[next] && len(out.Concepts) >= r.Limit {
					out.Truncated = true
					continue
				}
				if !nodes[next] {
					c, err := get[Concept](ctx, tx, "concepts", next)
					if err != nil {
						return out, err
					}
					out.Concepts = append(out.Concepts, c)
					nodes[next] = true
					queue = append(queue, visit{next, v.depth + 1})
				}
				out.Relations = append(out.Relations, rel)
				edges[rel.ID] = true
			}
		}
		return out, nil
	})
}
