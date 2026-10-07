// Package journal holds the use cases of the decision journal: the coach's records and the
// lifecycle events of proposals (record_event of local/v2_lifecycle.py, Journal.Append of
// coach/journal.go), and what is read back from it. A record that touches a proposal rebuilds
// the author's registry in the same transaction, so a failed rebuild takes the record back.
package journal

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
	jr "git.alva.dev/alva/harness-telemetry/internal/domain/journal"
	"git.alva.dev/alva/harness-telemetry/internal/domain/registry"
	"git.alva.dev/alva/harness-telemetry/pkg/deepv2"
)

// Journal is the decision journal (the Postgres adapter of HT-245).
type Journal interface {
	// Append adds the record build returns at the end of the journal: under the journal's
	// lock build gets the ctx of the transaction and the record_hash of the last record; the
	// record is sealed onto it. An error writes nothing. Within a transaction it is part of it.
	Append(ctx context.Context, build func(ctx context.Context, previousHash string) (jr.Record, error)) (jr.Record, error)
	// Records returns every record of the user with userID, in chain order.
	Records(ctx context.Context, userID uuid.UUID) ([]jr.Record, error)
	// All returns the whole journal in chain order.
	All(ctx context.Context) ([]jr.Record, error)
	// Lock takes, within the transaction of ctx, the lock Append takes, to the transaction's
	// end: what is read under it misses no record appended meanwhile.
	Lock(ctx context.Context) error
}

// Registry is the user's registry of proposals (the Deep service).
type Registry interface {
	// Merged returns the user's proposals merged from their published Deep reports, not
	// projected onto the journal.
	Merged(ctx context.Context, userID uuid.UUID) ([]registry.Proposal, error)
	// RebuildRegistry rebuilds the user's registry from their reports and the journal, under
	// the journal's lock; within a transaction it is part of it.
	RebuildRegistry(ctx context.Context, userID uuid.UUID) error
}

// TxManager runs a function within one transaction of the ports.
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Clock tells the time a record is recorded at.
type Clock interface {
	Now() time.Time
}

// Service writes and reads the decision journal.
type Service struct {
	journal  Journal
	registry Registry
	tx       TxManager
	clock    Clock
}

// NewService returns a Service on its ports.
func NewService(j Journal, r Registry, tx TxManager, clock Clock) *Service {
	return &Service{journal: j, registry: r, tx: tx, clock: clock}
}

// invalid is a refused input, the same *deepv2.ValidationError the Deep service returns, so a
// caller tells every refusal by one type.
func invalid(path string, err error) error {
	return &deepv2.ValidationError{Path: path, Reason: err.Error()}
}

func (s *Service) now() string { return s.clock.Now().UTC().Format(time.RFC3339) }

// AppendCoach records the coach entry raw of the user with userID: parsed strictly, given the
// server's id, time and author and, for each p2: finding in the user's registry, that
// proposal's fingerprint; masked and checked against the user's coach records. An entry naming
// a p2: proposal rebuilds the user's registry in the same transaction.
//
// An entry with a client_ref the user already recorded is a repeat whose answer was lost: with
// the same content it writes nothing and returns the record as written, with other content it
// is jr.ErrClientRefConflict. The key is looked up under the journal's lock, so two parallel
// repeats write one record (mcp.md «Идемпотентность append», HT-258).
func (s *Service) AppendCoach(ctx context.Context, userID uuid.UUID, raw map[string]any) (jr.CoachEntry, error) {
	var stored jr.Record
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if ref, _ := raw["client_ref"].(string); ref != "" {
			prev, found, err := s.repeatOf(ctx, userID, ref, raw)
			if err != nil || found {
				stored = prev
				return err
			}
		}
		var touches bool
		var err error
		stored, err = s.journal.Append(ctx, func(ctx context.Context, _ string) (jr.Record, error) {
			recs, err := s.journal.Records(ctx, userID)
			if err != nil {
				return jr.Record{}, fmt.Errorf("read decision journal: %w", err)
			}
			fps, err := s.fingerprints(ctx, userID, raw["findings"])
			if err != nil {
				return jr.Record{}, err
			}
			// A check of a decision on a p2: proposal changes that proposal's recurrence.
			touches = len(fps) > 0 || checksProposal(coachEntries(recs), raw["check_of"])
			r, err := jr.PrepareCoach(raw, coachEntries(recs), uuid.NewString(), s.now(), userID.String(), fps)
			if err != nil {
				return jr.Record{}, invalid("entry", err)
			}
			return r, nil
		})
		if err != nil || !touches {
			return err
		}
		return s.registry.RebuildRegistry(ctx, userID) //nolint:wrapcheck // the rebuild's reason is the answer
	})
	if err != nil {
		return jr.CoachEntry{}, err //nolint:wrapcheck // a refusal is the answer as it is
	}
	return jr.CoachEntryFromRecord(stored) //nolint:wrapcheck // never fails on a sealed coach record
}

