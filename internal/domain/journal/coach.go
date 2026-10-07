package journal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// maxQuoteRunes is how long a quote of a coach record may be, after masking.
const maxQuoteRunes = 200

// maxRefLen is how long an id, a finding or a session of a coach record may be.
const maxRefLen = 128

// CoachEvidence is a quote of a session in a coach record.
type CoachEvidence struct {
	Session string `json:"session"`
	At      string `json:"at,omitempty"`
	Seq     *int   `json:"seq,omitempty"`
	Quote   string `json:"quote"`
}

// CoachEntry is a coach record: a decision on a topic (coach_decision), or a check of a
// change (coach_check, CheckOf set) — the fields of the hottell-coach spec and of
// internal/hottell/local/coach/journal.go. ID, At and UserID are the record's record_id,
// recorded_at and user_id, set by the server. The JSON tags read the agent's input; the
// record's canonical form is fields, the spec's exact key set with null for what is missing.
type CoachEntry struct {
	ID     string `json:"-"`
	At     string `json:"-"`
	UserID string `json:"-"`

	// ClientRef is the key of a repeat the client sets: an append with a key the user already
	// recorded writes nothing (mcp.md «Идемпотентность append»). It is kept in the record only
	// when set, so the records without one keep the colleague's key set and hash.
	ClientRef string `json:"client_ref,omitempty"`

	CheckOf  string   `json:"check_of,omitempty"`
	TopicKey string   `json:"topic_key"`
	Agent    string   `json:"agent"`
	Topic    string   `json:"topic,omitempty"`
	Findings []string `json:"findings,omitempty"`
	// ProposalFingerprints is set by the server, not the agent: for each p2: of Findings in the
	// author's registry when the record is written, that proposal's proposal_fingerprint.
	ProposalFingerprints map[string]string `json:"proposal_fingerprints,omitempty"`
	Evidence             []CoachEvidence   `json:"evidence,omitempty"`
	Decision             string            `json:"decision,omitempty"`
	Layer                *string           `json:"layer"`
	Target               string            `json:"target,omitempty"`
	BeforeSHA256         string            `json:"before_sha256,omitempty"`
	AfterSHA256          string            `json:"after_sha256,omitempty"`
	Change               string            `json:"change,omitempty"`
	Rollback             string            `json:"rollback,omitempty"`
	Check                string            `json:"check,omitempty"`
	CheckAfter           string            `json:"check_after,omitempty"`
	Result               *string           `json:"result"`
	Observations         *int              `json:"observations"`
	// Repeats is, for repeated, on how many of the observed tasks the signal came back.
	Repeats *int `json:"repeats,omitempty"`
}

// Kind is coach_check for a check, coach_decision otherwise.
func (e CoachEntry) Kind() string {
	if e.CheckOf != "" {
		return KindCoachCheck
	}
	return KindCoachDecision
}

// coachKeys is the key set a coach record has besides the common keys.
func coachKeys(kind string) []string {
	if kind == KindCoachCheck {
		return []string{"check_of", "topic_key", "agent", "result", "observations", "repeats", "evidence"}
	}
	return []string{
		"topic_key", "agent", "topic", "findings", "proposal_fingerprints", "evidence", "decision", "layer",
		"target", "before_sha256", "after_sha256", "change", "rollback", "check", "check_after",
	}
}

