package engine_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"penumbra/engine"
)

func ExampleEngine() {
	ctx := context.Background()
	directory, err := os.MkdirTemp("", "knowledge-example-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(directory)
	core, err := engine.Open(ctx, filepath.Join(directory, "knowledge.db"))
	if err != nil {
		panic(err)
	}
	defer core.Close()

	_, err = core.RegisterOntology(ctx, engine.RegisterOntologyRequest{
		ProducerID: "example", RequestID: "ontology-1",
		Concepts: []engine.Concept{
			{ID: "cs:mutex", Namespace: "cs", Kind: engine.Mechanism, Label: "Mutex"},
			{ID: "cs:thread", Namespace: "cs", Kind: engine.ConceptNode, Label: "Thread"},
		},
	})
	if err != nil {
		panic(err)
	}

	_, err = core.SubmitFeedback(ctx, engine.FeedbackRequest{
		ProducerID: "example", RequestID: "feedback-1",
		SessionID: "session-1", ExternalSessionID: "conversation-1",
		Messages: []engine.Message{{
			ID: "message-1", ExternalMessageID: "turn-1", Role: "user",
			Content: "Mutex는 알고 있어요.", ObservedAt: time.Now().UTC(),
		}},
		Observations: []engine.Observation{{
			ID: "observation-1", Kind: engine.UserClaimsFamiliarity, MessageID: "message-1",
			Quote:    engine.Quote{Text: "Mutex는 알고 있어요."},
			Concepts: []engine.ConceptBinding{{ID: "binding-1", ConceptID: "cs:mutex", Binding: engine.Explicit}},
		}},
	})
	if err != nil {
		panic(err)
	}

	result, err := core.GetContext(ctx, engine.ContextRequest{
		ConceptIDs: []string{"cs:mutex", "cs:thread"}, MaxItems: 20, MaxBytes: 20000,
	})
	if err != nil {
		panic(err)
	}
	fmt.Printf("observations=%d\n", len(result.Observations))
	fmt.Printf("kind=%s\n", result.Observations[0].Observation.Kind)
	fmt.Printf("unobserved=%v\n", result.UnobservedConceptIDs)
	// Output:
	// observations=1
	// kind=user_claims_familiarity
	// unobserved=[cs:thread]
}
