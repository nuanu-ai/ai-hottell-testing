package coach

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// maxQuoteRunes is how long a quote in the journal may be, after masking.
const maxQuoteRunes = 200

// maxRefLen is how long an id, a finding or a session of the journal may be.
const maxRefLen = 128

type journalEvidence struct {
	Session string `json:"session"`
	At      string `json:"at,omitempty"`
	Seq     *int   `json:"seq,omitempty"`
	Quote   string `json:"quote"`
}

// journalEntry is one line of the journal: a decision on a topic, or a check of a change
// (CheckOf). The nulls of the spec's record stay nulls: layer, result, observations.
type journalEntry struct {
	ID           string            `json:"id"`
	CheckOf      string            `json:"check_of,omitempty"`
	TopicKey     string            `json:"topic_key"`
	At           string            `json:"at"`
	Agent        string            `json:"agent"`
	Topic        string            `json:"topic,omitempty"`
	Findings     []string          `json:"findings,omitempty"`
	Evidence     []journalEvidence `json:"evidence,omitempty"`
	Decision     string            `json:"decision,omitempty"`
	Layer        *string           `json:"layer"`
	Target       string            `json:"target,omitempty"`
	BeforeSHA256 string            `json:"before_sha256,omitempty"`
	AfterSHA256  string            `json:"after_sha256,omitempty"`
	Change       string            `json:"change,omitempty"`
	Rollback     string            `json:"rollback,omitempty"`
	Check        string            `json:"check,omitempty"`
	CheckAfter   string            `json:"check_after,omitempty"`
	Result       *string           `json:"result"`
	Observations *int              `json:"observations"`
	// Repeats is, for repeated, on how many of the observed tasks the signal came back.
	Repeats *int `json:"repeats,omitempty"`
}

// Journal is the coach's journal: one record per JSON line, appended only.
type Journal struct {
	path string
	mask masker
}

// NewJournal opens the journal at path, <data dir>/coach/journal.jsonl. The file appears
// with the first record (0600), and so does its directory (0700); the data directory above
// must already exist.
func NewJournal(path string) *Journal { return &Journal{path: path, mask: newMasker()} }

// Path is where the journal is.
func (j *Journal) Path() string { return j.path }

// Read returns the decisions in the order written, each with its latest check folded in
// (result, observations, repeats, checked_at, checks); id and topicKey narrow them when set.
func (j *Journal) Read(id, topicKey string) ([]map[string]any, error) {
	all, err := readJournal(j.path)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, rec := range fold(all) {
		if (id != "" && rec["id"] != id) || (topicKey != "" && rec["topic_key"] != topicKey) {
			continue
		}
		out = append(out, rec)
	}
	return roundTrip(out)
}

// Append checks one record, a decision or a check (check_of), masks it and appends it under
// a lock: two agents may write at once. It returns the record as written.
func (j *Journal) Append(raw map[string]any, now time.Time) (map[string]any, error) {
	e, err := decodeEntry(raw)
	if err != nil {
		return nil, err
	}
	if err := os.Mkdir(filepath.Dir(j.path), 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, fmt.Errorf("create the journal's directory in the data directory: %w", err)
	}
	lock, err := os.OpenFile(j.path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the journal's lock: %w", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return nil, fmt.Errorf("lock the journal: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) //nolint:errcheck // closing the file unlocks it too
	existing, err := readJournal(j.path)
	if err != nil {
		return nil, err
	}
	if e.ID == "" {
		e.ID = newJournalID(now)
	}
	if e.At == "" {
		e.At = now.UTC().Format(time.RFC3339)
	}
	for _, p := range []*string{&e.Topic, &e.Change, &e.Rollback, &e.Check, &e.Target} {
		*p = j.mask.text(*p)
	}
	for i := range e.Evidence {
		e.Evidence[i].Quote = j.mask.text(e.Evidence[i].Quote)
	}
	if err := validateEntry(e, existing); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(e); err != nil { // Encode ends the line itself
		return nil, fmt.Errorf("encode the record: %w", err)
	}
	f, err := os.OpenFile(j.path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the journal: %w", err)
	}
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		return nil, fmt.Errorf("chmod the journal: %w", err)
	}
	open, err := endsMidLine(f)
	if err != nil {
		return nil, err
	}
	line := buf.Bytes()
	if open { // a hand edit dropped the last newline: the record must not glue onto that line
		line = append([]byte{'\n'}, line...)
	}
	if _, err := f.Write(line); err != nil {
		return nil, fmt.Errorf("append to the journal: %w", err)
	}
	if err := f.Sync(); err != nil {
		return nil, fmt.Errorf("sync the journal: %w", err)
	}
	recs, err := roundTrip([]map[string]any{entryMap(e)})
	if err != nil {
		return nil, err
	}
	return recs[0], nil
}