// fields are the entry's keys of the record of kind, each present: a missing value is null,
// an empty array or object stays empty.
func (e CoachEntry) fields(kind string) map[string]any {
	evidence := make([]any, 0, len(e.Evidence))
	for _, ev := range e.Evidence {
		evidence = append(evidence, map[string]any{
			"session": ev.Session, "at": orNull(ev.At), "seq": intOrNull(ev.Seq), "quote": ev.Quote,
		})
	}
	if kind == KindCoachCheck {
		var result any
		if e.Result != nil {
			result = *e.Result
		}
		m := map[string]any{
			"check_of": e.CheckOf, "topic_key": e.TopicKey, "agent": e.Agent, "result": result,
			"observations": intOrNull(e.Observations), "repeats": intOrNull(e.Repeats), "evidence": evidence,
		}
		if e.ClientRef != "" {
			m["client_ref"] = e.ClientRef
		}
		return m
	}
	findings := make([]any, 0, len(e.Findings))
	for _, f := range e.Findings {
		findings = append(findings, f)
	}
	fingerprints := map[string]any{}
	for k, fp := range e.ProposalFingerprints {
		fingerprints[k] = fp
	}
	var layer any
	if e.Layer != nil {
		layer = *e.Layer
	}
	m := map[string]any{
		"topic_key": e.TopicKey, "agent": e.Agent, "topic": e.Topic, "findings": findings,
		"proposal_fingerprints": fingerprints, "evidence": evidence, "decision": e.Decision, "layer": layer,
		"target": orNull(e.Target), "before_sha256": orNull(e.BeforeSHA256), "after_sha256": orNull(e.AfterSHA256),
		"change": orNull(e.Change), "rollback": orNull(e.Rollback), "check": orNull(e.Check),
		"check_after": orNull(e.CheckAfter),
	}
	if e.ClientRef != "" {
		m["client_ref"] = e.ClientRef
	}
	return m
}

// SameCoachContent reports whether two entries hold the same content: every field of the entry
// but the server's (id, at, author, proposal_fingerprints) and client_ref, compared in
// canonical form. Both are masked as they are recorded.
func SameCoachContent(a, b CoachEntry) bool {
	strip := func(e CoachEntry) ([]byte, error) {
		e.ID, e.At, e.UserID, e.ClientRef, e.ProposalFingerprints = "", "", "", "", nil
		return Canonical(e.fields(e.Kind()))
	}
	x, errA := strip(a)
	y, errB := strip(b)
	return errA == nil && errB == nil && a.Kind() == b.Kind() && bytes.Equal(x, y)
}

// ErrClientRefConflict is an append whose client_ref the user already recorded with other
// content.
var ErrClientRefConflict = errors.New("entry.client_ref: already recorded with other content")

