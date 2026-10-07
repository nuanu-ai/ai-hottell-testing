package telemetry

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrSourceIncomplete is answered by a source prefix of N records when the store lacks one of
// the lines 1 to N. Its text starts with the code source_incomplete, as the errors of
// hottell-local's session_source start with theirs.
var ErrSourceIncomplete = errors.New("source_incomplete: the store lacks a line of the prefix")

// TranscriptKey names one transcript file of one session. A session of Claude has a main
// transcript and one file per subagent under the same session id, so the file name is part of
// the key.
type TranscriptKey struct {
	// UserID is the person whose transcript is read. It is required: two people can hold files
	// of the same name in one session id, and their lines repeat the numbers.
	UserID uuid.UUID
	// Agent is "claude" or "codex".
	Agent string
	// SessionID is the agent's session.
	SessionID string
	// File is the last segment of the transcript path without the ".zst" suffix: Codex moves a
	// rollout between directories and compresses old ones, and the name stays.
	File string
}

// TranscriptLine is one line of a transcript file as the binary sent it.
type TranscriptLine struct {
	// Number is the line's position in the file, starting at 1, counted over the decompressed
	// content.
	Number uint64
	// Kind is "main" or "subagent".
	Kind string
	// Body is the line as the agent wrote it, without the line break.
	Body string
	// Time is the stored time of the record. It is the time of the line itself when the line has
	// one and the moment the binary read it otherwise, so it never orders lines: Number does.
	Time time.Time
}

// Lines are the lines of one transcript file ordered by Number, each number once.
type Lines []TranscriptLine

// Contiguous returns n such that the lines numbered 1 to n are all present: the length of the
// prefix of the transcript the store holds without a hole. A set that does not start at line 1
// has no prefix, and the answer is 0.
func (l Lines) Contiguous() int {
	n := 0
	for _, line := range l {
		if line.Number != uint64(n)+1 {
			break
		}
		n++
	}
	return n
}

// SessionLine is a line of one of a session's transcript files, with whose file it is.
type SessionLine struct {
	// UserID is the person the file belongs to.
	UserID uuid.UUID
	// SessionID is the session the file belongs to; set by a read over many sessions.
	SessionID string
	// File is the file name as in TranscriptKey.File.
	File string
	TranscriptLine
}

// SourceRef is what one line of a Claude transcript tells a session's timeline about where its
// events come from: the line's place and the ids it carries, no text.
type SourceRef struct {
	// UserID is the person the file belongs to.
	UserID uuid.UUID
	// File is the file name as in TranscriptKey.File.
	File string
	// Kind is "main" or "subagent".
	Kind string
	// Number is the line's position in the file, starting at 1.
	Number uint64
	// PromptID is the promptId of a user record that is a prompt, not a tool's result; "" on an
	// assistant record.
	PromptID string
	// ToolUseIDs are the ids of the tool_use items of an assistant record, in their order.
	ToolUseIDs []string
}

// TranscriptFile describes one transcript file of a session.
type TranscriptFile struct {
	// UserID is the person the file belongs to.
	UserID uuid.UUID
	// Name is the file name as in TranscriptKey.File.
	Name string
	// Kind is "main" or "subagent".
	Kind string
	// MaxLine is the highest line number stored for the file; it exceeds the count of lines
	// when the store has holes.
	MaxLine uint64
}

// SessionFile is a transcript file with the session it belongs to, as a read over many sessions
// returns it.
type SessionFile struct {
	SessionID string
	TranscriptFile
}
