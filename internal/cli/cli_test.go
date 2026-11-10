package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"penumbra/engine"
	"penumbra/internal/cli"
)

func call(t *testing.T, path, input string, args ...string) (int, []byte, []byte) {
	t.Helper()
	var out, errout bytes.Buffer
	code := cli.Run(context.Background(), append([]string{"--db", path}, args...), strings.NewReader(input), &out, &errout)
	return code, out.Bytes(), errout.Bytes()
}

func ok[T any](t *testing.T, path, input string, args ...string) T {
	t.Helper()
	code, out, errout := call(t, path, input, args...)
	if code != 0 || len(errout) != 0 {
		t.Fatalf("%v: exit=%d stderr=%s stdout=%s", args, code, errout, out)
	}
	var result T
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("invalid JSON output: %v: %s", err, out)
	}
	return result
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "examples", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestConversationWorkflow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "with spaces?", "knowledge.db")
	version := ok[map[string]string](t, path, "", "version")
	if version["name"] != "Penumbra" || version["command"] != "penu" {
		t.Fatalf("wrong product identity: %v", version)
	}
	init := ok[struct{ Metadata engine.Metadata }](t, path, "", "init")
	reinit := ok[struct{ Metadata engine.Metadata }](t, path, "", "init")
	if init.Metadata.ProfileID != reinit.Metadata.ProfileID || init.Metadata.Revision != 0 {
		t.Fatal("init did not preserve the empty profile")
	}
	reg := ok[engine.WriteResult](t, path, fixture(t, "ontology.json"), "concepts", "register", "--file", "-")
	if reg.Revision != 1 {
		t.Fatal("ontology not stored")
	}
	found := ok[engine.SearchResult](t, path, "", "concepts", "search", "--namespace", "demo-db", "미등록개념")
	if len(found.Concepts) != 0 {
		t.Fatal("search fabricated a match")
	}
	found = ok[engine.SearchResult](t, path, "", "concepts", "search", "트랜잭션")
	if len(found.Concepts) != 1 || found.Concepts[0].ID != "demo-db:transaction" {
		t.Fatal("registered concept missing")
	}
	input := fixture(t, "feedback.json")
	write := ok[engine.WriteResult](t, path, input, "feedback", "submit", "--file", "-")
	retry := ok[engine.WriteResult](t, path, input, "feedback", "submit", "--file", "-")
	if write.Revision != retry.Revision {
		t.Fatal("CLI retry created a new observation")
	}
	contextArgs := []string{"context", "get", "--concept", "demo-db:transaction", "--concept", "demo-db:isolation"}
	context := ok[engine.ContextResult](t, path, "", contextArgs...)
	if len(context.Observations) != 1 || len(context.UnobservedConceptIDs) != 1 || context.UnobservedConceptIDs[0] != "demo-db:isolation" {
		t.Fatalf("knowledge boundary inferred: %+v", context)
	}
	links := ok[[]engine.ConceptSource](t, path, "", "concepts", "sources", "--concept", "demo-db:transaction")
	if len(links) != 1 {
		t.Fatal("missing source provenance")
	}
	source := ok[engine.SourceVersion](t, path, "", "sources", "get", "--version", links[0].SourceVersionID)
	if source.Content != "트랜잭션은 여러 작업을 묶는 개념이다. 격리 수준은 트랜잭션과 관련된 개념이다." {
		t.Fatal("source text changed")
	}
	graph := ok[engine.ExploreResult](t, path, "", "ontology", "explore", "--concept", "demo-db:transaction")
	if len(graph.Relations) != 1 {
		t.Fatal("ontology relation inaccessible")
	}
	ok[engine.WriteResult](t, path, fixture(t, "forgotten.json"), "feedback", "submit", "--file", "-")
	context = ok[engine.ContextResult](t, path, "", contextArgs...)
	if len(context.Signals) != 1 || context.Signals[0].Kind != engine.UserReportsForgotten || len(context.Observations) != 2 {
		t.Fatalf("forgetting report lost: %+v", context)
	}
	evidence := ok[[]engine.EvidenceResult](t, path, "", "evidence", "get", "demo:observation:1")
	if len(evidence) != 1 || !evidence[0].Active {
		t.Fatal("evidence inaccessible")
	}
	amend := engine.AmendRequest{ProducerID: "demo", RequestID: "demo-amendment", Amendment: engine.Amendment{ID: "demo:amendment", TargetType: engine.ObservationTarget, TargetID: "demo:observation:1", Action: "retract", Mode: "extraction_error", Reason: "demo retraction"}}
	raw, _ := json.Marshal(amend)
	ok[engine.WriteResult](t, path, string(raw), "amend", "submit", "--file", "-")
	evidence = ok[[]engine.EvidenceResult](t, path, "", "evidence", "get", "demo:observation:1")
	if evidence[0].Active || len(evidence[0].Amendments) != 1 {
		t.Fatal("retraction erased history")
	}
}

