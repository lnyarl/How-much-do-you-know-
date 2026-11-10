package engine

import (
	"context"
	"database/sql"
)

// GetSourceVersion returns the original, immutable source text for explanation.
// Its contents are not a correctness judgment about the user's statements.
func (e *Engine) GetSourceVersion(ctx context.Context, id string) (SourceVersion, error) {
	if !validID(id) {
		return SourceVersion{}, fail(InvalidInput, "source version id is required")
	}
	return readTransaction(ctx, e, func(tx *sql.Tx) (SourceVersion, error) { return get[SourceVersion](ctx, tx, "source_versions", id) })
}

// GetConceptSources resolves source provenance without inferring user knowledge.
func (e *Engine) GetConceptSources(ctx context.Context, id string) ([]ConceptSource, error) {
	if !validID(id) {
		return nil, fail(InvalidInput, "concept id is required")
	}
	return readTransaction(ctx, e, func(tx *sql.Tx) ([]ConceptSource, error) {
		if _, err := get[Concept](ctx, tx, "concepts", id); err != nil {
			return nil, err
		}
		rows, err := tx.QueryContext(ctx, "SELECT data FROM concept_sources WHERE concept_id=? ORDER BY id", id)
		if err != nil {
			return nil, err
		}
		return decodeRows[ConceptSource](rows)
	})
}
