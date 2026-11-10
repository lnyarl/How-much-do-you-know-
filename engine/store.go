package engine

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

const schemaVersion = 1

type Engine struct{ db *sql.DB }

// Open initializes a local SQLite file, or opens an existing compatible store.
// The caller owns the directory. A database contains exactly one profile.
func Open(ctx context.Context, path string) (*Engine, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fail(InvalidInput, "database path is required")
	}
	var dsn string
	if path == ":memory:" {
		dsn = "file:memory-" + rand.Text() + "?mode=memory"
	} else {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		f, err := os.OpenFile(absolute, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
		dsn = (&url.URL{Scheme: "file", Path: absolute}).String()
	}
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	dsn += separator + "_pragma=foreign_keys(1)&_pragma=busy_timeout(3000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	e := &Engine{db: db}
	if err = e.migrate(ctx); err == nil {
		_, err = db.ExecContext(ctx, "PRAGMA journal_mode=WAL")
	}
	if err != nil {
		db.Close()
		return nil, storageError(err)
	}
	return e, nil
}

func (e *Engine) Close() error { return e.db.Close() }

func (e *Engine) migrate(ctx context.Context) error {
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > schemaVersion || version < 0 {
		return fail(SchemaVersionUnsupported, "database schema %d, supported %d", version, schemaVersion)
	}
	if version == schemaVersion {
		var stored int
		if err := tx.QueryRowContext(ctx, "SELECT schema_version FROM store_meta WHERE singleton=1").Scan(&stored); err != nil {
			return err
		}
		if stored != version {
			return fail(SchemaVersionUnsupported, "inconsistent schema metadata")
		}
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, schema); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO store_meta(singleton,profile_id,schema_version,ontology_version,data_revision) VALUES(1,?,1,1,0)", rand.Text()); err != nil {
		return err
	}
	all := []ConceptKind{Topic, ConceptNode, Mechanism, Procedure, Example}
	for _, name := range []string{"is_a", "part_of", "concretizes", "example_of", "requires", "related_to"} {
		subjects := all
		if name == "example_of" {
			subjects = []ConceptKind{Example}
		}
		rt := RelationType{ID: name, SubjectKinds: subjects, ObjectKinds: all, Symmetric: name == "related_to"}
		if _, err := put(ctx, tx, "relation_types", name, rt); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "PRAGMA user_version=1"); err != nil {
		return err
	}
	return tx.Commit()
}

