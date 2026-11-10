// Package cli adapts terminal arguments and strict JSON to the shared engine.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"penumbra/engine"
)

const maxInputBytes = 16 * 1024 * 1024

const help = `Penumbra (페넘브라) — 개인 지식 기록

Usage: penu [--db PATH] COMMAND

  init                              Create an empty knowledge store
  meta                              Read store metadata
  concepts register --file FILE     Register ontology JSON
  concepts search [FLAGS] QUERY     Search concepts (flags precede QUERY)
  concepts sources --concept ID    Find source excerpts for a concept
  sources get --version ID         Read an original source version
  context get --concept ID         Get direct observations; repeat --concept
  feedback submit --file FILE       Submit user evidence JSON
  evidence get ID [ID...]           Read evidence and amendment history
  ontology explore --concept ID    Explore source-backed concept relations
  amend submit --file FILE          Retract or replace a record
  version                           Read CLI version

Global --db must precede COMMAND. Otherwise PENU_DB_PATH is used, then the
OS user config directory under penumbra/knowledge.db. Only init creates a store.
FILE can be - to read JSON from stdin. Success is JSON on stdout;
errors are JSON on stderr. Exit codes: 0 success, 2 input, 1 engine/I/O.

Read per-command flags with: penu context get --help
`

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type errorResponse struct {
	Error errorBody `json:"error"`
}

func inputError(format string, args ...any) error {
	return &engine.Error{Code: engine.InvalidInput, Detail: fmt.Sprintf(format, args...)}
}

func report(w io.Writer, err error) int {
	code := "IO_ERROR"
	exit := 1
	var domain *engine.Error
	if errors.As(err, &domain) {
		code = string(domain.Code)
		if domain.Code == engine.InvalidInput {
			exit = 2
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		code = "CANCELED"
	}
	_ = json.NewEncoder(w).Encode(errorResponse{Error: errorBody{Code: code, Message: err.Error()}})
	return exit
}

func flags(name string) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	return f
}

func parse(f *flag.FlagSet, args []string, stdout io.Writer) error {
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, "Usage:", f.Name())
			f.SetOutput(stdout)
			f.PrintDefaults()
			return flag.ErrHelp
		}
		return inputError("%v", err)
	}
	return nil
}

type stringsFlag []string

func (s *stringsFlag) String() string { return strings.Join(*s, ",") }
func (s *stringsFlag) Set(v string) error {
	if strings.TrimSpace(v) == "" {
		return inputError("empty identifier")
	}
	*s = append(*s, v)
	return nil
}

// Run does not exit the process. Input decoding and flag validation happen before
// opening a database, so invalid commands cannot create or migrate a store.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	global := flags("penu [--db PATH] COMMAND")
	dbPath := global.String("db", "", "SQLite path (or PENU_DB_PATH)")
	if err := parse(global, args, stdout); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, help)
			return 0
		}
		return report(stderr, err)
	}
	args = global.Args()
	if len(args) == 0 || args[0] == "help" {
		if len(args) > 1 {
			return report(stderr, inputError("use penu COMMAND --help"))
		}
		fmt.Fprint(stdout, help)
		return 0
	}
	if args[0] == "version" {
		if len(args) != 1 {
			return report(stderr, inputError("version takes no arguments"))
		}
		_ = json.NewEncoder(stdout).Encode(map[string]string{"name": "Penumbra", "command": "penu", "version": "0.1.0"})
		return 0
	}
	invoke, init, err := command(args, stdin, stdout)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		return report(stderr, err)
	}
	path := *dbPath
	if path == "" {
		path = os.Getenv("PENU_DB_PATH")
	}
	if path == "" {
		path = os.Getenv("A_DB_PATH") // Preserve explicitly configured pre-rename stores.
	}
	if path == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return report(stderr, err)
		}
		path, err = defaultStorePath(base)
		if err != nil {
			return report(stderr, err)
		}
	}
	if path == ":memory:" || strings.TrimSpace(path) == "" {
		return report(stderr, inputError("CLI requires a persistent database path"))
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return report(stderr, err)
	}
	if init {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return report(stderr, err)
		}
	} else if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			_ = json.NewEncoder(stderr).Encode(errorResponse{Error: errorBody{Code: "STORE_NOT_FOUND", Message: "store not found; run penu --db PATH init first"}})
			return 1
		}
		return report(stderr, err)
	}
	core, err := engine.Open(ctx, path)
	if err != nil {
		return report(stderr, err)
	}
	defer core.Close()
	var result any
	if init {
		meta, err := core.Metadata(ctx)
		if err != nil {
			return report(stderr, err)
		}
		result = struct {
			Path     string          `json:"path"`
			Metadata engine.Metadata `json:"metadata"`
		}{path, meta}
	} else {
		result, err = invoke(ctx, core)
		if err != nil {
			return report(stderr, err)
		}
	}
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(result); err != nil {
		return report(stderr, err)
	}
	return 0
}