// repeatOf finds, under the journal's lock, the user's coach record with client_ref ref: found
// with the same content as raw after masking, it is the answer; with other content,
// jr.ErrClientRefConflict; none, found is false and the append goes on (under the lock still
// held).
func (s *Service) repeatOf(ctx context.Context, userID uuid.UUID, ref string, raw map[string]any) (jr.Record, bool, error) {
	if err := s.journal.Lock(ctx); err != nil {
		return jr.Record{}, false, fmt.Errorf("lock decision journal: %w", err)
	}
	recs, err := s.journal.Records(ctx, userID)
	if err != nil {
		return jr.Record{}, false, fmt.Errorf("read decision journal: %w", err)
	}
	entries := coachEntries(recs)
	for i, prev := range entries {
		if prev.ClientRef != ref {
			continue
		}
		// The repeat is checked as a new entry would be — every field, those of the other kind
		// included — against the user's other records, then compared with the one it repeats:
		// a valid entry of other content under the key is the key's conflict.
		e, err := jr.ParseCoachEntry(raw)
		if err != nil {
			return jr.Record{}, false, invalid("entry", err)
		}
		// It stands beside the whole history — the record it repeats included, without its key,
		// so that a check of that record resolves — under an id of its own; ids, times and the
		// key are not compared.
		e.ID, e.At, e.UserID = "repeat-of-"+prev.ID, prev.At, prev.UserID
		e = jr.MaskCoachEntry(e)
		history := slices.Clone(entries)
		history[i].ClientRef = ""
		if err := jr.ValidateCoach(e, history); err != nil {
			return jr.Record{}, false, invalid("entry", err)
		}
		if !jr.SameCoachContent(prev, e) {
			return jr.Record{}, false, jr.ErrClientRefConflict
		}
		for _, r := range recs {
			if r.RecordID == prev.ID {
				return r, true, nil
			}
		}
	}
	return jr.Record{}, false, nil
}

