// Package engine stores source-backed concepts and direct observations of one
// person's expressed knowledge. It does not judge correctness or infer knowledge.
package engine

import "time"

type ConceptKind string

const (
	Topic       ConceptKind = "topic"
	ConceptNode ConceptKind = "concept"
	Mechanism   ConceptKind = "mechanism"
	Procedure   ConceptKind = "procedure"
	Example     ConceptKind = "example"
)

type ObservationKind string

const (
	UserExplanation         ObservationKind = "user_explanation"
	UserClaimsFamiliarity   ObservationKind = "user_claims_familiarity"
	UserClaimsUnfamiliarity ObservationKind = "user_claims_unfamiliarity"
	UserReportsForgotten    ObservationKind = "user_reports_forgotten"
	UserReportsRecalled     ObservationKind = "user_reports_recalled"
	UserRequestsExplanation ObservationKind = "user_requests_explanation"
	UserConfirmsReference   ObservationKind = "user_confirms_reference"
	UserAttempt             ObservationKind = "user_attempt"
)

type BindingKind string

const (
	Explicit      BindingKind = "explicit"
	UserConfirmed BindingKind = "user_confirmed"
	Candidate     BindingKind = "candidate"
)

type Assistance string

const (
	NoAssistance      Assistance = "none"
	Hint              Assistance = "hint"
	Explanation       Assistance = "explanation"
	UnknownAssistance Assistance = "unknown"
)

type ByteSpan struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type Quote struct {
	Text string    `json:"quote"`
	Span *ByteSpan `json:"span,omitempty"`
}

type Source struct {
	ID        string `json:"id"`
	Namespace string `json:"namespace"`
	Title     string `json:"title"`
}

type SourceVersion struct {
	ID       string `json:"id"`
	SourceID string `json:"source_id"`
	Content  string `json:"content"`
}

type Concept struct {
	ID        string      `json:"id"`
	Namespace string      `json:"namespace"`
	Kind      ConceptKind `json:"kind"`
	Label     string      `json:"label"`
	Aliases   []string    `json:"aliases,omitempty"`
}

type ConceptSource struct {
	ID              string `json:"id"`
	ConceptID       string `json:"concept_id"`
	SourceVersionID string `json:"source_version_id"`
	Quote
}

type RelationType struct {
	ID           string        `json:"id"`
	SubjectKinds []ConceptKind `json:"subject_kinds"`
	ObjectKinds  []ConceptKind `json:"object_kinds"`
	Symmetric    bool          `json:"symmetric"`
	AllowSelf    bool          `json:"allow_self"`
}

type Relation struct {
	ID              string `json:"id"`
	SubjectID       string `json:"subject_id"`
	TypeID          string `json:"type_id"`
	ObjectID        string `json:"object_id"`
	SourceVersionID string `json:"source_version_id"`
	Quote
}

type RegisterOntologyRequest struct {
	ProducerID     string          `json:"producer_id"`
	RequestID      string          `json:"request_id"`
	Sources        []Source        `json:"sources,omitempty"`
	Versions       []SourceVersion `json:"versions,omitempty"`
	Concepts       []Concept       `json:"concepts,omitempty"`
	ConceptSources []ConceptSource `json:"concept_sources,omitempty"`
	RelationTypes  []RelationType  `json:"relation_types,omitempty"`
	Relations      []Relation      `json:"relations,omitempty"`
}

type Message struct {
	ID                string    `json:"id"`
	ExternalMessageID string    `json:"external_message_id"`
	Role              string    `json:"role"`
	Content           string    `json:"content"`
	ObservedAt        time.Time `json:"observed_at"`
}

type ConceptBinding struct {
	ID                        string      `json:"id"`
	ConceptID                 string      `json:"concept_id"`
	Binding                   BindingKind `json:"binding"`
	ConfirmationObservationID string      `json:"confirmation_observation_id,omitempty"`
}

type Observation struct {
	ID        string          `json:"id"`
	Kind      ObservationKind `json:"kind"`
	MessageID string          `json:"message_id"`
	Quote
	Assistance        Assistance       `json:"assistance"`
	ContextMessageIDs []string         `json:"context_message_ids,omitempty"`
	Concepts          []ConceptBinding `json:"concepts,omitempty"`
}

type PersonalAssertion struct {
	ID                        string      `json:"id"`
	ObservationID             string      `json:"observation_id"`
	SubjectID                 string      `json:"subject_id"`
	PredicateText             string      `json:"predicate_text"`
	ObjectText                string      `json:"object_text"`
	ObjectConceptID           string      `json:"object_concept_id,omitempty"`
	Binding                   BindingKind `json:"binding"`
	ConfirmationObservationID string      `json:"confirmation_observation_id,omitempty"`
}

