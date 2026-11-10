package engine_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"penumbra/engine"
)

var ctx = context.Background()

const sourceText = "Mutex is connected to mutual exclusion. Transactions and races are separate concepts."

func open(t *testing.T) (*engine.Engine, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "knowledge.db")
	e, err := engine.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	return e, path
}

func seedRequest() engine.RegisterOntologyRequest {
	return engine.RegisterOntologyRequest{
		ProducerID: "fixture", RequestID: "ontology",
		Sources:  []engine.Source{{ID: "source", Namespace: "cs", Title: "Source"}},
		Versions: []engine.SourceVersion{{ID: "v1", SourceID: "source", Content: sourceText}},
		Concepts: []engine.Concept{
			{ID: "mutex", Namespace: "cs", Kind: engine.Mechanism, Label: "Mutex", Aliases: []string{"Lock"}},
			{ID: "mutual", Namespace: "cs", Kind: engine.ConceptNode, Label: "Mutual exclusion"},
			{ID: "thread", Namespace: "cs", Kind: engine.ConceptNode, Label: "Thread"},
			{ID: "transaction", Namespace: "cs", Kind: engine.ConceptNode, Label: "Transaction"},
			{ID: "race", Namespace: "cs", Kind: engine.ConceptNode, Label: "Race"},
		},
		ConceptSources: []engine.ConceptSource{{ID: "cs1", ConceptID: "mutex", SourceVersionID: "v1", Quote: engine.Quote{Text: sourceText}}},
		Relations:      []engine.Relation{{ID: "r1", SubjectID: "mutex", TypeID: "concretizes", ObjectID: "mutual", SourceVersionID: "v1", Quote: engine.Quote{Text: sourceText}}},
	}
}

func seed(t *testing.T, e *engine.Engine) {
	t.Helper()
	if _, err := e.RegisterOntology(ctx, seedRequest()); err != nil {
		t.Fatal(err)
	}
}

func feedback(id, content string, kind engine.ObservationKind) engine.FeedbackRequest {
	return engine.FeedbackRequest{
		ProducerID: "assistant", RequestID: id, SessionID: "session", ExternalSessionID: "external-session",
		Messages: []engine.Message{{ID: "m-" + id, ExternalMessageID: "turn-" + id, Role: "user", Content: content, ObservedAt: time.Date(2026, 11, 10, 4, 0, 0, 0, time.UTC)}},
		Observations: []engine.Observation{{
			ID: "o-" + id, Kind: kind, MessageID: "m-" + id, Quote: engine.Quote{Text: content},
			Concepts: []engine.ConceptBinding{{ID: "b-" + id, ConceptID: "mutex", Binding: engine.Explicit}},
		}},
	}
}

