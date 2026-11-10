package engine

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"unicode/utf8"
)

type sessionRecord struct {
	ID                string `json:"id"`
	ProducerID        string `json:"producer_id"`
	ExternalSessionID string `json:"external_session_id"`
}

func validObservation(k ObservationKind) bool {
	return slices.Contains([]ObservationKind{UserExplanation, UserClaimsFamiliarity, UserClaimsUnfamiliarity, UserReportsForgotten, UserReportsRecalled, UserRequestsExplanation, UserConfirmsReference, UserAttempt}, k)
}

func (e *Engine) SubmitFeedback(ctx context.Context, r FeedbackRequest) (WriteResult, error) {
	return e.write(ctx, r.ProducerID, r.RequestID, "submit_feedback", r, func(tx *sql.Tx) ([]string, bool, error) {
		if !validID(r.SessionID) || !validID(r.ExternalSessionID) {
			return nil, false, fail(InvalidInput, "session_id and external_session_id are required")
		}
		session := sessionRecord{ID: r.SessionID, ProducerID: r.ProducerID, ExternalSessionID: r.ExternalSessionID}
		changed, err := put(ctx, tx, "sessions", session.ID, session)
		if err != nil {
			return nil, false, err
		}
		ids := []string{}
		for _, m := range r.Messages {
			if !validID(m.ID) || !validID(m.ExternalMessageID) || !utf8.ValidString(m.Content) || m.ObservedAt.IsZero() || !slices.Contains([]string{"user", "assistant", "system"}, m.Role) {
				return nil, false, fail(InvalidInput, "message requires id, external_message_id, role, valid UTF-8 content and observed_at")
			}
			m.ObservedAt = m.ObservedAt.UTC()
			added, err := put(ctx, tx, "messages", m.ID, m, "session_id", session.ID)
			if err != nil {
				return nil, false, err
			}
			changed = changed || added
			ids = append(ids, m.ID)
		}
		// Save observations first so confirmations may be referenced in either order.
		for _, obs := range r.Observations {
			if !validID(obs.ID) || !validObservation(obs.Kind) {
				return nil, false, fail(InvalidInput, "unsupported observation id or kind")
			}
			message, err := messageInSession(ctx, tx, obs.MessageID, session.ID)
			if err != nil {
				return nil, false, err
			}
			if message.Role != "user" {
				return nil, false, fail(MissingEvidence, "observations require an original user message")
			}
			obs.Quote, err = validateQuote(message.Content, obs.Quote)
			if err != nil {
				return nil, false, err
			}
			if obs.Assistance == "" {
				obs.Assistance = UnknownAssistance
			}
			if !slices.Contains([]Assistance{NoAssistance, Hint, Explanation, UnknownAssistance}, obs.Assistance) {
				return nil, false, fail(InvalidInput, "unknown assistance value")
			}
			for _, id := range obs.ContextMessageIDs {
				if _, err := messageInSession(ctx, tx, id, session.ID); err != nil {
					return nil, false, err
				}
			}
			obs.Concepts = nil
			added, err := put(ctx, tx, "observations", obs.ID, obs)
			if err != nil {
				return nil, false, err
			}
			changed = changed || added
			ids = append(ids, obs.ID)
		}
		for _, obs := range r.Observations {
			for _, binding := range obs.Concepts {
				if !validID(binding.ID) {
					return nil, false, fail(InvalidInput, "concept binding requires id")
				}
				stored, err := get[Observation](ctx, tx, "observations", obs.ID)
				if err != nil {
					return nil, false, err
				}
				if err := validateBinding(ctx, tx, binding, stored.Quote.Text, session.ID); err != nil {
					return nil, false, err
				}
				added, err := put(ctx, tx, "observation_concepts", binding.ID, binding, "observation_id", obs.ID)
				if err != nil {
					return nil, false, err
				}
				changed = changed || added
				ids = append(ids, binding.ID)
			}
		}
		for _, a := range r.Assertions {
			if !validID(a.ID) || !text(a.PredicateText) || !text(a.ObjectText) {
				return nil, false, fail(InvalidInput, "assertion requires id, predicate_text and object_text")
			}
			obs, err := get[Observation](ctx, tx, "observations", a.ObservationID)
			if err != nil {
				return nil, false, err
			}
			if _, err := messageInSession(ctx, tx, obs.MessageID, session.ID); err != nil {
				return nil, false, err
			}
			if _, err := get[Concept](ctx, tx, "concepts", a.SubjectID); err != nil {
				return nil, false, err
			}
			if a.ObjectConceptID != "" {
				if _, err := get[Concept](ctx, tx, "concepts", a.ObjectConceptID); err != nil {
					return nil, false, err
				}
			}
			if a.Binding == "" {
				a.Binding = Candidate
			}
			if a.Binding != Candidate && a.Binding != UserConfirmed {
				return nil, false, fail(InvalidInput, "interpreted assertions must be candidate or user_confirmed")
			}
			if a.Binding == UserConfirmed {
				if err := confirmation(ctx, tx, a.ConfirmationObservationID, session.ID); err != nil {
					return nil, false, err
				}
				if err := confirmedConcept(ctx, tx, a.SubjectID, a.ConfirmationObservationID); err != nil {
					return nil, false, err
				}
				report, err := get[Observation](ctx, tx, "observations", a.ConfirmationObservationID)
				if err != nil {
					return nil, false, err
				}
				if !strings.Contains(report.Quote.Text, a.PredicateText) || !strings.Contains(report.Quote.Text, a.ObjectText) {
					return nil, false, fail(MissingEvidence, "confirmed assertion must be stated in the confirmation quote")
				}
			} else if a.ConfirmationObservationID != "" {
				return nil, false, fail(InvalidInput, "candidate assertion cannot claim confirmation")
			}
			added, err := put(ctx, tx, "personal_assertions", a.ID, a)
			if err != nil {
				return nil, false, err
			}
			changed = changed || added
			ids = append(ids, a.ID)
		}
		return ids, changed, nil
	})
}