type operation func(context.Context, *engine.Engine) (any, error)

func defaultStorePath(base string) (string, error) {
	current := filepath.Join(base, "penumbra", "knowledge.db")
	if _, err := os.Stat(current); err == nil {
		return current, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	legacy := filepath.Join(base, "a", "knowledge.db")
	if _, err := os.Stat(legacy); err == nil {
		return legacy, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return current, nil
}

func command(args []string, stdin io.Reader, stdout io.Writer) (operation, bool, error) {
	name := args[0]
	if name == "init" || name == "meta" {
		f := flags("penu " + name)
		if err := parse(f, args[1:], stdout); err != nil {
			return nil, false, err
		}
		if f.NArg() != 0 {
			return nil, false, inputError("%s takes no positional arguments", name)
		}
		return func(ctx context.Context, e *engine.Engine) (any, error) { return e.Metadata(ctx) }, name == "init", nil
	}
	if len(args) < 2 {
		return nil, false, inputError("unknown or incomplete command; run penu --help")
	}
	name += " " + args[1]
	f := flags("penu " + name)
	rest := args[2:]
	noPositionals := func() error {
		if f.NArg() != 0 {
			return inputError("unexpected positional arguments: %v; flags precede positional arguments", f.Args())
		}
		return nil
	}
	switch name {
	case "concepts register", "feedback submit", "amend submit":
		file := f.String("file", "", "JSON file, or - for stdin")
		if err := parse(f, rest, stdout); err != nil {
			return nil, false, err
		}
		if err := noPositionals(); err != nil {
			return nil, false, err
		}
		if *file == "" {
			return nil, false, inputError("--file is required")
		}
		data, err := readInput(*file, stdin)
		if err != nil {
			return nil, false, err
		}
		switch name {
		case "concepts register":
			r, err := engine.DecodeStrict[engine.RegisterOntologyRequest](data)
			if err != nil {
				return nil, false, err
			}
			return func(ctx context.Context, e *engine.Engine) (any, error) { return e.RegisterOntology(ctx, r) }, false, nil
		case "feedback submit":
			r, err := engine.DecodeStrict[engine.FeedbackRequest](data)
			if err != nil {
				return nil, false, err
			}
			return func(ctx context.Context, e *engine.Engine) (any, error) { return e.SubmitFeedback(ctx, r) }, false, nil
		default:
			r, err := engine.DecodeStrict[engine.AmendRequest](data)
			if err != nil {
				return nil, false, err
			}
			return func(ctx context.Context, e *engine.Engine) (any, error) { return e.AmendRecord(ctx, r) }, false, nil
		}
	case "concepts search":
		ns := f.String("namespace", "", "limit to a namespace")
		limit := f.Int("limit", 20, "maximum concepts, 1..1000")
		cursor := f.String("cursor", "", "next_cursor from the previous response")
		if err := parse(f, rest, stdout); err != nil {
			return nil, false, err
		}
		if f.NArg() != 1 {
			return nil, false, inputError("provide one quoted search term; flags precede the term")
		}
		r := engine.SearchRequest{Query: f.Arg(0), Namespace: *ns, Limit: *limit, Cursor: *cursor}
		return func(ctx context.Context, e *engine.Engine) (any, error) { return e.SearchConcepts(ctx, r) }, false, nil
	case "context get":
		var concepts stringsFlag
		f.Var(&concepts, "concept", "concept ID; repeat for multiple IDs")
		maxItems := f.Int("max-items", 30, "maximum observations, 1..1000")
		maxBytes := f.Int("max-bytes", 65536, "maximum JSON response bytes")
		candidates := f.Bool("include-candidates", false, "include explicitly marked candidate bindings")
		cursor := f.String("cursor", "", "next_cursor from the previous response")
		if err := parse(f, rest, stdout); err != nil {
			return nil, false, err
		}
		if err := noPositionals(); err != nil {
			return nil, false, err
		}
		if len(concepts) == 0 {
			return nil, false, inputError("--concept is required")
		}
		r := engine.ContextRequest{ConceptIDs: concepts, MaxItems: *maxItems, MaxBytes: *maxBytes, IncludeCandidates: *candidates, Cursor: *cursor}
		return func(ctx context.Context, e *engine.Engine) (any, error) { return e.GetContext(ctx, r) }, false, nil
	case "evidence get":
		if err := parse(f, rest, stdout); err != nil {
			return nil, false, err
		}
		if f.NArg() == 0 {
			return nil, false, inputError("at least one observation ID is required")
		}
		return func(ctx context.Context, e *engine.Engine) (any, error) { return e.GetEvidence(ctx, f.Args()) }, false, nil
	case "ontology explore":
		concept := f.String("concept", "", "starting concept ID")
		direction := f.String("direction", "both", "out, in, or both")
		depth := f.Int("depth", 2, "maximum relation depth, 0..20")
		limit := f.Int("limit", 100, "maximum nodes and edges, 1..1000")
		var relations stringsFlag
		f.Var(&relations, "relation-type", "relation type filter; repeat as needed")
		if err := parse(f, rest, stdout); err != nil {
			return nil, false, err
		}
		if err := noPositionals(); err != nil {
			return nil, false, err
		}
		if *concept == "" {
			return nil, false, inputError("--concept is required")
		}
		r := engine.ExploreRequest{ConceptID: *concept, RelationTypes: relations, Direction: *direction, Depth: *depth, Limit: *limit}
		return func(ctx context.Context, e *engine.Engine) (any, error) { return e.ExploreOntology(ctx, r) }, false, nil
	case "concepts sources":
		concept := f.String("concept", "", "concept ID")
		if err := parse(f, rest, stdout); err != nil {
			return nil, false, err
		}
		if err := noPositionals(); err != nil {
			return nil, false, err
		}
		if *concept == "" {
			return nil, false, inputError("--concept is required")
		}
		return func(ctx context.Context, e *engine.Engine) (any, error) { return e.GetConceptSources(ctx, *concept) }, false, nil
	case "sources get":
		version := f.String("version", "", "source version ID")
		if err := parse(f, rest, stdout); err != nil {
			return nil, false, err
		}
		if err := noPositionals(); err != nil {
			return nil, false, err
		}
		if *version == "" {
			return nil, false, inputError("--version is required")
		}
		return func(ctx context.Context, e *engine.Engine) (any, error) { return e.GetSourceVersion(ctx, *version) }, false, nil
	}
	return nil, false, inputError("unknown command %q; run penu --help", name)
}

func readInput(file string, stdin io.Reader) ([]byte, error) {
	reader := stdin
	if file != "-" {
		f, err := os.Open(file)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		reader = f
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxInputBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxInputBytes {
		return nil, inputError("JSON input exceeds 16 MiB")
	}
	return body, nil
}