func orNull(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func intOrNull(n *int) any {
	if n == nil {
		return nil
	}
	return *n
}

// parseCoachFields reads the coach keys of a stored record back into an entry: nulls are
// dropped, then the rest is decoded strictly.
func parseCoachFields(m map[string]any, keys []string) (CoachEntry, error) {
	raw := map[string]any{}
	for _, k := range keys[len(commonKeys()):] {
		if m[k] == nil {
			continue
		}
		raw[k] = m[k]
	}
	if ev, ok := raw["evidence"].([]any); ok {
		items := make([]any, 0, len(ev))
		for _, item := range ev {
			im, ok := item.(map[string]any)
			if !ok || !hasKeys(im, "session", "at", "seq", "quote") {
				return CoachEntry{}, errors.New("evidence: expected {session, at, seq, quote}")
			}
			kept := map[string]any{}
			for k, v := range im {
				if v != nil {
					kept[k] = v
				}
			}
			items = append(items, kept)
		}
		raw["evidence"] = items
	}
	var e CoachEntry
	b, err := json.Marshal(raw)
	if err != nil {
		return e, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		return e, err
	}
	return e, nil
}

// CoachRecord is the journal record of an entry whose ID, At and UserID the server has set;
// Seal chains it.
func CoachRecord(e CoachEntry) Record {
	return Record{SchemaVersion: 1, Kind: e.Kind(), RecordID: e.ID, RecordedAt: e.At, UserID: e.UserID, Coach: &e}
}

// ParseCoachEntry reads the entry an agent sends, strictly: an unknown field is an error, so
// a typo is not lost; id, at and proposal_fingerprints are the server's and are unknown fields
// here too. An evidence item without at or seq is written with null.
func ParseCoachEntry(raw map[string]any) (CoachEntry, error) {
	var e CoachEntry
	if len(raw) == 0 {
		return e, errors.New("append needs an entry")
	}
	// encoding/json matches keys without regard to case, so each key is compared with the
	// declared ones exactly first: TOPIC is not topic, and of Topic and topic none wins.
	if err := exactKeys(raw); err != nil {
		return e, err
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return e, fmt.Errorf("entry: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		return e, fmt.Errorf("entry: %w", err)
	}
	return e, nil
}

// CoachEntryFromRecord is the entry of a coach record, with the record's id, time and author.
func CoachEntryFromRecord(r Record) (CoachEntry, error) {
	if !r.IsCoach() || r.Coach == nil {
		return CoachEntry{}, fmt.Errorf("record %s: %s is not a coach record", r.RecordID, r.Kind)
	}
	e := *r.Coach
	if e.Kind() != r.Kind {
		return e, fmt.Errorf("record %s: the fields of a %s", r.RecordID, e.Kind())
	}
	e.ID, e.At, e.UserID = r.RecordID, r.RecordedAt, r.UserID
	return e, nil
}

// MaskCoachEntry masks the free texts of an entry — topic, change, rollback, check, target
// and the quotes — and cuts the texts other than quotes to maxFieldRunes. A quote is not cut:
// one longer than maxQuoteRunes after masking is refused by ValidateCoach.
func MaskCoachEntry(e CoachEntry) CoachEntry {
	for _, p := range []*string{&e.Topic, &e.Change, &e.Rollback, &e.Check, &e.Target} {
		*p = cutMasked(Mask(*p), maxFieldRunes)
	}
	evidence := make([]CoachEvidence, len(e.Evidence))
	for i, ev := range e.Evidence {
		ev.Quote = Mask(ev.Quote)
		evidence[i] = ev
	}
	if e.Evidence != nil {
		e.Evidence = evidence
	}
	return e
}

// ValidateCoach checks a masked entry against the journal: existing are the coach entries
// already in it, and a check_of is looked up among those of the entry's own user.
func ValidateCoach(e CoachEntry, existing []CoachEntry) error {
	if err := validateRefs(e); err != nil {
		return err
	}
	if err := checkCoachMasked(e); err != nil {
		return err
	}
	byID := map[string]CoachEntry{}
	for _, x := range existing {
		if x.ID == e.ID {
			return fmt.Errorf("id %q is already in the journal", e.ID)
		}
		if x.UserID == e.UserID {
			byID[x.ID] = x
			if e.ClientRef != "" && x.ClientRef == e.ClientRef {
				return fmt.Errorf("client_ref %q is already in the journal", e.ClientRef)
			}
		}
	}
	if e.At != "" {
		if _, err := time.Parse(time.RFC3339, e.At); err != nil {
			return fmt.Errorf("at must be RFC3339: %w", err)
		}
	}
	if e.Agent != "codex" && e.Agent != "claude" {
		return fmt.Errorf("agent: codex or claude, not %q", e.Agent)
	}
	if !validTopicKey(e.TopicKey) {
		return fmt.Errorf("topic_key: lowercase latin letters, digits and hyphens, 2–64, not %q", e.TopicKey)
	}
	for i, ev := range e.Evidence {
		if strings.TrimSpace(ev.Quote) == "" {
			return fmt.Errorf("evidence[%d] needs a quote", i)
		}
		if n := utf8.RuneCountInString(ev.Quote); n > maxQuoteRunes {
			return fmt.Errorf("evidence[%d].quote: %d characters after masking, at most %d", i, n, maxQuoteRunes)
		}
		if ev.At != "" {
			if _, err := time.Parse(time.RFC3339, ev.At); err != nil {
				return fmt.Errorf("evidence[%d].at must be RFC3339: %w", i, err)
			}
		}
	}
	for _, h := range []string{e.BeforeSHA256, e.AfterSHA256} {
		if h != "" && !sha256Re.MatchString(h) {
			return errors.New("before_sha256 and after_sha256 are 64 lowercase hex digits")
		}
	}
	if err := validateFingerprints(e); err != nil {
		return err
	}
	if e.CheckOf != "" {
		return validateCoachCheck(e, byID)
	}
	return validateCoachDecision(e)
}

// validateFingerprints: each key of proposal_fingerprints is a p2: finding of the decision,
// each value a proposal_fingerprint; a check has none.
func validateFingerprints(e CoachEntry) error {
	for k, fp := range e.ProposalFingerprints {
		if e.CheckOf != "" || !strings.HasPrefix(k, "p2:") || !slices.Contains(e.Findings, k) || !sha256Re.MatchString(fp) {
			return fmt.Errorf("proposal_fingerprints: a p2: finding of the decision and its 64-hex fingerprint, not %q", k)
		}
	}
	return nil
}

// validateRefs checks the closed formats: id, check_of, findings and evidence sessions.
func validateRefs(e CoachEntry) error {
	const format = "latin letters, digits and . _ : -, 1–128 characters"
	if !validRef(e.ID) {
		return fmt.Errorf("id: %s, not %q", format, e.ID)
	}
	if e.ClientRef != "" && !validRef(e.ClientRef) {
		return fmt.Errorf("client_ref: %s, not %q", format, e.ClientRef)
	}
	if e.CheckOf != "" && !validRef(e.CheckOf) {
		return fmt.Errorf("check_of: %s, not %q", format, e.CheckOf)
	}
	for i, f := range e.Findings {
		if !validRef(f) {
			return fmt.Errorf("findings[%d]: a finding id, %s, not %q", i, format, f)
		}
	}
	for i, ev := range e.Evidence {
		if !validRef(ev.Session) {
			return fmt.Errorf("evidence[%d].session: a session id, %s, not %q", i, format, ev.Session)
		}
	}
	return nil
}

func validateCoachDecision(e CoachEntry) error {
	switch e.Decision {
	case "applied", "declined", "not_justified", "test":
	default:
		return fmt.Errorf("decision: applied, declined, not_justified or test, not %q", e.Decision)
	}
	if e.CheckAfter != "" {
		if _, err := time.Parse(time.DateOnly, e.CheckAfter); err != nil {
			return fmt.Errorf("check_after: YYYY-MM-DD, when to check: %w", err)
		}
	}
	if strings.TrimSpace(e.Topic) == "" {
		return errors.New("topic: the topic in one sentence")
	}
	if len(e.Evidence) == 0 {
		return errors.New("evidence: no quotes from the sessions, no topic")
	}
	if e.Result != nil || e.Observations != nil || e.Repeats != nil {
		return errors.New("result, observations and repeats come from a check (check_of), not from a decision")
	}
	if e.Layer != nil {
		switch *e.Layer {
		case "experience", "instructions", "skill", "technical":
		default:
			return fmt.Errorf("layer: experience, instructions, skill or technical, not %q", *e.Layer)
		}
	}
	if e.Decision != "applied" && e.Decision != "test" {
		return nil
	}
	if e.Layer == nil {
		return errors.New("layer is required for applied and test")
	}
	if *e.Layer != "experience" && e.Target == "" {
		return errors.New("target: the file or setting changed")
	}
	if e.Change == "" || e.Rollback == "" || e.Check == "" {
		return errors.New("change, rollback and check are required for applied and test")
	}
	if e.CheckAfter == "" {
		return errors.New("check_after: YYYY-MM-DD, when to check, is required for applied and test")
	}
	return nil
}

func validateCoachCheck(e CoachEntry, byID map[string]CoachEntry) error {
	orig, ok := byID[e.CheckOf]
	if !ok || orig.CheckOf != "" {
		return fmt.Errorf("check_of: no decision %q in the journal", e.CheckOf)
	}
	if orig.Decision != "applied" && orig.Decision != "test" {
		return fmt.Errorf("check_of: only applied and test are checked, %q is %s", e.CheckOf, orig.Decision)
	}
	// The agent of a check is not compared with the decision's: the code checks only topic_key.
	if e.TopicKey != orig.TopicKey {
		return errors.New("a check has the topic_key of its decision")
	}
	if e.Decision != "" || e.Layer != nil {
		return errors.New("a check has no decision and no layer")
	}
	if fields := decisionFields(e); len(fields) > 0 {
		return fmt.Errorf("a check holds only its result, not the decision's fields: %s", strings.Join(fields, ", "))
	}
	if e.Result == nil {
		return errors.New("result: repeated, not_repeated or not_enough_data")
	}
	switch *e.Result {
	case "not_enough_data":
		if e.Observations != nil && *e.Observations != 0 {
			return errors.New("observations: not_enough_data has none, null or 0")
		}
	case "repeated", "not_repeated":
		if e.Observations == nil || *e.Observations < 1 {
			return errors.New("observations: how many comparable tasks after the change were seen, at least 1")
		}
	default:
		return fmt.Errorf("result: repeated, not_repeated or not_enough_data, not %q", *e.Result)
	}
	switch {
	case *e.Result == "repeated" && (e.Repeats == nil || *e.Repeats < 1 || *e.Repeats > *e.Observations):
		return errors.New("repeats: on how many of the observed tasks the signal came back, 1 to observations")
	case *e.Result != "repeated" && e.Repeats != nil:
		return errors.New("repeats is only for repeated")
	}
	if *e.Result == "repeated" && len(e.Evidence) == 0 {
		return errors.New("evidence: repeated needs a quote of the repeat")
	}
	return nil
}

// decisionFields names the fields of a decision a check record holds.
func decisionFields(e CoachEntry) []string {
	var out []string
	for _, f := range []struct {
		name string
		set  bool
	}{
		{"topic", e.Topic != ""},
		{"findings", len(e.Findings) > 0},
		{"target", e.Target != ""},
		{"before_sha256", e.BeforeSHA256 != ""},
		{"after_sha256", e.AfterSHA256 != ""},
		{"change", e.Change != ""},
		{"rollback", e.Rollback != ""},
		{"check", e.Check != ""},
		{"check_after", e.CheckAfter != ""},
	} {
		if f.set {
			out = append(out, f.name)
		}
	}
	return out
}

// validRef is a closed reference — an id, a finding or a session: latin letters, digits and
// . _ : -, 1 to maxRefLen long.
func validRef(s string) bool {
	if s == "" || len(s) > maxRefLen {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == ':', c == '-':
		default:
			return false
		}
	}
	return true
}

// validTopicKey: lowercase latin letters, digits and hyphens, 2–64, not starting with a hyphen.
func validTopicKey(s string) bool {
	if len(s) < 2 || len(s) > 64 || s[0] == '-' {
		return false
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

// entryKeys are the keys an agent's entry may hold, evidenceKeys those of an evidence item.
func entryKeys() []string {
	return []string{
		"client_ref", "check_of", "topic_key", "agent", "topic", "findings", "evidence", "decision", "layer", "target",
		"before_sha256", "after_sha256", "change", "rollback", "check", "check_after", "result", "observations", "repeats",
	}
}

func evidenceKeys() []string { return []string{"session", "at", "seq", "quote"} }

func exactKeys(raw map[string]any) error {
	for k := range raw {
		if !slices.Contains(entryKeys(), k) {
			return fmt.Errorf("entry: json: unknown field %q", k)
		}
	}
	items, _ := raw["evidence"].([]any)
	for i, item := range items {
		im, _ := item.(map[string]any)
		for k := range im {
			if !slices.Contains(evidenceKeys(), k) {
				return fmt.Errorf("entry: evidence[%d]: json: unknown field %q", i, k)
			}
		}
	}
	return nil
}