type FeedbackRequest struct {
	ProducerID        string              `json:"producer_id"`
	RequestID         string              `json:"request_id"`
	SessionID         string              `json:"session_id"`
	ExternalSessionID string              `json:"external_session_id"`
	Messages          []Message           `json:"messages,omitempty"`
	Observations      []Observation       `json:"observations,omitempty"`
	Assertions        []PersonalAssertion `json:"assertions,omitempty"`
}

type WriteResult struct {
	Revision int64    `json:"revision"`
	IDs      []string `json:"ids"`
}

type SearchRequest struct {
	Query     string `json:"query"`
	Namespace string `json:"namespace,omitempty"`
	Limit     int    `json:"limit"`
	Cursor    string `json:"cursor,omitempty"`
}

type SearchResult struct {
	Concepts   []Concept `json:"concepts"`
	Revision   int64     `json:"revision"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

type ContextRequest struct {
	ConceptIDs        []string `json:"concept_ids"`
	MaxItems          int      `json:"max_items"`
	MaxBytes          int      `json:"max_bytes"`
	IncludeCandidates bool     `json:"include_candidates,omitempty"`
	Cursor            string   `json:"cursor,omitempty"`
}

type ObservationView struct {
	Observation Observation `json:"observation"`
	Message     Message     `json:"message"`
	SessionID   string      `json:"session_id"`
	ProducerID  string      `json:"producer_id"`
	RecordedAt  time.Time   `json:"recorded_at"`
}

type Signal struct {
	ObservationID string          `json:"observation_id"`
	Kind          ObservationKind `json:"kind"`
	ConceptIDs    []string        `json:"concept_ids"`
	ObservedAt    time.Time       `json:"observed_at"`
}

type OmittedEvidence struct {
	ObservationID string `json:"observation_id"`
	Reason        string `json:"reason"`
}

type ContextResult struct {
	Observations         []ObservationView `json:"observations"`
	CandidateBindings    []ConceptBinding  `json:"candidate_bindings,omitempty"`
	UnobservedConceptIDs []string          `json:"unobserved_concept_ids"`
	UnknownConceptIDs    []string          `json:"unknown_concept_ids"`
	Signals              []Signal          `json:"signals"`
	OmittedEvidence      []OmittedEvidence `json:"omitted_evidence"`
	Revision             int64             `json:"revision"`
	Truncated            bool              `json:"truncated"`
	NextCursor           string            `json:"next_cursor,omitempty"`
}

type EvidenceResult struct {
	Observation     ObservationView     `json:"observation"`
	ContextMessages []Message           `json:"context_messages"`
	Assertions      []PersonalAssertion `json:"assertions"`
	Amendments      []Amendment         `json:"amendments"`
	Active          bool                `json:"active"`
}

type ExploreRequest struct {
	ConceptID     string   `json:"concept_id"`
	RelationTypes []string `json:"relation_types,omitempty"`
	Direction     string   `json:"direction"`
	Depth         int      `json:"depth"`
	Limit         int      `json:"limit"`
}

type ExploreResult struct {
	Concepts  []Concept  `json:"concepts"`
	Relations []Relation `json:"relations"`
	Revision  int64      `json:"revision"`
	Truncated bool       `json:"truncated"`
}

type TargetType string

const (
	ObservationTarget TargetType = "observation"
	BindingTarget     TargetType = "binding"
	AssertionTarget   TargetType = "assertion"
	RelationTarget    TargetType = "relation"
)

type Amendment struct {
	ID                    string     `json:"id"`
	TargetType            TargetType `json:"target_type"`
	TargetID              string     `json:"target_id"`
	Action                string     `json:"action"`
	ReplacementID         string     `json:"replacement_id,omitempty"`
	Reason                string     `json:"reason"`
	Mode                  string     `json:"mode"`
	EvidenceObservationID string     `json:"evidence_observation_id,omitempty"`
}

type AmendRequest struct {
	ProducerID string    `json:"producer_id"`
	RequestID  string    `json:"request_id"`
	Amendment  Amendment `json:"amendment"`
}

type Metadata struct {
	ProfileID       string `json:"profile_id"`
	SchemaVersion   int    `json:"schema_version"`
	OntologyVersion int    `json:"ontology_version"`
	Revision        int64  `json:"revision"`
}
