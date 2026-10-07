package journal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"unicode/utf8"
)

// ZeroHash is the previous_hash of the first record of the journal: 64 zeros.
const ZeroHash = "0000000000000000000000000000000000000000000000000000000000000000"

// Kinds of a record: the lifecycle events of a proposal and the records of the coach.
const (
	KindDecision      = "decision"
	KindApplication   = "application"
	KindEffect        = "effect"
	KindCoachDecision = "coach_decision"
	KindCoachCheck    = "coach_check"
)

// ProposalSource is one source of a proposal frozen into a lifecycle event.
type ProposalSource struct {
	SessionID    string
	TaskID       string
	SourceSHA256 string
	Evidence     []string
}

// Record is one record of the decision journal (docs/specs/deep-review, «Журнал решений →
// Запись»). RecordID, RecordedAt and UserID are set by the server (the colleague's event_id
// and actor, the coach's id and at). ProposalID, ProposalFingerprint, AuthorityRef,
// ProposalSources and Detail belong to the lifecycle events (decision, application, effect);
// Coach holds the fields of a coach record (coach_decision, coach_check). RecordedAt stays
// the text it was written as, so the hash is recomputed byte for byte.
type Record struct {
	SchemaVersion       int
	Kind                string
	RecordID            string
	RecordedAt          string
	UserID              string
	ProposalID          string
	ProposalFingerprint string
	AuthorityRef        string
	ProposalSources     []ProposalSource
	Detail              map[string]any
	Coach               *CoachEntry
	PreviousHash        string
	RecordHash          string
}

// IsLifecycle reports whether the record is a lifecycle event of a proposal.
func (r Record) IsLifecycle() bool { return slices.Contains(LifecycleKinds(), r.Kind) }

// IsCoach reports whether the record is a coach record.
func (r Record) IsCoach() bool { return r.Kind == KindCoachDecision || r.Kind == KindCoachCheck }

// commonKeys are the keys every record has besides record_hash, which is not in the
// canonical text.
func commonKeys() []string {
	return []string{"schema_version", "kind", "record_id", "recorded_at", "user_id", "previous_hash"}
}

func lifecycleKeys() []string {
	return append(commonKeys(), "proposal_id", "proposal_fingerprint", "authority_ref", "proposal_sources", "detail")
}

// value is the record without record_hash as a flat JSON object for Canonical: the exact key
// set of its kind, every key present, a missing value null. So a record has one canonical text.
func (r Record) value() (map[string]any, error) {
	if err := checkKindFields(r); err != nil {
		return nil, err
	}
	v := map[string]any{
		"schema_version": r.SchemaVersion,
		"kind":           r.Kind,
		"record_id":      r.RecordID,
		"recorded_at":    r.RecordedAt,
		"user_id":        r.UserID,
		"previous_hash":  r.PreviousHash,
	}
	switch {
	case r.IsLifecycle():
		sources := make([]any, 0, len(r.ProposalSources))
		for _, s := range r.ProposalSources {
			sources = append(sources, map[string]any{
				"session_id": s.SessionID, "task_id": s.TaskID, "source_sha256": s.SourceSHA256, "evidence": orEmpty(s.Evidence),
			})
		}
		v["proposal_id"] = r.ProposalID
		v["proposal_fingerprint"] = r.ProposalFingerprint
		v["authority_ref"] = r.AuthorityRef
		v["proposal_sources"] = sources
		v["detail"] = nil
		if r.Detail != nil {
			v["detail"] = r.Detail
		}
	case r.IsCoach():
		if r.Coach == nil {
			return nil, errors.New("invalid coach journal record")
		}
		for k, x := range r.Coach.fields(r.Kind) {
			v[k] = x
		}
	default:
		return nil, errors.New("invalid lifecycle event or broken hash chain")
	}
	return v, nil
}

// CanonicalText is the canonical text of the record without record_hash: what the journal
// stores and hashes.
func (r Record) CanonicalText() ([]byte, error) {
	v, err := r.value()
	if err != nil {
		return nil, err
	}
	return Canonical(v)
}

// RecordHash is sha256(Canonical(the record without record_hash)), in hex.
func RecordHash(r Record) (string, error) {
	text, err := r.CanonicalText()
	if err != nil {
		return "", fmt.Errorf("record %s: %w", r.RecordID, err)
	}
	return SHA256Hex(text), nil
}

// Seal returns the record chained onto previousHash: previous_hash set, record_hash computed.
// A record a free text of which is not masked is refused: PrepareLifecycle and PrepareCoach
// mask it.
func (r Record) Seal(previousHash string) (Record, error) {
	if err := checkMasked(r); err != nil {
		return Record{}, fmt.Errorf("record %s: %w", r.RecordID, err)
	}
	r.PreviousHash = previousHash
	h, err := RecordHash(r)
	if err != nil {
		return Record{}, err
	}
	r.RecordHash = h
	return r, nil
}

