package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

type ErrorCode string

const (
	InvalidInput             ErrorCode = "INVALID_INPUT"
	MissingEvidence          ErrorCode = "MISSING_EVIDENCE"
	EvidenceMismatch         ErrorCode = "EVIDENCE_MISMATCH"
	UnknownReference         ErrorCode = "UNKNOWN_REFERENCE"
	AmbiguousBinding         ErrorCode = "AMBIGUOUS_BINDING"
	InvalidRelation          ErrorCode = "INVALID_RELATION"
	IdempotencyConflict      ErrorCode = "IDEMPOTENCY_CONFLICT"
	StaleCursor              ErrorCode = "STALE_CURSOR"
	StoreBusy                ErrorCode = "STORE_BUSY"
	SchemaVersionUnsupported ErrorCode = "SCHEMA_VERSION_UNSUPPORTED"
)

type Error struct {
	Code   ErrorCode
	Detail string
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Detail }

func fail(code ErrorCode, format string, args ...any) error {
	return &Error{Code: code, Detail: fmt.Sprintf(format, args...)}
}

func HasCode(err error, code ErrorCode) bool {
	var target *Error
	return errors.As(err, &target) && target.Code == code
}

// DecodeStrict is the JSON boundary for adapters. It rejects unrecognized fields,
// duplicate object keys and trailing JSON, including unsolicited judgment fields.
func DecodeStrict[T any](data []byte) (T, error) {
	var out T
	if err := uniqueKeys(data); err != nil {
		return out, fail(InvalidInput, "%v", err)
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&out); err != nil {
		return out, fail(InvalidInput, "%v", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return out, fail(InvalidInput, "expected a single JSON value")
	}
	return out, nil
}

func uniqueKeys(data []byte) error {
	d := json.NewDecoder(bytes.NewReader(data))
	var walk func() error
	walk = func() error {
		token, err := d.Token()
		if err != nil {
			return err
		}
		switch token {
		case json.Delim('{'):
			keys := map[string]bool{}
			for d.More() {
				t, err := d.Token()
				if err != nil {
					return err
				}
				key := t.(string)
				if keys[key] {
					return fmt.Errorf("duplicate JSON field %q", key)
				}
				keys[key] = true
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		case json.Delim('['):
			for d.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		}
		return nil
	}
	if err := walk(); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("expected a single JSON value")
	}
	return nil
}
