package journal

import (
	"errors"
	"fmt"
	"maps"
	"slices"
)

// errUnmasked is why a record whose free text still holds a secret is refused: record_hash
// is computed over masked texts, and once sealed a secret cannot be taken out without
// breaking the chain.
var errUnmasked = errors.New("a free text is not masked")

// MaskLifecycle masks the free texts of a lifecycle event: authority_ref and every string
// value in detail, nested arrays and objects included; keys are kept as they are. The record
// passed in is not changed.
func MaskLifecycle(r Record) Record {
	r.AuthorityRef = Mask(r.AuthorityRef)
	if r.Detail != nil {
		d, _ := maskValue(r.Detail).(map[string]any)
		r.Detail = d
	}
	return r
}

func maskValue(v any) any {
	switch x := v.(type) {
	case string:
		return Mask(x)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, item := range x {
			out[k] = maskValue(item)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = maskValue(item)
		}
		return out
	case []string:
		out := make([]string, len(x))
		for i, item := range x {
			out[i] = Mask(item)
		}
		return out
	default:
		return v
	}
}

// PrepareLifecycle is the one way in for a lifecycle event: it masks the event, then checks
// its fields and detail. The result is not sealed yet: the adapter seals it onto the last
// record_hash under the journal's lock.
func PrepareLifecycle(r Record) (Record, error) {
	r = MaskLifecycle(r)
	if err := validateLifecycleFields(r); err != nil {
		return Record{}, err
	}
	return r, nil
}

// PrepareCoach is the one way in for a coach record: it parses the agent's entry strictly,
// sets the server's id, time, author and proposal_fingerprints, masks it and checks it
// against the coach entries already in the journal. The result is not sealed yet.
func PrepareCoach(raw map[string]any, existing []CoachEntry, id, at, userID string, fingerprints map[string]string) (Record, error) {
	e, err := ParseCoachEntry(raw)
	if err != nil {
		return Record{}, err
	}
	e.ID, e.At, e.UserID = id, at, userID
	if len(fingerprints) > 0 {
		e.ProposalFingerprints = maps.Clone(fingerprints)
	}
	e = MaskCoachEntry(e)
	if err := ValidateCoach(e, existing); err != nil {
		return Record{}, err
	}
	return CoachRecord(e), nil
}

// checkMasked refuses a record a free text of which Mask would still change.
func checkMasked(r Record) error {
	switch {
	case r.IsLifecycle():
		if Mask(r.AuthorityRef) != r.AuthorityRef {
			return fmt.Errorf("authority_ref: %w", errUnmasked)
		}
		if r.Detail != nil && !masked(r.Detail) {
			return fmt.Errorf("detail: %w", errUnmasked)
		}
	case r.IsCoach() && r.Coach != nil:
		return checkCoachMasked(*r.Coach)
	}
	return nil
}

func masked(v any) bool {
	switch x := v.(type) {
	case string:
		return Mask(x) == x
	case map[string]any:
		for _, item := range x {
			if !masked(item) {
				return false
			}
		}
	case []any:
		for _, item := range x {
			if !masked(item) {
				return false
			}
		}
	case []string:
		return !slices.ContainsFunc(x, func(s string) bool { return Mask(s) != s })
	}
	return true
}

// checkCoachMasked refuses a coach entry whose free texts are not masked, and one whose
// references would be changed by masking: a reference cannot be masked, it would no longer
// point at anything, so a reference that looks like a secret is refused.
func checkCoachMasked(e CoachEntry) error {
	for _, f := range []struct{ name, value string }{
		{"topic", e.Topic}, {"change", e.Change}, {"rollback", e.Rollback}, {"check", e.Check}, {"target", e.Target},
	} {
		if Mask(f.value) != f.value {
			return fmt.Errorf("%s: %w", f.name, errUnmasked)
		}
	}
	for i, ev := range e.Evidence {
		if Mask(ev.Quote) != ev.Quote {
			return fmt.Errorf("evidence[%d].quote: %w", i, errUnmasked)
		}
	}
	return checkRefsMasked(e)
}

func checkRefsMasked(e CoachEntry) error {
	refs := []struct{ name, value string }{
		{"id", e.ID}, {"client_ref", e.ClientRef}, {"check_of", e.CheckOf}, {"topic_key", e.TopicKey},
	}
	for i, f := range e.Findings {
		refs = append(refs, struct{ name, value string }{fmt.Sprintf("findings[%d]", i), f})
	}
	for i, ev := range e.Evidence {
		refs = append(refs, struct{ name, value string }{fmt.Sprintf("evidence[%d].session", i), ev.Session})
	}
	for _, f := range refs {
		if Mask(f.value) != f.value {
			return fmt.Errorf("%s: looks like a secret and a reference cannot be masked", f.name)
		}
	}
	return nil
}

// checkKindFields refuses a record holding the fields of the other kind: they are not in its
// hash, so a change to them would pass VerifyChain.
func checkKindFields(r Record) error {
	switch {
	case r.IsLifecycle():
		if r.Coach != nil {
			return errors.New("invalid lifecycle journal record: a lifecycle event has no coach fields")
		}
	case r.IsCoach():
		if r.ProposalID != "" || r.ProposalFingerprint != "" || r.AuthorityRef != "" ||
			len(r.ProposalSources) > 0 || r.Detail != nil {
			return errors.New("invalid coach journal record: a coach record has no lifecycle fields")
		}
	}
	return nil
}