// ParseRecord reads a stored record: its canonical text and its record_hash. The text must be
// a JSON object with the exact key set of its kind, in canonical form; the order of checks
// and the reasons are the spec's: not an object or not the key set of an event — invalid
// lifecycle journal record; a coach kind without the coach key set — invalid coach journal
// record; any other kind — invalid lifecycle event or broken hash chain.
func ParseRecord(text []byte, recordHash string) (Record, error) {
	// The decoder would turn invalid UTF-8 and a lone surrogate into U+FFFD without a word; the
	// raw bytes are checked first, so such a text is refused for what it is (HT-396).
	if !utf8.Valid(text) {
		return Record{}, errors.New("invalid lifecycle journal record: text is not UTF-8")
	}
	if loneSurrogate(text) {
		return Record{}, errors.New("invalid lifecycle journal record: escaped surrogate")
	}
	dec := json.NewDecoder(bytes.NewReader(text))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		return Record{}, errors.New("invalid lifecycle journal record")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Record{}, errors.New("invalid lifecycle journal record")
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return Record{}, errors.New("invalid lifecycle journal record")
	}
	kind, _ := m["kind"].(string)
	r := Record{Kind: kind, RecordHash: recordHash}
	shapeErr := errors.New("invalid lifecycle journal record")
	if r.IsCoach() {
		shapeErr = errors.New("invalid coach journal record")
		keys := append(commonKeys(), coachKeys(kind)...)
		if _, ok := m["client_ref"]; ok {
			keys = append(keys, "client_ref") // kept only when the client set one
		}
		if !hasKeys(m, keys...) {
			return Record{}, shapeErr
		}
		e, err := parseCoachFields(m, keys)
		if err != nil {
			return Record{}, fmt.Errorf("%w: %w", shapeErr, err)
		}
		r.Coach = &e
	} else {
		if !hasKeys(m, lifecycleKeys()...) {
			return Record{}, shapeErr
		}
		if !r.IsLifecycle() {
			return Record{}, errors.New("invalid lifecycle event or broken hash chain")
		}
		if err := parseLifecycleFields(m, &r); err != nil {
			return Record{}, err
		}
	}
	if n, ok := intValue(m["schema_version"]); !ok || n != 1 {
		return Record{}, shapeErr
	}
	r.SchemaVersion = 1
	for _, f := range []struct {
		key string
		dst *string
	}{{"record_id", &r.RecordID}, {"recorded_at", &r.RecordedAt}, {"user_id", &r.UserID}} {
		s, ok := m[f.key].(string)
		if !ok {
			return Record{}, fmt.Errorf("%s: expected nonempty text up to 1000 characters", f.key)
		}
		*f.dst = s
	}
	if r.PreviousHash, ok = m["previous_hash"].(string); !ok {
		return Record{}, errors.New("invalid lifecycle event or broken hash chain")
	}
	again, err := r.CanonicalText()
	if err != nil || !bytes.Equal(again, text) {
		return Record{}, shapeErr // not canonical: a null written as "" or a value of the wrong type
	}
	return r, nil
}

// parseLifecycleFields reads the four proposal fields and the detail of an event.
func parseLifecycleFields(m map[string]any, r *Record) error {
	var ok bool
	if r.ProposalID, ok = m["proposal_id"].(string); !ok {
		return errors.New("proposal_id: expected nonempty text up to 1000 characters")
	}
	if r.AuthorityRef, ok = m["authority_ref"].(string); !ok {
		return errors.New("authority_ref: expected nonempty text up to 1000 characters")
	}
	if r.ProposalFingerprint, ok = m["proposal_fingerprint"].(string); !ok {
		return errors.New("invalid proposal fingerprint")
	}
	sources, ok := m["proposal_sources"].([]any)
	if !ok || len(sources) == 0 {
		return errors.New("lifecycle event needs frozen proposal sources")
	}
	for _, s := range sources {
		sm, ok := s.(map[string]any)
		if !ok || !hasKeys(sm, "session_id", "task_id", "source_sha256", "evidence") {
			return errors.New("invalid lifecycle proposal source")
		}
		var src ProposalSource
		if src.SessionID, ok = sm["session_id"].(string); !ok {
			return errors.New("proposal_sources.session_id: expected nonempty text up to 1000 characters")
		}
		if src.TaskID, ok = sm["task_id"].(string); !ok {
			return errors.New("proposal_sources.task_id: expected nonempty text up to 1000 characters")
		}
		if src.SourceSHA256, ok = sm["source_sha256"].(string); !ok {
			return errors.New("invalid lifecycle source version")
		}
		ev, err := texts(sm["evidence"], "proposal_sources.evidence")
		if err != nil {
			return err
		}
		src.Evidence = ev
		r.ProposalSources = append(r.ProposalSources, src)
	}
	if r.Detail, ok = m["detail"].(map[string]any); !ok {
		return errors.New("detail: expected object")
	}
	return nil
}

