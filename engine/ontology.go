package engine

import (
	"context"
	"database/sql"
	"slices"
	"strings"
)

func validConceptKind(k ConceptKind) bool {
	return slices.Contains([]ConceptKind{Topic, ConceptNode, Mechanism, Procedure, Example}, k)
}

func (e *Engine) RegisterOntology(ctx context.Context, r RegisterOntologyRequest) (WriteResult, error) {
	return e.write(ctx, r.ProducerID, r.RequestID, "register_ontology", r, func(tx *sql.Tx) ([]string, bool, error) {
		ids := []string{}
		changed := false
		save := func(table, id string, v any) error {
			added, err := put(ctx, tx, table, id, v)
			if err != nil {
				return err
			}
			changed = changed || added
			ids = append(ids, id)
			return nil
		}
		for _, s := range r.Sources {
			if !validID(s.ID) || !validID(s.Namespace) || !text(s.Title) {
				return nil, false, fail(InvalidInput, "source requires id, namespace, title")
			}
			if err := save("sources", s.ID, s); err != nil {
				return nil, false, err
			}
		}
		for _, v := range r.Versions {
			if !validID(v.ID) || !text(v.Content) {
				return nil, false, fail(InvalidInput, "source version requires id and content")
			}
			if _, err := get[Source](ctx, tx, "sources", v.SourceID); err != nil {
				return nil, false, err
			}
			added, err := put(ctx, tx, "source_versions", v.ID, v, "content_hash", digest([]byte(v.Content)))
			if err != nil {
				return nil, false, err
			}
			changed = changed || added
			ids = append(ids, v.ID)
		}
		for _, c := range r.Concepts {
			if !validID(c.ID) || !validID(c.Namespace) || !text(c.Label) || !validConceptKind(c.Kind) {
				return nil, false, fail(InvalidInput, "concept requires id, namespace, label, and a supported kind")
			}
			for _, a := range c.Aliases {
				if !text(a) {
					return nil, false, fail(InvalidInput, "empty alias")
				}
			}
			if err := save("concepts", c.ID, c); err != nil {
				return nil, false, err
			}
			for _, alias := range append([]string{c.Label}, c.Aliases...) {
				if _, err := tx.ExecContext(ctx, "INSERT INTO concept_aliases VALUES(?,?) ON CONFLICT DO NOTHING", c.ID, alias); err != nil {
					return nil, false, err
				}
			}
		}
		for _, rt := range r.RelationTypes {
			if !validID(rt.ID) || len(rt.SubjectKinds) == 0 || len(rt.ObjectKinds) == 0 {
				return nil, false, fail(InvalidRelation, "relation type requires an id and allowed node kinds")
			}
			for _, k := range append(slices.Clone(rt.SubjectKinds), rt.ObjectKinds...) {
				if !validConceptKind(k) {
					return nil, false, fail(InvalidRelation, "unknown node kind")
				}
			}
			if err := save("relation_types", rt.ID, rt); err != nil {
				return nil, false, err
			}
		}
		for _, cs := range r.ConceptSources {
			if !validID(cs.ID) {
				return nil, false, fail(InvalidInput, "concept source requires id")
			}
			if _, err := get[Concept](ctx, tx, "concepts", cs.ConceptID); err != nil {
				return nil, false, err
			}
			version, err := get[SourceVersion](ctx, tx, "source_versions", cs.SourceVersionID)
			if err != nil {
				return nil, false, err
			}
			cs.Quote, err = validateQuote(version.Content, cs.Quote)
			if err != nil {
				return nil, false, err
			}
			if err := save("concept_sources", cs.ID, cs); err != nil {
				return nil, false, err
			}
		}
		for _, rel := range r.Relations {
			if !validID(rel.ID) {
				return nil, false, fail(InvalidInput, "relation requires id")
			}
			subject, err := get[Concept](ctx, tx, "concepts", rel.SubjectID)
			if err != nil {
				return nil, false, err
			}
			object, err := get[Concept](ctx, tx, "concepts", rel.ObjectID)
			if err != nil {
				return nil, false, err
			}
			rt, err := get[RelationType](ctx, tx, "relation_types", rel.TypeID)
			if err != nil {
				return nil, false, err
			}
			if !slices.Contains(rt.SubjectKinds, subject.Kind) || !slices.Contains(rt.ObjectKinds, object.Kind) || (!rt.AllowSelf && subject.ID == object.ID) {
				return nil, false, fail(InvalidRelation, "relation violates domain, range, or self-reference constraint")
			}
			version, err := get[SourceVersion](ctx, tx, "source_versions", rel.SourceVersionID)
			if err != nil {
				return nil, false, err
			}
			rel.Quote, err = validateQuote(version.Content, rel.Quote)
			if err != nil {
				return nil, false, err
			}
			if err := save("concept_relations", rel.ID, rel); err != nil {
				return nil, false, err
			}
		}
		if changed {
			if _, err := tx.ExecContext(ctx, "UPDATE store_meta SET ontology_version=ontology_version+1"); err != nil {
				return nil, false, err
			}
		}
		return ids, changed, nil
	})
}

func validateExplicit(ctx context.Context, tx *sql.Tx, conceptID, quote string) error {
	concept, err := get[Concept](ctx, tx, "concepts", conceptID)
	if err != nil {
		return err
	}
	for _, name := range append([]string{concept.Label}, concept.Aliases...) {
		if !strings.Contains(quote, name) {
			continue
		}
		rows, err := tx.QueryContext(ctx, "SELECT DISTINCT concept_id FROM concept_aliases WHERE alias=?", name)
		if err != nil {
			return err
		}
		ambiguous := false
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			if id != conceptID {
				ambiguous = true
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if !ambiguous {
			return nil
		}
	}
	return fail(AmbiguousBinding, "no unambiguous concept name in quoted user text; use candidate or a specific user confirmation")
}
