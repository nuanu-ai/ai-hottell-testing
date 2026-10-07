package telemetry

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	domain "git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

var errNoFactsUser = errors.New("codex facts: the session names no user")

// codexAgent is the agent whose rollouts carry the facts.
const codexAgent = "codex"

// Service holds the use cases that compute from the stored telemetry what the store does not
// hold as such.
type Service struct {
	transcripts TranscriptReader
}

// NewService returns a Service that reads the transcripts through transcripts.
func NewService(transcripts TranscriptReader) *Service {
	return &Service{transcripts: transcripts}
}

// CodexFacts returns the facts of the Codex session sessionID of the person userID that its
// hooks do not carry: the exit code, status and duration of each tool call and the tokens,
// model and duration of each turn, read from the session's main rollout. A session whose
// rollout the store does not hold, the person having turned the transcripts source off for
// one, gives facts that answer no_transcript. Lines that arrive later change the answer: the
// facts are computed again on each call, never held. userID is required, since two people can
// hold sessions of one id; domain.ErrUnavailable while the store cannot be read.
func (s *Service) CodexFacts(ctx context.Context, userID uuid.UUID, sessionID string) (domain.CodexFacts, error) {
	if userID == uuid.Nil {
		return domain.CodexFacts{}, errNoFactsUser
	}
	files, err := s.transcripts.TranscriptFiles(ctx, userID, codexAgent, sessionID)
	if err != nil {
		return domain.CodexFacts{}, fmt.Errorf("codex facts: %w", err)
	}
	main, ok := mainRollout(files)
	if !ok {
		return domain.CodexFacts{}, nil
	}
	lines, err := s.transcripts.CodexFactLines(ctx, domain.TranscriptKey{
		UserID: userID, Agent: codexAgent, SessionID: sessionID, File: main.Name,
	})
	if err != nil {
		return domain.CodexFacts{}, fmt.Errorf("codex facts: %w", err)
	}
	return domain.NewCodexFacts(lines), nil
}

// mainRollout picks the session's main rollout: of several main files, which the store holds
// only when a rollout's name changed, the one with the most lines, the first by name of equals.
func mainRollout(files []domain.TranscriptFile) (domain.TranscriptFile, bool) {
	var main domain.TranscriptFile
	found := false
	for _, f := range files {
		if f.Kind != "main" {
			continue
		}
		if !found || f.MaxLine > main.MaxLine {
			main, found = f, true
		}
	}
	return main, found
}