// fingerprints are the proposal_fingerprints of the p2: findings that are proposals of the
// user's registry.
func (s *Service) fingerprints(ctx context.Context, userID uuid.UUID, findings any) (map[string]string, error) {
	items, _ := findings.([]any)
	var ids []string
	for _, f := range items {
		if id, ok := f.(string); ok && strings.HasPrefix(id, "p2:") {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	merged, err := s.registry.Merged(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("read registry: %w", err)
	}
	out := map[string]string{}
	for _, p := range merged {
		id, _ := p["proposal_id"].(string)
		if !slices.Contains(ids, id) {
			continue
		}
		fp, err := fingerprintOf(p)
		if err != nil {
			return nil, err
		}
		out[id] = fp
	}
	return out, nil
}

func fingerprintOf(p registry.Proposal) (string, error) {
	groupKey, _ := p["group_key"].(string)
	fp, err := jr.ProposalFingerprint(groupKey, p["target"], p["change"])
	if err != nil {
		return "", fmt.Errorf("proposal fingerprint: %w", err)
	}
	return fp, nil
}

// checksProposal tells whether checkOf names a coach decision of entries whose findings include
// a p2: proposal.
func checksProposal(entries []jr.CoachEntry, checkOf any) bool {
	id, _ := checkOf.(string)
	if id == "" {
		return false
	}
	for _, e := range entries {
		if e.ID == id {
			return slices.ContainsFunc(e.Findings, func(f string) bool { return strings.HasPrefix(f, "p2:") })
		}
	}
	return false
}

func coachEntries(recs []jr.Record) []jr.CoachEntry {
	var out []jr.CoachEntry
	for _, r := range recs {
		if e, err := jr.CoachEntryFromRecord(r); err == nil {
			out = append(out, e)
		}
	}
	return out
}

// EventInput is a lifecycle event the owner records on a proposal of their registry.
type EventInput struct {
	ProposalID   string
	AuthorityRef string
	Detail       map[string]any
}

// AppendDecision records the owner's decision status (accepted, rejected or
// revision_requested) on their proposal.
func (s *Service) AppendDecision(ctx context.Context, userID uuid.UUID, proposalID, status, authorityRef string) (jr.Record, error) {
	return s.appendEvent(ctx, userID, jr.KindDecision, EventInput{
		ProposalID: proposalID, AuthorityRef: authorityRef, Detail: map[string]any{"status": status},
	})
}

// AppendApplication records that the owner applied their prepared, accepted proposal.
func (s *Service) AppendApplication(ctx context.Context, userID uuid.UUID, in EventInput) (jr.Record, error) {
	return s.appendEvent(ctx, userID, jr.KindApplication, in)
}

// AppendEffect records the measured effect of the owner's applied proposal.
func (s *Service) AppendEffect(ctx context.Context, userID uuid.UUID, in EventInput) (jr.Record, error) {
	return s.appendEvent(ctx, userID, jr.KindEffect, in)
}

// appendEvent is record_event: the proposal is looked up in the owner's registry — only the
// owner records on it, any other is domain.ErrForbidden, a proposal that does not exist
// included, so another user's proposal looks the same as a missing one; it is projected onto
// the owner's journal, an application needs it prepared_verified before the event, the event
// freezes the proposal's fingerprint and sources, is masked and checked, its transition is
// checked on the projection, and the registry is rebuilt in the same transaction.
func (s *Service) appendEvent(ctx context.Context, userID uuid.UUID, kind string, in EventInput) (jr.Record, error) {
	var stored jr.Record
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		var err error
		stored, err = s.journal.Append(ctx, func(ctx context.Context, _ string) (jr.Record, error) {
			return s.buildEvent(ctx, userID, kind, in)
		})
		if err != nil {
			return err
		}
		return s.registry.RebuildRegistry(ctx, userID) //nolint:wrapcheck // the rebuild's reason is the answer
	})
	if err != nil {
		return jr.Record{}, err //nolint:wrapcheck // a refusal is the answer as it is
	}
	return stored, nil
}

func (s *Service) buildEvent(ctx context.Context, userID uuid.UUID, kind string, in EventInput) (jr.Record, error) {
	// The detail is checked as it will be stored: a long secret masked away fits the limit.
	if err := jr.ValidateDetail(kind, jr.MaskLifecycle(jr.Record{Detail: in.Detail}).Detail); err != nil {
		return jr.Record{}, invalid("detail", err)
	}
	merged, err := s.registry.Merged(ctx, userID)
	if err != nil {
		return jr.Record{}, fmt.Errorf("read registry: %w", err)
	}
	i := slices.IndexFunc(merged, func(p registry.Proposal) bool { return p["proposal_id"] == in.ProposalID })
	if i < 0 {
		return jr.Record{}, domain.ErrForbidden
	}
	p := merged[i]
	recs, err := s.journal.Records(ctx, userID)
	if err != nil {
		return jr.Record{}, fmt.Errorf("read decision journal: %w", err)
	}
	state := stateOf(registry.Project(p, recs))
	if kind == jr.KindApplication && state.Readiness != "prepared_verified" {
		return jr.Record{}, invalid("proposal_id", errors.New("proposal is not prepared for application"))
	}
	fp, err := fingerprintOf(p)
	if err != nil {
		return jr.Record{}, err
	}
	r, err := jr.PrepareLifecycle(jr.Record{
		SchemaVersion: 1, Kind: kind, RecordID: uuid.NewString(), RecordedAt: s.now(), UserID: userID.String(),
		ProposalID: in.ProposalID, ProposalFingerprint: fp, AuthorityRef: in.AuthorityRef,
		ProposalSources: sourcesOf(p), Detail: in.Detail,
	})
	if err != nil {
		return jr.Record{}, invalid(kind, err)
	}
	if err := jr.ApplyEvent(&state, r); err != nil {
		return jr.Record{}, invalid(kind, err)
	}
	return r, nil
}