func messageInSession(ctx context.Context, tx *sql.Tx, id, session string) (Message, error) {
	m, err := get[Message](ctx, tx, "messages", id)
	if err != nil {
		return m, err
	}
	var owner string
	if err := tx.QueryRowContext(ctx, "SELECT session_id FROM messages WHERE id=?", id).Scan(&owner); err != nil {
		return m, err
	}
	if owner != session {
		return m, fail(InvalidInput, "message does not belong to this session")
	}
	return m, nil
}

func confirmation(ctx context.Context, tx *sql.Tx, id, session string) error {
	if id == "" {
		return fail(MissingEvidence, "user_confirmed requires a confirmation observation")
	}
	o, err := get[Observation](ctx, tx, "observations", id)
	if err != nil {
		return err
	}
	if o.Kind != UserConfirmsReference {
		return fail(MissingEvidence, "confirmation must be a user_confirms_reference observation")
	}
	if _, err := messageInSession(ctx, tx, o.MessageID, session); err != nil {
		return err
	}
	ok, err := active(ctx, tx, ObservationTarget, id)
	if err != nil {
		return err
	}
	if !ok {
		return fail(MissingEvidence, "confirmation was retracted or replaced")
	}
	return nil
}

func validateBinding(ctx context.Context, tx *sql.Tx, b ConceptBinding, quote, session string) error {
	if _, err := get[Concept](ctx, tx, "concepts", b.ConceptID); err != nil {
		return err
	}
	switch b.Binding {
	case Explicit:
		if b.ConfirmationObservationID != "" {
			return fail(InvalidInput, "explicit binding cannot claim confirmation")
		}
		return validateExplicit(ctx, tx, b.ConceptID, quote)
	case UserConfirmed:
		if err := confirmation(ctx, tx, b.ConfirmationObservationID, session); err != nil {
			return err
		}
		return confirmedConcept(ctx, tx, b.ConceptID, b.ConfirmationObservationID)
	case Candidate:
		if b.ConfirmationObservationID != "" {
			return fail(InvalidInput, "candidate binding cannot claim confirmation")
		}
		return nil
	default:
		return fail(InvalidInput, "binding must be explicit, user_confirmed or candidate")
	}
}

func confirmedConcept(ctx context.Context, tx *sql.Tx, conceptID, observationID string) error {
	report, err := get[Observation](ctx, tx, "observations", observationID)
	if err != nil {
		return err
	}
	err = validateExplicit(ctx, tx, conceptID, report.Quote.Text)
	if err == nil || !HasCode(err, AmbiguousBinding) {
		return err
	}
	concept, lookupErr := get[Concept](ctx, tx, "concepts", conceptID)
	if lookupErr != nil {
		return lookupErr
	}
	reference := concept.ID
	if !strings.Contains(reference, ":") {
		reference = concept.Namespace + ":" + reference
	}
	if strings.Contains(report.Quote.Text, reference) {
		return nil
	}
	return fail(AmbiguousBinding, "confirmation must explicitly identify the concept by unambiguous name or namespaced ID")
}