// ChainError is why VerifyChain refused a journal: Index is the 0-based position of the
// first record that does not hold.
type ChainError struct {
	Index int
	Err   error
}

func (e *ChainError) Error() string { return fmt.Sprintf("record %d: %v", e.Index+1, e.Err) }

func (e *ChainError) Unwrap() error { return e.Err }

// loneSurrogate reports whether the text escapes a UTF-16 surrogate, \ud800 to \udfff:
// canonical text writes every character as is, so a surrogate escape is never Python's, and
// a lone one has no character at all.
func loneSurrogate(text []byte) bool {
	for i := 0; i < len(text); i++ {
		if text[i] != '\\' || i+1 >= len(text) {
			continue
		}
		if text[i+1] == 'u' && i+5 < len(text) {
			if code, err := strconv.ParseUint(string(text[i+2:i+6]), 16, 16); err == nil && code >= 0xd800 && code <= 0xdfff {
				return true
			}
		}
		i++ // the escaped character is not the start of another escape
	}
	return false
}

// Head is the head of the journal: how many records it holds and the record_hash of the last,
// ZeroHash for none. The chain alone cannot tell its last records deleted, or a history
// changed and sealed again: the hash has no key, and whoever writes the table can seal it
// anew. A head kept outside the table — the server's log line at every append — can (HT-396).
type Head struct {
	Records    int
	RecordHash string
}

// ErrHeadMismatch is VerifyChain's refusal of a journal whose links hold but that does not end
// at the head recorded outside the table: cut short, or rewritten and sealed again.
var ErrHeadMismatch = errors.New("journal head mismatch")

// HeadOf is the head of recs, a journal in order.
func HeadOf(recs []Record) Head {
	if len(recs) == 0 {
		return Head{RecordHash: ZeroHash}
	}
	return Head{Records: len(recs), RecordHash: recs[len(recs)-1].RecordHash}
}

// VerifyChain checks the journal in order, as _read_locked does: each record is checked again
// whole — the fields and detail of an event (ValidateRecord), the fields of a coach record
// (ValidateCoach against the coach records above it) — then its previous_hash is the
// record_hash of the record before (ZeroHash for the first), its record_hash is the hash of
// its record, and no record_id repeats. Then the journal must end at want, the head recorded
// outside the table: a journal cut short or rewritten and sealed again ends elsewhere.
// Transitions are Project's to check, not this. A broken link is a *ChainError naming the
// first record that does not hold.
func VerifyChain(recs []Record, want Head) error {
	if err := verifyLinks(recs); err != nil {
		return err
	}
	if got := HeadOf(recs); got != want {
		return fmt.Errorf("%w: %d records ending at %s, the head recorded is %d ending at %s",
			ErrHeadMismatch, got.Records, got.RecordHash, want.Records, want.RecordHash)
	}
	return nil
}

// verifyLinks is VerifyChain without the head.
func verifyLinks(recs []Record) error {
	prev := ZeroHash
	seen := make(map[string]bool, len(recs))
	var coach []CoachEntry
	for i, r := range recs {
		if err := verifyOne(r, prev, coach); err != nil {
			return &ChainError{Index: i, Err: err}
		}
		if r.IsCoach() {
			e, _ := CoachEntryFromRecord(r)
			coach = append(coach, e)
		}
		if seen[r.RecordID] {
			return &ChainError{Index: i, Err: errors.New("duplicate lifecycle event id")}
		}
		seen[r.RecordID] = true
		prev = r.RecordHash
	}
	return nil
}

func verifyOne(r Record, prev string, coach []CoachEntry) error {
	if !r.IsCoach() {
		return ValidateRecord(r, prev)
	}
	e, err := CoachEntryFromRecord(r)
	if err != nil {
		return err
	}
	if err := ValidateCoach(e, withoutID(coach, e.ID)); err != nil {
		return err
	}
	if r.PreviousHash != prev {
		return errors.New("invalid lifecycle event or broken hash chain")
	}
	h, err := RecordHash(r)
	if err != nil {
		return err
	}
	if r.RecordHash != h {
		return errors.New("lifecycle journal hash mismatch")
	}
	return nil
}

func withoutID(entries []CoachEntry, id string) []CoachEntry {
	out := make([]CoachEntry, 0, len(entries))
	for _, e := range entries {
		if e.ID != id {
			out = append(out, e)
		}
	}
	return out
}

// orEmpty keeps an empty array an array: nil would be written as null.
func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
