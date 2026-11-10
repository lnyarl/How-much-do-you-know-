package engine

import (
	"context"
	"database/sql"
	"encoding/json"
)

func targetTable(t TargetType) string {
	switch t {
	case ObservationTarget:
		return "observations"
	case BindingTarget:
		return "observation_concepts"
	case AssertionTarget:
		return "personal_assertions"
	case RelationTarget:
		return "concept_relations"
	}
	return ""
}

func targetObservation(ctx context.Context, tx *sql.Tx, t TargetType, id string) (Observation, error) {
	switch t {
	case ObservationTarget:
		return get[Observation](ctx, tx, "observations", id)
	case BindingTarget:
		var oid string
		if err := tx.QueryRowContext(ctx, "SELECT observation_id FROM observation_concepts WHERE id=?", id).Scan(&oid); err != nil {
			return Observation{}, err
		}
		return get[Observation](ctx, tx, "observations", oid)
	case AssertionTarget:
		a, err := get[PersonalAssertion](ctx, tx, "personal_assertions", id)
		if err != nil {
			return Observation{}, err
		}
		return get[Observation](ctx, tx, "observations", a.ObservationID)
	}
	return Observation{}, fail(InvalidInput, "source relations are not user statements")
}

func (e *Engine) AmendRecord(ctx context.Context, r AmendRequest) (WriteResult, error) {
	return e.write(ctx, r.ProducerID, r.RequestID, "amend_record", r, func(tx *sql.Tx) ([]string, bool, error) {
		a := r.Amendment
		table := targetTable(a.TargetType)
		if !validID(a.ID) || !validID(a.TargetID) || table == "" || !text(a.Reason) || (a.Action != "retract" && a.Action != "replace") || (a.Mode != "extraction_error" && a.Mode != "user_statement") {
			return nil, false, fail(InvalidInput, "invalid amendment target, action, mode or reason")
		}
		if old, err := get[Amendment](ctx, tx, "amendments", a.ID); err == nil {
			one, _ := json.Marshal(old)
			two, _ := json.Marshal(a)
			if string(one) != string(two) {
				return nil, false, fail(IdempotencyConflict, "amendment id has different content")
			}
			return []string{a.ID}, false, nil
		} else if !HasCode(err, UnknownReference) {
			return nil, false, err
		}
		if _, err := get[json.RawMessage](ctx, tx, table, a.TargetID); err != nil {
			return nil, false, err
		}
		ok, err := active(ctx, tx, a.TargetType, a.TargetID)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, fail(InvalidInput, "target already amended")
		}
		if a.Action == "retract" && a.ReplacementID != "" {
			return nil, false, fail(InvalidInput, "retract cannot have a replacement")
		}
		if a.Action == "replace" {
			if a.ReplacementID == "" || a.TargetID == a.ReplacementID {
				return nil, false, fail(InvalidInput, "replacement must be a distinct existing record")
			}
			if _, err := get[json.RawMessage](ctx, tx, table, a.ReplacementID); err != nil {
				return nil, false, err
			}
			ok, err := active(ctx, tx, a.TargetType, a.ReplacementID)
			if err != nil {
				return nil, false, err
			}
			if !ok {
				return nil, false, fail(InvalidInput, "replacement is not active")
			}
			if a.TargetType != RelationTarget {
				old, err := targetObservation(ctx, tx, a.TargetType, a.TargetID)
				if err != nil {
					return nil, false, err
				}
				replacement, err := targetObservation(ctx, tx, a.TargetType, a.ReplacementID)
				if err != nil {
					return nil, false, err
				}
				var session string
				if err := tx.QueryRowContext(ctx, "SELECT session_id FROM messages WHERE id=?", old.MessageID).Scan(&session); err != nil {
					return nil, false, err
				}
				if _, err := messageInSession(ctx, tx, replacement.MessageID, session); err != nil {
					return nil, false, err
				}
				if a.Mode == "extraction_error" && old.MessageID != replacement.MessageID {
					return nil, false, fail(InvalidInput, "extraction replacement must retain the original user message")
				}
				if a.TargetType == BindingTarget && old.ID != replacement.ID {
					return nil, false, fail(InvalidInput, "binding replacement must belong to the same observation")
				}
			}
		}
		if a.Mode == "user_statement" {
			old, err := targetObservation(ctx, tx, a.TargetType, a.TargetID)
			if err != nil {
				return nil, false, err
			}
			var session string
			if err := tx.QueryRowContext(ctx, "SELECT session_id FROM messages WHERE id=?", old.MessageID).Scan(&session); err != nil {
				return nil, false, err
			}
			if a.EvidenceObservationID == a.TargetID && a.TargetType == ObservationTarget {
				return nil, false, fail(MissingEvidence, "a statement cannot be its own correction evidence")
			}
			if err := confirmation(ctx, tx, a.EvidenceObservationID, session); err != nil {
				return nil, false, err
			}
		} else if a.EvidenceObservationID != "" {
			if _, err := get[Observation](ctx, tx, "observations", a.EvidenceObservationID); err != nil {
				return nil, false, err
			}
		}
		added, err := put(ctx, tx, "amendments", a.ID, a)
		return []string{a.ID}, added, err
	})
}
