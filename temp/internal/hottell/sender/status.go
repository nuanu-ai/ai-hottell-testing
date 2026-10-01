package sender

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
)

// Status is what the sender last saw of the service; the daemon keeps it in
// state.Paths.SenderFile for hottell status.
type Status struct {
	// LastSuccess is when the service last took a request; zero before the first.
	LastSuccess time.Time `json:"last_success,omitzero"`
	// LastError is the last request the service did not take, cleared by a success.
	LastError *Failure `json:"last_error,omitempty"`
	// Unauthorized is why sending stopped on 401; "" while sending runs.
	Unauthorized string `json:"unauthorized,omitempty"`
}

// Failure is one request the service did not take.
type Failure struct {
	At time.Time `json:"at"`
	// Code is the HTTP status; 0 when there was no answer.
	Code int `json:"code,omitempty"`
	// Reason is the status with the message of the service, or the network error.
	Reason string `json:"reason"`
}

// ReadStatus returns the status kept in path, or a zero one when none is kept.
func ReadStatus(path string) (Status, error) {
	var st Status
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return Status{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return st, nil
}

func writeStatus(path string, st Status) error {
	data, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("encode the sender status: %w", err)
	}
	return state.WriteFileAtomic(path, data)
}