const schema = `
CREATE TABLE store_meta(
 singleton INTEGER PRIMARY KEY CHECK(singleton=1), profile_id TEXT NOT NULL,
 schema_version INTEGER NOT NULL, ontology_version INTEGER NOT NULL, data_revision INTEGER NOT NULL
);
CREATE TABLE sources(id TEXT PRIMARY KEY, data TEXT NOT NULL CHECK(json_valid(data)));
CREATE TABLE source_versions(
 id TEXT PRIMARY KEY, data TEXT NOT NULL CHECK(json_valid(data)),
 content_hash TEXT NOT NULL,
 source_id TEXT GENERATED ALWAYS AS(json_extract(data,'$.source_id')) STORED REFERENCES sources(id)
);
CREATE TABLE concepts(
 id TEXT PRIMARY KEY, data TEXT NOT NULL CHECK(json_valid(data)),
 namespace TEXT GENERATED ALWAYS AS(json_extract(data,'$.namespace')) STORED NOT NULL,
 kind TEXT GENERATED ALWAYS AS(json_extract(data,'$.kind')) STORED NOT NULL CHECK(kind IN('topic','concept','mechanism','procedure','example')),
 label TEXT GENERATED ALWAYS AS(json_extract(data,'$.label')) STORED NOT NULL
);
CREATE INDEX concepts_namespace ON concepts(namespace,label,id);
CREATE TABLE concept_aliases(concept_id TEXT NOT NULL REFERENCES concepts(id),alias TEXT NOT NULL,PRIMARY KEY(concept_id,alias));
CREATE INDEX aliases_name ON concept_aliases(alias);
CREATE TABLE concept_sources(
 id TEXT PRIMARY KEY, data TEXT NOT NULL CHECK(json_valid(data)),
 concept_id TEXT GENERATED ALWAYS AS(json_extract(data,'$.concept_id')) STORED REFERENCES concepts(id),
 source_version_id TEXT GENERATED ALWAYS AS(json_extract(data,'$.source_version_id')) STORED REFERENCES source_versions(id)
);
CREATE TABLE relation_types(id TEXT PRIMARY KEY,data TEXT NOT NULL CHECK(json_valid(data)));
CREATE TABLE concept_relations(
 id TEXT PRIMARY KEY,data TEXT NOT NULL CHECK(json_valid(data)),
 subject_id TEXT GENERATED ALWAYS AS(json_extract(data,'$.subject_id')) STORED REFERENCES concepts(id),
 object_id TEXT GENERATED ALWAYS AS(json_extract(data,'$.object_id')) STORED REFERENCES concepts(id),
 type_id TEXT GENERATED ALWAYS AS(json_extract(data,'$.type_id')) STORED REFERENCES relation_types(id),
 source_version_id TEXT GENERATED ALWAYS AS(json_extract(data,'$.source_version_id')) STORED REFERENCES source_versions(id)
);
CREATE INDEX relations_subject ON concept_relations(subject_id);
CREATE INDEX relations_object ON concept_relations(object_id);
CREATE TABLE sessions(id TEXT PRIMARY KEY,data TEXT NOT NULL CHECK(json_valid(data)),
 producer_id TEXT GENERATED ALWAYS AS(json_extract(data,'$.producer_id')) STORED NOT NULL,
 external_session_id TEXT GENERATED ALWAYS AS(json_extract(data,'$.external_session_id')) STORED NOT NULL,
 UNIQUE(producer_id,external_session_id)
);
CREATE TABLE messages(id TEXT PRIMARY KEY,data TEXT NOT NULL CHECK(json_valid(data)),
 session_id TEXT NOT NULL REFERENCES sessions(id),
 external_message_id TEXT GENERATED ALWAYS AS(json_extract(data,'$.external_message_id')) STORED NOT NULL,
 UNIQUE(session_id,external_message_id)
);
CREATE TABLE observations(id TEXT PRIMARY KEY,data TEXT NOT NULL CHECK(json_valid(data)),
 message_id TEXT GENERATED ALWAYS AS(json_extract(data,'$.message_id')) STORED REFERENCES messages(id),
 recorded_at TEXT NOT NULL DEFAULT(strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX observations_message ON observations(message_id);
CREATE TABLE observation_concepts(id TEXT PRIMARY KEY,data TEXT NOT NULL CHECK(json_valid(data)),
 observation_id TEXT NOT NULL REFERENCES observations(id),
 concept_id TEXT GENERATED ALWAYS AS(json_extract(data,'$.concept_id')) STORED REFERENCES concepts(id),
 confirmation_observation_id TEXT GENERATED ALWAYS AS(nullif(json_extract(data,'$.confirmation_observation_id'),'')) STORED REFERENCES observations(id)
);
CREATE INDEX observation_concepts_concept ON observation_concepts(concept_id);
CREATE TABLE personal_assertions(id TEXT PRIMARY KEY,data TEXT NOT NULL CHECK(json_valid(data)),
 observation_id TEXT GENERATED ALWAYS AS(json_extract(data,'$.observation_id')) STORED REFERENCES observations(id),
 subject_id TEXT GENERATED ALWAYS AS(json_extract(data,'$.subject_id')) STORED REFERENCES concepts(id),
 object_concept_id TEXT GENERATED ALWAYS AS(nullif(json_extract(data,'$.object_concept_id'),'')) STORED REFERENCES concepts(id),
 confirmation_observation_id TEXT GENERATED ALWAYS AS(nullif(json_extract(data,'$.confirmation_observation_id'),'')) STORED REFERENCES observations(id)
);
CREATE TABLE amendments(id TEXT PRIMARY KEY,data TEXT NOT NULL CHECK(json_valid(data)),
 target_type TEXT GENERATED ALWAYS AS(json_extract(data,'$.target_type')) STORED NOT NULL,
 target_id TEXT GENERATED ALWAYS AS(json_extract(data,'$.target_id')) STORED NOT NULL,
 UNIQUE(target_type,target_id)
);
CREATE TABLE receipts(producer_id TEXT NOT NULL,request_id TEXT NOT NULL,operation TEXT NOT NULL,payload_hash TEXT NOT NULL,result TEXT NOT NULL,PRIMARY KEY(producer_id,request_id));
`

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func get[T any](ctx context.Context, q queryer, table, id string) (T, error) {
	var out T
	var body string
	err := q.QueryRowContext(ctx, "SELECT data FROM "+table+" WHERE id=?", id).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return out, fail(UnknownReference, "%s %q", table, id)
	}
	if err != nil {
		return out, err
	}
	err = json.Unmarshal([]byte(body), &out)
	return out, err
}

