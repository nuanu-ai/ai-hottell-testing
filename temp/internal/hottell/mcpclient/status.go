package mcpclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/mcpconfig"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
)

// Reason says why the client could not talk to the MCP server.
type Reason string

// The reasons hottell status tells apart.
const (
	// ReasonNoServer: no agent config holds a usable hottell entry.
	ReasonNoServer Reason = "no_server"
	// ReasonUnauthorized: the server answered 401 to the MCP key.
	ReasonUnauthorized Reason = "unauthorized"
	// ReasonUnreachable: the request did not reach the server or the connection broke.
	ReasonUnreachable Reason = "unreachable"
	// ReasonFailed: the server answered, but not with what the contract promises.
	ReasonFailed Reason = "failed"
)

// ReasonOf classifies an error of the client; "" for nil.
func ReasonOf(err error) Reason {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrNoServer):
		return ReasonNoServer
	case errors.Is(err, ErrUnauthorized):
		return ReasonUnauthorized
	case errors.Is(err, ErrUnreachable):
		return ReasonUnreachable
	default:
		return ReasonFailed
	}
}

// Status is what the client last saw of the MCP server; the client keeps it in
// state.Paths.MCPFile for hottell status. It never holds the MCP key.
type Status struct {
	// Source is the config the URL and the key were last read from.
	Source mcpconfig.Source `json:"source,omitzero"`
	// URL is the MCP server's address from that config.
	URL string `json:"url,omitempty"`
	// LastSuccess is when the server last answered; zero before the first answer.
	LastSuccess time.Time `json:"last_success,omitzero"`
	// Problem is the last failure, cleared by a success.
	Problem *Problem `json:"problem,omitempty"`
}

// Problem is one failure of the client.
type Problem struct {
	At     time.Time `json:"at"`
	Reason Reason    `json:"reason"`
	// Detail is the error as the client saw it.
	Detail string `json:"detail"`
}

// Message is what hottell status shows for the problem.
func (p *Problem) Message() string {
	switch p.Reason {
	case ReasonNoServer:
		return "MCP-сервер hottell не найден в конфигах Claude Code и Codex: " + p.Detail
	case ReasonUnauthorized:
		return "ключ MCP отклонён сервером (401): выпустите новый на странице «Подключение» и обновите конфиг агента"
	case ReasonUnreachable:
		return "сервис недоступен по сети: " + p.Detail
	default:
		return "ошибка MCP: " + p.Detail
	}
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
		return fmt.Errorf("encode the MCP status: %w", err)
	}
	return state.WriteFileAtomic(path, data)
}