func TestErrorsAndHelpDoNotCreateStores(t *testing.T) {
	for _, args := range [][]string{
		{"unknown"},
		{"context", "get"},
		{"feedback", "submit", "--file", "-"},
		{"concepts", "search", "term", "--limit", "1"},
	} {
		path := filepath.Join(t.TempDir(), "untouched.db")
		code, out, errout := call(t, path, `{"score":100}`, args...)
		if code != 2 || len(out) != 0 || !json.Valid(errout) {
			t.Fatalf("invalid error channel for %v: %d %s %s", args, code, out, errout)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("invalid command created a store")
		}
	}
	for _, args := range [][]string{{"--help"}, {"context", "get", "--help"}, {"concepts", "register", "--help"}, {"version"}} {
		path := filepath.Join(t.TempDir(), "untouched.db")
		code, out, errout := call(t, path, "", args...)
		if code != 0 || len(out) == 0 || len(errout) != 0 {
			t.Fatalf("help/version failed: %v", args)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("help/version created a store")
		}
	}
	path := filepath.Join(t.TempDir(), "missing.db")
	code, out, errout := call(t, path, "", "meta")
	if code != 1 || len(out) != 0 || !strings.Contains(string(errout), "STORE_NOT_FOUND") {
		t.Fatalf("missing store error: %d %s %s", code, out, errout)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("read created a store")
	}
}

func TestFileInputEnvironmentAndEngineErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "knowledge.db")
	t.Setenv("A_DB_PATH", filepath.Join(t.TempDir(), "legacy.db"))
	t.Setenv("PENU_DB_PATH", path)
	var out, errout bytes.Buffer
	if code := cli.Run(context.Background(), []string{"init"}, strings.NewReader(""), &out, &errout); code != 0 {
		t.Fatal(errout.String())
	}
	filename := filepath.Join("..", "..", "examples", "ontology.json")
	ok[engine.WriteResult](t, path, "", "concepts", "register", "--file", filename)
	for _, raw := range []string{`{"score":100}`, `{"producer_id":"a","producer_id":"b"}`, `{} {}`} {
		code, out, errout := call(t, path, raw, "feedback", "submit", "--file", "-")
		if code != 2 || len(out) != 0 || !strings.Contains(string(errout), "INVALID_INPUT") {
			t.Fatal("strict JSON boundary bypassed")
		}
	}
	code, stdout, stderr := call(t, path, "", "evidence", "get", "missing")
	if code != 1 || len(stdout) != 0 || !strings.Contains(string(stderr), "UNKNOWN_REFERENCE") {
		t.Fatal("engine error not surfaced")
	}
}

func TestLegacyDatabaseEnvironmentRemainsReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.db")
	before := ok[struct{ Metadata engine.Metadata }](t, path, "", "init")
	t.Setenv("PENU_DB_PATH", "")
	t.Setenv("A_DB_PATH", path)
	var out, errout bytes.Buffer
	if code := cli.Run(context.Background(), []string{"meta"}, strings.NewReader(""), &out, &errout); code != 0 {
		t.Fatal(errout.String())
	}
	var after engine.Metadata
	if err := json.Unmarshal(out.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if after.ProfileID != before.Metadata.ProfileID {
		t.Fatal("legacy store was replaced")
	}
}