func list[T any](ctx context.Context, q queryer, table string) ([]T, error) {
	rows, err := q.QueryContext(ctx, "SELECT data FROM "+table+" ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var v T
		if err := json.Unmarshal([]byte(body), &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func put(ctx context.Context, tx *sql.Tx, table, id string, value any, extra ...any) (bool, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return false, err
	}
	var old string
	err = tx.QueryRowContext(ctx, "SELECT data FROM "+table+" WHERE id=?", id).Scan(&old)
	if err == nil {
		if old != string(body) {
			return false, fail(IdempotencyConflict, "%s ID %q has different content", table, id)
		}
		if len(extra) > 0 {
			column := extra[0].(string)
			var parent string
			if err := tx.QueryRowContext(ctx, "SELECT "+column+" FROM "+table+" WHERE id=?", id).Scan(&parent); err != nil {
				return false, err
			}
			if parent != extra[1].(string) {
				return false, fail(IdempotencyConflict, "%s ID %q has a different owner", table, id)
			}
		}
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	query := "INSERT INTO " + table + "(id,data) VALUES(?,?)"
	args := []any{id, string(body)}
	if len(extra) > 0 {
		query = "INSERT INTO " + table + "(id,data," + extra[0].(string) + ") VALUES(?,?,?)"
		args = append(args, extra[1])
	}
	_, err = tx.ExecContext(ctx, query, args...)
	return err == nil, err
}

func metadata(ctx context.Context, q queryer) (Metadata, error) {
	var m Metadata
	err := q.QueryRowContext(ctx, "SELECT profile_id,schema_version,ontology_version,data_revision FROM store_meta WHERE singleton=1").Scan(&m.ProfileID, &m.SchemaVersion, &m.OntologyVersion, &m.Revision)
	return m, err
}

func (e *Engine) Metadata(ctx context.Context) (Metadata, error) {
	m, err := metadata(ctx, e.db)
	return m, storageError(err)
}

func (e *Engine) write(ctx context.Context, producer, request, operation string, input any, fn func(*sql.Tx) ([]string, bool, error)) (WriteResult, error) {
	if !validID(producer) || !validID(request) {
		return WriteResult{}, fail(InvalidInput, "producer_id and request_id are required")
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return WriteResult{}, err
	}
	hash := digest(raw)
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return WriteResult{}, storageError(err)
	}
	defer tx.Rollback()
	// Acquire the SQLite write lock before reading receipts, including across processes.
	if _, err = tx.ExecContext(ctx, "UPDATE store_meta SET data_revision=data_revision WHERE singleton=1"); err != nil {
		return WriteResult{}, storageError(err)
	}
	var oldOp, oldHash, result string
	err = tx.QueryRowContext(ctx, "SELECT operation,payload_hash,result FROM receipts WHERE producer_id=? AND request_id=?", producer, request).Scan(&oldOp, &oldHash, &result)
	if err == nil {
		if oldOp != operation || oldHash != hash {
			return WriteResult{}, fail(IdempotencyConflict, "request key reused with different content")
		}
		var out WriteResult
		if err = json.Unmarshal([]byte(result), &out); err != nil {
			return out, err
		}
		return out, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return WriteResult{}, storageError(err)
	}
	ids, changed, err := fn(tx)
	if err != nil {
		return WriteResult{}, storageError(err)
	}
	if changed {
		if _, err = tx.ExecContext(ctx, "UPDATE store_meta SET data_revision=data_revision+1 WHERE singleton=1"); err != nil {
			return WriteResult{}, err
		}
	}
	m, err := metadata(ctx, tx)
	if err != nil {
		return WriteResult{}, err
	}
	out := WriteResult{Revision: m.Revision, IDs: ids}
	body, _ := json.Marshal(out)
	if _, err = tx.ExecContext(ctx, "INSERT INTO receipts VALUES(?,?,?,?,?)", producer, request, operation, hash, string(body)); err != nil {
		return WriteResult{}, storageError(err)
	}
	if err = tx.Commit(); err != nil {
		return WriteResult{}, storageError(err)
	}
	return out, nil
}

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func storageError(err error) error {
	if err == nil {
		return nil
	}
	var coded interface{ Code() int }
	if errors.As(err, &coded) {
		switch coded.Code() & 255 {
		case 5, 6:
			return fail(StoreBusy, "database is busy")
		case 19:
			return fail(IdempotencyConflict, "database constraint: %v", err)
		}
	}
	return err
}

func validID(s string) bool {
	return s != "" && s == strings.TrimSpace(s) && !strings.ContainsRune(s, 0) && utf8.ValidString(s)
}
func text(s string) bool { return strings.TrimSpace(s) != "" && utf8.ValidString(s) }

func validateQuote(content string, quote Quote) (Quote, error) {
	if !text(quote.Text) {
		return quote, fail(MissingEvidence, "nonempty quote is required")
	}
	if quote.Span == nil {
		if quote.Text != content {
			return quote, fail(EvidenceMismatch, "partial quotes require a byte span")
		}
		quote.Span = &ByteSpan{Start: 0, End: len(content)}
	}
	a, b := quote.Span.Start, quote.Span.End
	if a < 0 || b <= a || b > len(content) || !utf8.ValidString(content[:a]) || !utf8.ValidString(content[:b]) || content[a:b] != quote.Text {
		return quote, fail(EvidenceMismatch, "quote must match UTF-8 byte boundaries")
	}
	return quote, nil
}

func readTransaction[T any](ctx context.Context, e *Engine, fn func(*sql.Tx) (T, error)) (T, error) {
	var zero T
	tx, err := e.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return zero, storageError(err)
	}
	defer tx.Rollback()
	out, err := fn(tx)
	if err != nil {
		return zero, storageError(err)
	}
	if err := tx.Commit(); err != nil {
		return zero, storageError(err)
	}
	return out, nil
}

func active(ctx context.Context, q queryer, kind TargetType, id string) (bool, error) {
	var count int
	err := q.QueryRowContext(ctx, "SELECT count(*) FROM amendments WHERE target_type=? AND target_id=?", kind, id).Scan(&count)
	return count == 0, err
}

func parseRecorded(s string) (time.Time, error) { return time.Parse(time.RFC3339Nano, s) }
