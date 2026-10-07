package deep

import (
	"git.alva.dev/alva/harness-telemetry/internal/domain/journal"
	"git.alva.dev/alva/harness-telemetry/pkg/deepv2"
)

// Ports are what the Service reaches the outside through.
type Ports struct {
	DeepReports  Reports
	SkillReports SkillReports
	Journal      Journal
	Transcripts  Transcripts
	Proposals    Proposals
	Tx           TxManager
}

// Service accepts the Deep reports of sessions and the skill opportunity reports resting on
// them, and reads them back.
type Service struct {
	deepReports  Reports
	skillReports SkillReports
	journal      Journal
	transcripts  Transcripts
	proposals    Proposals
	tx           TxManager
}

// NewService returns a Service on its ports.
func NewService(p Ports) *Service {
	return &Service{
		deepReports: p.DeepReports, skillReports: p.SkillReports, journal: p.Journal, transcripts: p.Transcripts,
		proposals: p.Proposals, tx: p.Tx,
	}
}

// invalid is the error of an input the validators would not read: the same
// *deepv2.ValidationError they return, so a caller tells every rejected input by one type.
func invalid(path, reason string) error { return &deepv2.ValidationError{Path: path, Reason: reason} }

// maskStrings returns v with every string value masked by journal.Mask, keys untouched
// (deep-review spec, «Маскирование до хэша»): what is validated and hashed is the masked
// value.
func maskStrings(v any) any {
	switch x := v.(type) {
	case string:
		return journal.Mask(x)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, item := range x {
			out[k] = maskStrings(item)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = maskStrings(item)
		}
		return out
	default:
		return v
	}
}

// decodeMasked reads data with deepv2.Decode and masks its strings; a text that is not one
// JSON document is invalid at path.
func decodeMasked(data []byte, path string) (any, error) {
	v, err := deepv2.Decode(data)
	if err != nil {
		return nil, invalid(path, "expected one JSON document")
	}
	return maskStrings(v), nil
}