// stateOf reads the readiness, target version and the three axes of a projected proposal.
func stateOf(p registry.Proposal) jr.ProposalState {
	axis := func(key string) map[string]any { m, _ := p[key].(map[string]any); return m }
	str := func(m map[string]any, key string) string { v, _ := m[key].(string); return v }
	num := func(m map[string]any, key string) int {
		switch v := m[key].(type) {
		case int:
			return v
		case float64:
			return int(v)
		}
		return 0
	}
	exec := axis("execution")
	var evidence []string
	items, _ := exec["evidence"].([]any)
	for _, e := range items {
		if v, ok := e.(string); ok {
			evidence = append(evidence, v)
		}
	}
	eff := axis("effect")
	return jr.ProposalState{
		Readiness:     str(axis("readiness"), "status"),
		TargetVersion: str(axis("target"), "version"),
		Decision:      jr.Decision{Status: str(axis("decision"), "status"), At: str(axis("decision"), "at")},
		Execution: jr.Execution{
			Status: str(exec, "status"), At: str(exec, "at"), Version: str(exec, "version"), Evidence: evidence,
		},
		Effect: jr.Effect{
			Status: str(eff, "status"), Method: str(eff, "method"), Metric: str(eff, "metric"),
			NBefore: num(eff, "n_before"), NAfter: num(eff, "n_after"), Note: str(eff, "note"),
		},
	}
}

// sourcesOf freezes the sources of a proposal into a lifecycle event.
func sourcesOf(p registry.Proposal) []jr.ProposalSource {
	items, _ := p["sources"].([]any)
	out := make([]jr.ProposalSource, 0, len(items))
	for _, item := range items {
		m, _ := item.(map[string]any)
		src := jr.ProposalSource{}
		src.SessionID, _ = m["session_id"].(string)
		src.TaskID, _ = m["task_id"].(string)
		src.SourceSHA256, _ = m["source_sha256"].(string)
		ev, _ := m["evidence"].([]any)
		for _, e := range ev {
			if v, ok := e.(string); ok {
				src.Evidence = append(src.Evidence, v)
			}
		}
		out = append(out, src)
	}
	return out
}

// CoachView is a coach decision with its checks folded into its recurrence.
type CoachView struct {
	Entry      jr.CoachEntry
	Recurrence *jr.Recurrence
}

// ReadCoach returns the coach decisions of the user with userID, folded: each with the
// recurrence of its checks, in journal order. A nonempty id keeps only that decision, a
// nonempty topicKey only the decisions of the topic.
func (s *Service) ReadCoach(ctx context.Context, userID uuid.UUID, id, topicKey string) ([]CoachView, error) {
	recs, err := s.journal.Records(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("read decision journal: %w", err)
	}
	folded := jr.FoldChecks(recs)
	var out []CoachView
	for _, e := range coachEntries(recs) {
		if e.CheckOf != "" || (id != "" && e.ID != id) || (topicKey != "" && e.TopicKey != topicKey) {
			continue
		}
		v := CoachView{Entry: e}
		if rec, ok := folded[e.ID]; ok {
			v.Recurrence = &rec
		}
		out = append(out, v)
	}
	return out, nil
}

// CycleFilter selects the records the cycle counters count: the period [From, To) and, unless
// UserID is zero, one person.
type CycleFilter struct {
	UserID   uuid.UUID
	From, To time.Time
}

// Cycle counts the improvement cycle over the journal (journal.Counters).
func (s *Service) Cycle(ctx context.Context, f CycleFilter) (jr.CycleCounters, error) {
	var (
		recs []jr.Record
		err  error
	)
	if f.UserID == uuid.Nil {
		recs, err = s.journal.All(ctx)
	} else {
		recs, err = s.journal.Records(ctx, f.UserID)
	}
	if err != nil {
		return jr.CycleCounters{}, fmt.Errorf("read decision journal: %w", err)
	}
	return jr.Counters(recs, f.From, f.To), nil
}