// endsMidLine reports whether the journal is not empty and its last byte is not a newline.
func endsMidLine(f *os.File) (bool, error) {
	st, err := f.Stat()
	if err != nil {
		return false, fmt.Errorf("stat the journal: %w", err)
	}
	if st.Size() == 0 {
		return false, nil
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, st.Size()-1); err != nil {
		return false, fmt.Errorf("read the journal's end: %w", err)
	}
	return last[0] != '\n', nil
}

// decodeEntry reads a record of append; an unknown field is an error, so a typo is not lost.
func decodeEntry(raw map[string]any) (journalEntry, error) {
	var e journalEntry
	if len(raw) == 0 {
		return e, errors.New("append needs an entry")
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

// readJournal reads every record; a line that is not a JSON object (null included) is an
// error, not a record to skip: the journal is the coach's memory.
func readJournal(path string) ([]journalEntry, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the journal of the dashboard's data directory
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the journal: %w", err)
	}
	var out []journalEntry
	for i, line := range bytes.Split(b, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if bytes.Equal(line, []byte("null")) {
			return nil, fmt.Errorf("%s: line %d: null is not a record", path, i+1)
		}
		var e journalEntry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("%s: line %d: %w", path, i+1, err)
		}
		out = append(out, e)
	}
	return out, nil
}

// fold returns the decisions in the order written with their latest check folded in; a
// change may be checked again and again (not_enough_data is not final).
func fold(all []journalEntry) []map[string]any {
	out := []map[string]any{}
	idx := map[string]map[string]any{}
	for _, e := range all {
		rec := entryMap(e)
		if e.CheckOf == "" {
			idx[e.ID] = rec
			out = append(out, rec)
			continue
		}
		if o := idx[e.CheckOf]; o != nil {
			n, _ := o["checks"].(int)
			o["result"], o["observations"], o["checked_at"], o["checks"] = rec["result"], rec["observations"], e.At, n+1
			if r, ok := rec["repeats"]; ok { // only repeated has repeats; an older one goes
				o["repeats"] = r
			} else {
				delete(o, "repeats")
			}
		}
	}
	return out
}

func entryMap(e journalEntry) map[string]any {
	b, _ := json.Marshal(e)
	var rec map[string]any
	_ = json.Unmarshal(b, &rec)
	return rec
}

// roundTrip gives the records the JSON types a client sees: numbers are float64.
func roundTrip(recs []map[string]any) ([]map[string]any, error) {
	b, err := json.Marshal(recs)
	if err != nil {
		return nil, fmt.Errorf("encode the records: %w", err)
	}
	var out []map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("decode the records: %w", err)
	}
	return out, nil
}

func newJournalID(now time.Time) string {
	const abc = "0123456789abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = abc[int(b[i])%len(abc)]
	}
	return now.UTC().Format("20060102T150405") + "-" + string(b)
}

func validateEntry(e journalEntry, existing []journalEntry) error {
	if err := validateRefs(e); err != nil {
		return err
	}
	byID := map[string]journalEntry{}
	for _, x := range existing {
		byID[x.ID] = x
	}
	if _, dup := byID[e.ID]; dup {
		return fmt.Errorf("id %q is already in the journal", e.ID)
	}
	if _, err := time.Parse(time.RFC3339, e.At); err != nil {
		return fmt.Errorf("at must be RFC3339: %w", err)
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
		if h != "" && !validSHA256(h) {
			return errors.New("before_sha256 and after_sha256 are 64 lowercase hex digits")
		}
	}
	if e.CheckOf != "" {
		return validateCheck(e, byID)
	}
	return validateDecision(e)
}

// validateRefs checks the closed formats: id, check_of, findings and evidence sessions.
func validateRefs(e journalEntry) error {
	const format = "latin letters, digits and . _ : -, 1–128 characters"
	if !validRef(e.ID) {
		return fmt.Errorf("id: %s, not %q", format, e.ID)
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

func validateDecision(e journalEntry) error {
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

func validateCheck(e journalEntry, byID map[string]journalEntry) error {
	orig, ok := byID[e.CheckOf]
	if !ok || orig.CheckOf != "" {
		return fmt.Errorf("check_of: no decision %q in the journal", e.CheckOf)
	}
	if orig.Decision != "applied" && orig.Decision != "test" {
		return fmt.Errorf("check_of: only applied and test are checked, %q is %s", e.CheckOf, orig.Decision)
	}
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
func decisionFields(e journalEntry) []string {
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

// validSHA256 is 64 lowercase hex digits.
func validSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