func submit(t *testing.T, e *engine.Engine, r engine.FeedbackRequest) engine.WriteResult {
	t.Helper()
	result, err := e.SubmitFeedback(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func query(t *testing.T, e *engine.Engine, ids ...string) engine.ContextResult {
	t.Helper()
	v, err := e.GetContext(ctx, engine.ContextRequest{ConceptIDs: ids, MaxItems: 100, MaxBytes: 100000})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func code(t *testing.T, err error, want engine.ErrorCode) {
	t.Helper()
	if !engine.HasCode(err, want) {
		t.Fatalf("error=%v; want code %s", err, want)
	}
}

func TestDirectObservationsDoNotSpreadThroughOntology(t *testing.T) {
	e, _ := open(t)
	seed(t, e)
	submit(t, e, feedback("one", "Mutex means everything runs at once.", engine.UserExplanation))
	got := query(t, e, "mutex", "mutual", "thread", "missing")
	if len(got.Observations) != 1 || got.Observations[0].Observation.Quote.Text != "Mutex means everything runs at once." {
		t.Fatalf("raw explanation lost: %+v", got)
	}
	if !reflect.DeepEqual(got.UnobservedConceptIDs, []string{"mutual", "thread"}) || !reflect.DeepEqual(got.UnknownConceptIDs, []string{"missing"}) {
		t.Fatalf("invented knowledge or wrong coverage: %+v", got)
	}
	graph, err := e.ExploreOntology(ctx, engine.ExploreRequest{ConceptID: "mutex", Direction: "out", Depth: 3, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Concepts) != 2 || len(graph.Relations) != 1 {
		t.Fatalf("ontology unavailable: %+v", graph)
	}
	if len(query(t, e, "mutual").Observations) != 0 {
		t.Fatal("graph exploration created user knowledge")
	}
}

func TestKindsAndRecallRemainReports(t *testing.T) {
	e, _ := open(t)
	seed(t, e)
	kinds := []engine.ObservationKind{engine.UserClaimsFamiliarity, engine.UserClaimsUnfamiliarity, engine.UserReportsForgotten, engine.UserRequestsExplanation, engine.UserReportsRecalled, engine.UserAttempt}
	for i, k := range kinds {
		r := feedback(fmt.Sprint(i), "Mutex: "+string(k), k)
		r.Messages[0].ObservedAt = r.Messages[0].ObservedAt.Add(time.Duration(i) * time.Hour)
		if i == 2 {
			r.Messages[0].ObservedAt = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
		}
		submit(t, e, r)
	}
	first := query(t, e, "mutex")
	second := query(t, e, "mutex")
	if !reflect.DeepEqual(first, second) {
		t.Fatal("query generated a time-based state change")
	}
	if len(first.Observations) != 6 || len(first.Signals) != 3 {
		t.Fatalf("reports overwritten or inferred: %+v", first)
	}
	if first.Observations[len(first.Observations)-1].Observation.Kind != engine.UserReportsForgotten {
		t.Fatal("recording order replaced observed time")
	}
	for _, o := range first.Observations {
		if o.Observation.Assistance != engine.UnknownAssistance {
			t.Fatal("unknown assistance inferred")
		}
	}
	m, _ := e.Metadata(ctx)
	if m.Revision != 7 {
		t.Fatalf("reads changed revision: %d", m.Revision)
	}
}

func TestExposureAndAcknowledgmentDoNotCreateUnderstanding(t *testing.T) {
	e, _ := open(t)
	seed(t, e)
	r := engine.FeedbackRequest{ProducerID: "assistant", RequestID: "exposure", SessionID: "s", ExternalSessionID: "s", Messages: []engine.Message{
		{ID: "explain", ExternalMessageID: "1", Role: "assistant", Content: "Mutex is mutual exclusion.", ObservedAt: time.Now()},
		{ID: "ack", ExternalMessageID: "2", Role: "user", Content: "네", ObservedAt: time.Now()},
	}}
	submit(t, e, r)
	got := query(t, e, "mutex")
	if len(got.Observations) != 0 || !reflect.DeepEqual(got.UnobservedConceptIDs, []string{"mutex"}) {
		t.Fatalf("exposure treated as understanding: %+v", got)
	}
	r.RequestID = "invalid-ai-observation"
	r.Observations = []engine.Observation{{ID: "ai-obs", Kind: engine.UserExplanation, MessageID: "explain", Quote: engine.Quote{Text: "Mutex is mutual exclusion."}}}
	_, err := e.SubmitFeedback(ctx, r)
	code(t, err, engine.MissingEvidence)
}

func TestCandidateBindingIsNotConfirmedKnowledge(t *testing.T) {
	e, _ := open(t)
	seed(t, e)
	r := feedback("candidate", "배웠는데 기억이 안 나요.", engine.UserReportsForgotten)
	r.Observations[0].Concepts[0].Binding = engine.Candidate
	submit(t, e, r)
	got := query(t, e, "mutex")
	if len(got.Observations) != 0 || len(got.Signals) != 0 || len(got.UnobservedConceptIDs) != 1 {
		t.Fatalf("candidate promoted: %+v", got)
	}
	included, err := e.GetContext(ctx, engine.ContextRequest{ConceptIDs: []string{"mutex"}, MaxItems: 10, MaxBytes: 10000, IncludeCandidates: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(included.Observations) != 1 || len(included.CandidateBindings) != 1 || len(included.Signals) != 0 || len(included.UnobservedConceptIDs) != 1 {
		t.Fatalf("candidate inclusion changed coverage: %+v", included)
	}
}

func TestUserConfirmationCanBindAnOtherwiseUnstatedConcept(t *testing.T) {
	e, _ := open(t)
	seed(t, e)
	r := feedback("confirmed", "기억이 안 나요.", engine.UserReportsForgotten)
	confirm := engine.Observation{ID: "confirmation", Kind: engine.UserConfirmsReference, MessageID: "confirm-message", Quote: engine.Quote{Text: "네, Mutex를 말한 겁니다."}}
	r.Messages = append(r.Messages, engine.Message{ID: "confirm-message", ExternalMessageID: "confirm-turn", Role: "user", Content: confirm.Quote.Text, ObservedAt: time.Now()})
	r.Observations = append(r.Observations, confirm)
	r.Observations[0].Concepts[0].Binding = engine.UserConfirmed
	r.Observations[0].Concepts[0].ConfirmationObservationID = confirm.ID
	submit(t, e, r)
	if got := query(t, e, "mutex"); len(got.Signals) != 1 {
		t.Fatalf("confirmed report missing: %+v", got)
	}
	_, err := e.AmendRecord(ctx, engine.AmendRequest{ProducerID: "assistant", RequestID: "withdraw-confirm", Amendment: engine.Amendment{ID: "withdraw", TargetType: engine.ObservationTarget, TargetID: confirm.ID, Action: "retract", Reason: "reference extraction error", Mode: "extraction_error"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := query(t, e, "mutex"); len(got.Observations) != 0 || len(got.UnobservedConceptIDs) != 1 {
		t.Fatalf("withdrawn confirmation still effective: %+v", got)
	}
}

func TestAmbiguousAndUnstatedNamesCannotBecomeExplicitBindings(t *testing.T) {
	e, _ := open(t)
	seed(t, e)
	_, err := e.RegisterOntology(ctx, engine.RegisterOntologyRequest{ProducerID: "fixture", RequestID: "other-lock", Concepts: []engine.Concept{{ID: "db-lock", Namespace: "db", Kind: engine.ConceptNode, Label: "DBLock", Aliases: []string{"Lock"}}}})
	if err != nil {
		t.Fatal(err)
	}
	for i, content := range []string{"Lock은 알고 있어요.", "이건 알고 있어요."} {
		_, err := e.SubmitFeedback(ctx, feedback(fmt.Sprint(i), content, engine.UserClaimsFamiliarity))
		code(t, err, engine.AmbiguousBinding)
	}
}

func TestUTF8EvidenceValidationAndAtomicRollback(t *testing.T) {
	e, _ := open(t)
	seed(t, e)
	before, _ := e.Metadata(ctx)
	bad := feedback("rollback", "한글 Mutex", engine.UserExplanation)
	bad.Observations[0].Quote = engine.Quote{Text: "한", Span: &engine.ByteSpan{Start: 1, End: 3}}
	_, err := e.SubmitFeedback(ctx, bad)
	code(t, err, engine.EvidenceMismatch)
	after, _ := e.Metadata(ctx)
	if before != after {
		t.Fatal("failed write changed metadata")
	}
	_, err = e.GetEvidence(ctx, []string{"o-rollback"})
	code(t, err, engine.UnknownReference)
	// Same IDs with different content succeed only if the failed bundle left no rows.
	submit(t, e, feedback("rollback", "Mutex는 모르겠어요.", engine.UserClaimsUnfamiliarity))
	part := feedback("partial", "한글 Mutex 설명", engine.UserExplanation)
	part.Observations[0].Quote = engine.Quote{Text: "Mutex", Span: &engine.ByteSpan{Start: 7, End: 12}}
	submit(t, e, part)
}

func TestIdempotencySurvivesRestartAndNoOpRegistration(t *testing.T) {
	e, path := open(t)
	seed(t, e)
	r := feedback("retry", "Mutex는 알고 있어요.", engine.UserClaimsFamiliarity)
	one := submit(t, e, r)
	two := submit(t, e, r)
	if !reflect.DeepEqual(one, two) {
		t.Fatalf("retry changed result: %+v %+v", one, two)
	}
	newKey := r
	newKey.RequestID = "another-key"
	three := submit(t, e, newKey)
	if three.Revision != one.Revision {
		t.Fatal("same records changed revision")
	}
	reg := seedRequest()
	reg.RequestID = "same-ontology"
	registered, err := e.RegisterOntology(ctx, reg)
	if err != nil {
		t.Fatal(err)
	}
	if registered.Revision != one.Revision {
		t.Fatal("no-op ontology registration changed revision")
	}
	before := query(t, e, "mutex")
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := engine.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := query(t, reopened, "mutex"); !reflect.DeepEqual(before, got) {
		t.Fatal("restart changed context")
	}
	if got := submit(t, reopened, r); !reflect.DeepEqual(got, one) {
		t.Fatal("persistent receipt lost")
	}
	r.Messages[0].Content = "changed"
	_, err = reopened.SubmitFeedback(ctx, r)
	code(t, err, engine.IdempotencyConflict)
}

func TestContextPaginationBudgetAndStaleCursor(t *testing.T) {
	e, _ := open(t)
	seed(t, e)
	submit(t, e, feedback("a", "Mutex A", engine.UserExplanation))
	submit(t, e, feedback("b", "Mutex B", engine.UserExplanation))
	r := engine.ContextRequest{ConceptIDs: []string{"mutex"}, MaxItems: 1, MaxBytes: 10000}
	first, err := e.GetContext(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Observations) != 1 || first.NextCursor == "" {
		t.Fatalf("missing page: %+v", first)
	}
	r.Cursor = first.NextCursor
	second, err := e.GetContext(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Observations) != 1 || second.Observations[0].Observation.ID == first.Observations[0].Observation.ID || second.NextCursor != "" {
		t.Fatal("page repeated or skipped")
	}
	submit(t, e, feedback("c", "Mutex C", engine.UserExplanation))
	_, err = e.GetContext(ctx, r)
	code(t, err, engine.StaleCursor)
	long := feedback("long", "Mutex "+strings.Repeat("가", 4000), engine.UserExplanation)
	long.Messages[0].ObservedAt = time.Now()
	submit(t, e, long)
	budget := engine.ContextRequest{ConceptIDs: []string{"mutex", "mutual"}, MaxItems: 1, MaxBytes: 1400}
	got, err := e.GetContext(ctx, budget)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(got)
	if len(body) > budget.MaxBytes || len(got.OmittedEvidence) != 1 || !reflect.DeepEqual(got.UnobservedConceptIDs, []string{"mutual"}) {
		t.Fatalf("budget changed coverage: bytes=%d %+v", len(body), got)
	}
	evidence, err := e.GetEvidence(ctx, []string{got.OmittedEvidence[0].ObservationID})
	if err != nil {
		t.Fatal(err)
	}
	if evidence[0].Observation.Observation.Quote.Text != long.Messages[0].Content {
		t.Fatal("oversized evidence inaccessible")
	}
}

func TestAmendmentPreservesHistoryAndCannotCycle(t *testing.T) {
	e, _ := open(t)
	seed(t, e)
	submit(t, e, feedback("original", "Mutex is one thing.", engine.UserExplanation))
	r := engine.AmendRequest{ProducerID: "assistant", RequestID: "withdraw", Amendment: engine.Amendment{ID: "amendment", TargetType: engine.ObservationTarget, TargetID: "o-original", Action: "retract", Reason: "wrong extraction", Mode: "extraction_error"}}
	first, err := e.AmendRecord(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := e.AmendRecord(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, retry) {
		t.Fatal("amendment retry changed result")
	}
	if got := query(t, e, "mutex"); len(got.Observations) != 0 {
		t.Fatal("retracted observation still in default context")
	}
	history, err := e.GetEvidence(ctx, []string{"o-original"})
	if err != nil {
		t.Fatal(err)
	}
	if history[0].Active || len(history[0].Amendments) != 1 || history[0].Observation.Message.Content != "Mutex is one thing." {
		t.Fatal("history was overwritten")
	}
	submit(t, e, feedback("new", "Mutex is another thing.", engine.UserExplanation))
	bad := engine.AmendRequest{ProducerID: "assistant", RequestID: "cycle", Amendment: engine.Amendment{ID: "cycle", TargetType: engine.ObservationTarget, TargetID: "o-new", Action: "replace", ReplacementID: "o-new", Reason: "cycle", Mode: "extraction_error"}}
	_, err = e.AmendRecord(ctx, bad)
	code(t, err, engine.InvalidInput)
	bad.Amendment.ReplacementID = "o-original"
	_, err = e.AmendRecord(ctx, bad)
	code(t, err, engine.InvalidInput)
}

func TestPersonalAssertionsPreserveUserContentWithoutJudgment(t *testing.T) {
	e, _ := open(t)
	seed(t, e)
	r := feedback("belief", "Transaction prevents every Race.", engine.UserExplanation)
	r.Observations[0].Concepts = []engine.ConceptBinding{{ID: "belief-binding", ConceptID: "transaction", Binding: engine.Explicit}}
	r.Assertions = []engine.PersonalAssertion{{ID: "claim", ObservationID: "o-belief", SubjectID: "transaction", PredicateText: "prevents", ObjectText: "every Race", ObjectConceptID: "race"}}
	submit(t, e, r)
	got, err := e.GetEvidence(ctx, []string{"o-belief"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got[0].Assertions) != 1 || got[0].Assertions[0].Binding != engine.Candidate || got[0].Assertions[0].ObjectText != "every Race" {
		t.Fatalf("belief changed or promoted: %+v", got)
	}
	if len(query(t, e, "race").Observations) != 0 {
		t.Fatal("assertion object generated knowledge observation")
	}
}

func TestSearchMatchesAliasesWithoutMerging(t *testing.T) {
	e, _ := open(t)
	seed(t, e)
	reg := engine.RegisterOntologyRequest{ProducerID: "fixture", RequestID: "duplicate-label", Concepts: []engine.Concept{{ID: "other-mutex", Namespace: "other", Kind: engine.ConceptNode, Label: "Mutex"}}}
	if _, err := e.RegisterOntology(ctx, reg); err != nil {
		t.Fatal(err)
	}
	result, err := e.SearchConcepts(ctx, engine.SearchRequest{Query: "Mutex", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Concepts) != 1 || result.NextCursor == "" {
		t.Fatal("same labels merged")
	}
	next, err := e.SearchConcepts(ctx, engine.SearchRequest{Query: "Mutex", Limit: 1, Cursor: result.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if next.Concepts[0].ID == result.Concepts[0].ID {
		t.Fatal("search repeated page")
	}
	alias, err := e.SearchConcepts(ctx, engine.SearchRequest{Query: "Lock", Namespace: "cs", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(alias.Concepts) != 1 || alias.Concepts[0].ID != "mutex" {
		t.Fatal("alias lookup failed")
	}
	none, err := e.SearchConcepts(ctx, engine.SearchRequest{Query: "%", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(none.Concepts) != 0 {
		t.Fatal("search interpreted literal as SQL wildcard")
	}
}

func TestInvalidRelationRollsBackOntology(t *testing.T) {
	e, _ := open(t)
	seed(t, e)
	r := engine.RegisterOntologyRequest{ProducerID: "fixture", RequestID: "invalid-domain", Concepts: []engine.Concept{{ID: "new", Namespace: "cs", Kind: engine.ConceptNode, Label: "New"}}, Relations: []engine.Relation{{ID: "bad", SubjectID: "mutex", TypeID: "example_of", ObjectID: "mutual", SourceVersionID: "v1", Quote: engine.Quote{Text: sourceText}}}}
	_, err := e.RegisterOntology(ctx, r)
	code(t, err, engine.InvalidRelation)
	if got := query(t, e, "new"); len(got.UnknownConceptIDs) != 1 {
		t.Fatal("partial ontology persisted")
	}
}

func TestStrictJSONRejectsJudgmentsAndDuplicateFields(t *testing.T) {
	for _, raw := range []string{
		`{"producer_id":"a","score":100}`,
		`{"observations":[{"kind":"user_attempt","correct":true}]}`,
		`{"producer_id":"a","producer_id":"b"}`,
		`{} {}`,
	} {
		_, err := engine.DecodeStrict[engine.FeedbackRequest]([]byte(raw))
		code(t, err, engine.InvalidInput)
	}
}

func TestConcurrentWritersAndCanceledRequest(t *testing.T) {
	e, path := open(t)
	seed(t, e)
	other, err := engine.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := range 12 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			target := e
			if i%2 == 1 {
				target = other
			}
			_, err := target.SubmitFeedback(ctx, feedback(fmt.Sprintf("concurrent-%d", i), "Mutex", engine.UserAttempt))
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(query(t, e, "mutex").Observations) != 12 {
		t.Fatal("concurrent writes lost observations")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := e.SubmitFeedback(canceled, feedback("canceled", "Mutex", engine.UserAttempt)); err == nil {
		t.Fatal("cancellation ignored")
	}
	if len(query(t, e, "mutex").Observations) != 12 {
		t.Fatal("canceled request persisted")
	}
}

func TestUnsupportedSchemaDoesNotMigrate(t *testing.T) {
	e, path := open(t)
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version=999"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	unsupported, err := engine.Open(ctx, path)
	if unsupported != nil {
		unsupported.Close()
		t.Fatal("unsupported database opened")
	}
	code(t, err, engine.SchemaVersionUnsupported)
}

func TestGenericAcknowledgmentCannotConfirmAConcept(t *testing.T) {
	e, _ := open(t)
	seed(t, e)
	r := feedback("ambiguous-confirmation", "기억이 안 나요.", engine.UserReportsForgotten)
	r.Messages = append(r.Messages, engine.Message{ID: "yes", ExternalMessageID: "yes", Role: "user", Content: "네", ObservedAt: time.Now()})
	r.Observations = append(r.Observations, engine.Observation{ID: "yes-observation", Kind: engine.UserConfirmsReference, MessageID: "yes", Quote: engine.Quote{Text: "네"}})
	r.Observations[0].Concepts[0].Binding = engine.UserConfirmed
	r.Observations[0].Concepts[0].ConfirmationObservationID = "yes-observation"
	_, err := e.SubmitFeedback(ctx, r)
	code(t, err, engine.AmbiguousBinding)
	if len(query(t, e, "mutex").Observations) != 0 {
		t.Fatal("ambiguous confirmation persisted")
	}
}

func TestBindingCanBeReplacedWithoutChangingTheObservation(t *testing.T) {
	e, _ := open(t)
	seed(t, e)
	r := feedback("binding-replacement", "Mutex에 대한 설명입니다.", engine.UserExplanation)
	r.Observations[0].Concepts[0].Binding = engine.Candidate
	submit(t, e, r)
	r.RequestID = "binding-replacement-2"
	r.Observations[0].Concepts[0].ID = "explicit-replacement"
	r.Observations[0].Concepts[0].Binding = engine.Explicit
	submit(t, e, r)
	_, err := e.AmendRecord(ctx, engine.AmendRequest{ProducerID: "assistant", RequestID: "replace-binding", Amendment: engine.Amendment{
		ID: "replace-binding", TargetType: engine.BindingTarget, TargetID: "b-binding-replacement", Action: "replace", ReplacementID: "explicit-replacement", Mode: "extraction_error", Reason: "resolved direct name",
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.GetContext(ctx, engine.ContextRequest{ConceptIDs: []string{"mutex"}, MaxItems: 10, MaxBytes: 10000, IncludeCandidates: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Observations) != 1 || len(got.CandidateBindings) != 0 || got.Observations[0].Observation.Concepts[0].ID != "explicit-replacement" {
		t.Fatalf("replacement failed: %+v", got)
	}
	history, err := e.GetEvidence(ctx, []string{"o-binding-replacement"})
	if err != nil {
		t.Fatal(err)
	}
	if len(history[0].Observation.Observation.Concepts) != 2 || len(history[0].Amendments) != 1 {
		t.Fatal("binding history lost")
	}
}

func TestFailureDuringWriteRollsBackPreviouslyInsertedMessages(t *testing.T) {
	e, path := open(t)
	seed(t, e)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec("CREATE TRIGGER fail_observation BEFORE INSERT ON observations BEGIN SELECT RAISE(ABORT,'injected write failure'); END")
	if err != nil {
		t.Fatal(err)
	}
	before, _ := e.Metadata(ctx)
	_, err = e.SubmitFeedback(ctx, feedback("write-failure", "Mutex first content", engine.UserExplanation))
	if err == nil {
		t.Fatal("injected failure was ignored")
	}
	after, _ := e.Metadata(ctx)
	if before != after {
		t.Fatal("partial write changed revision")
	}
	if _, err := db.Exec("DROP TRIGGER fail_observation"); err != nil {
		t.Fatal(err)
	}
	// New text with the same message ID proves the earlier message was rolled back.
	submit(t, e, feedback("write-failure", "Mutex replacement content", engine.UserExplanation))
	if got := query(t, e, "mutex"); len(got.Observations) != 1 || got.Observations[0].Message.Content != "Mutex replacement content" {
		t.Fatal("partial message or receipt survived rollback")
	}
}

func TestCrossSessionEvidenceAndMissingReferencesAreRejected(t *testing.T) {
	e, _ := open(t)
	seed(t, e)
	original := feedback("session-original", "Mutex", engine.UserExplanation)
	submit(t, e, original)
	cross := feedback("cross", "Mutex", engine.UserExplanation)
	cross.SessionID = "other-session"
	cross.ExternalSessionID = "other-session"
	cross.Observations[0].ContextMessageIDs = []string{"m-session-original"}
	_, err := e.SubmitFeedback(ctx, cross)
	code(t, err, engine.InvalidInput)
	unknown := feedback("unknown", "Mutex", engine.UserExplanation)
	unknown.Observations[0].Concepts[0].ConceptID = "unregistered"
	_, err = e.SubmitFeedback(ctx, unknown)
	code(t, err, engine.UnknownReference)
	if len(query(t, e, "mutex").Observations) != 1 {
		t.Fatal("invalid bundle created observations")
	}
}
