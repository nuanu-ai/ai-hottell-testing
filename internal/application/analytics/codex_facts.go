package analytics

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// codexPeriodFacts are the Codex transcript files and facts read for a whole period: the files by
// session id, every person's in the order of the read, file and person; the facts and source
// lines of each main rollout by person, session id and file. The lines they are read from are
// let go once read (HT-536): a period's rollout bodies held through the build were a third of
// its peak.
type codexPeriodFacts struct {
	files map[string][]telemetry.TranscriptFile
	facts map[nativeKey]map[string]codexFileFacts
}

// codexFileFacts are the facts of one main rollout and the source lines of its timeline events.
type codexFileFacts struct {
	facts telemetry.CodexFacts
	src   SourceLines
}

// readCodexFacts reads, in one read, the transcript files and the fact lines of every Codex
// session of period (HT-457), and keeps of each main rollout its facts and source lines, not its
// lines (HT-536).
func readCodexFacts(ctx context.Context, src Source, period telemetry.Filter) (*codexPeriodFacts, error) {
	files, lines, err := src.CodexFactsInPeriod(ctx, telemetry.Filter{UserID: period.UserID, From: period.From, To: period.To})
	if err != nil {
		return nil, fmt.Errorf("read codex facts: %w", err)
	}
	pf := &codexPeriodFacts{
		files: map[string][]telemetry.TranscriptFile{}, facts: map[nativeKey]map[string]codexFileFacts{},
	}
	for _, f := range files {
		pf.files[f.SessionID] = append(pf.files[f.SessionID], f.TranscriptFile)
	}
	byFile := map[nativeKey]map[string]telemetry.Lines{}
	for _, l := range lines {
		k := nativeKey{l.UserID, l.SessionID}
		if byFile[k] == nil {
			byFile[k] = map[string]telemetry.Lines{}
		}
		byFile[k][l.File] = append(byFile[k][l.File], l.TranscriptLine)
	}
	// Only a main rollout gives a session its facts (mainRollout), so only the main files are read.
	for _, f := range files {
		if f.Kind != SrcKindMain {
			continue
		}
		k := nativeKey{f.UserID, f.SessionID}
		if pf.facts[k] == nil {
			pf.facts[k] = map[string]codexFileFacts{}
		}
		if _, done := pf.facts[k][f.Name]; done {
			continue
		}
		fl := byFile[k][f.Name]
		pf.facts[k][f.Name] = codexFileFacts{facts: telemetry.NewCodexFacts(fl), src: CodexSourceLines(fl)}
	}
	return pf, nil
}

// sessionCodexFacts is the facts of the session key's main rollout and the source lines of its
// timeline events over pf, the facts read for the whole period, or, when pf is nil, over the
// session's own reads (codexFacts). As in those reads, a key without a person picks the main file
// among every person's files.
func sessionCodexFacts(
	ctx context.Context, src Source, key SessionKey, pf *codexPeriodFacts,
) (telemetry.CodexFacts, SourceLines, error) {
	if pf == nil {
		facts, lines, err := codexFacts(ctx, src, key)
		if err != nil {
			return telemetry.CodexFacts{}, SourceLines{}, err
		}
		return facts, CodexSourceLines(lines), nil
	}
	files := pf.files[key.SessionID]
	if key.UserID != uuid.Nil {
		files = slices.DeleteFunc(slices.Clone(files), func(f telemetry.TranscriptFile) bool { return f.UserID != key.UserID })
	}
	main, ok := mainRollout(files)
	if !ok {
		return telemetry.CodexFacts{}, SourceLines{}, nil
	}
	ff := pf.facts[nativeKey{main.UserID, key.SessionID}][main.Name]
	return ff.facts, ff.src, nil
}

// mainRollout is the main file of a Codex session among its files: the one with the highest
// line, the first of them on a tie; false when the session has no main file.
func mainRollout(files []telemetry.TranscriptFile) (telemetry.TranscriptFile, bool) {
	var main *telemetry.TranscriptFile
	for i := range files {
		if files[i].Kind == "main" && (main == nil || files[i].MaxLine > main.MaxLine) {
			main = &files[i]
		}
	}
	if main == nil {
		return telemetry.TranscriptFile{}, false
	}
	return *main, true
}
