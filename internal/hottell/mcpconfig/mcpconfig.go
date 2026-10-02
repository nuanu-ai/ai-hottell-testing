// Package mcpconfig finds the hottell MCP server in the agents' own configs: the URL and
// the Authorization header the binary connects with. The binary keeps no copy of either
// and reads them again on every connection, as docs/specs/hottell-contract/mcp.md
// («Какую запись берёт бинарь») describes.
package mcpconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// ServerName is the name of the MCP server in both agents' configs.
const ServerName = "hottell"

// Environment variables that move the agents' configs.
const (
	ClaudeConfigDirEnv = "CLAUDE_CONFIG_DIR"
	CodexHomeEnv       = "CODEX_HOME"
)

// Agent names the agent whose config holds the server.
type Agent string

// The agents whose configs are read.
const (
	Claude Agent = "claude"
	Codex  Agent = "codex"
)

// Source is the config an entry was read from.
type Source struct {
	Agent Agent
	Path  string
}

// Why a config holds no usable entry.
var (
	ErrNoServer = errors.New("MCP server hottell is not in the config")
	ErrNoURL    = errors.New("MCP server hottell has no url")
	ErrNoKey    = errors.New("MCP server hottell has a url but no Authorization header")
	ErrNotHTTP  = errors.New("MCP server hottell is not of type http")
	// ErrKeyInEnv: the daemon under launchd does not see the shell's environment, so a
	// Codex key kept in bearer_token_env_var or env_http_headers is not supported.
	ErrKeyInEnv = errors.New("the MCP key hottell is in an environment variable, not in http_headers")
)

// ConfigError says why one config gave no usable entry.
type ConfigError struct {
	Source Source
	Err    error
}

func (e *ConfigError) Error() string {
	return fmt.Sprintf("%s config %s: %v", e.Source.Agent, e.Source.Path, e.Err)
}

func (e *ConfigError) Unwrap() error { return e.Err }

// NotFoundError is returned when no config holds a usable entry; it names the reason for
// every config read.
type NotFoundError struct {
	Configs []*ConfigError
}

func (e *NotFoundError) Error() string {
	reasons := make([]string, len(e.Configs))
	for i, c := range e.Configs {
		reasons[i] = c.Error()
	}
	return "no usable MCP server hottell: " + strings.Join(reasons, "; ")
}

// Unwrap lets errors.Is match the reason of any config.
func (e *NotFoundError) Unwrap() []error {
	errs := make([]error, len(e.Configs))
	for i, c := range e.Configs {
		errs[i] = c
	}
	return errs
}

// Find returns the URL and the Authorization header value («Bearer <key>», as written)
// of the hottell MCP server, and the config they came from. With explicitPath set only
// that file is read: a .toml file as the Codex config, anything else as the Claude Code
// one. Otherwise the Claude Code config is read first and the Codex config second, and
// the first usable entry wins; URL and key always come from one entry.
func Find(explicitPath string) (url, key string, src Source, err error) {
	var sources []Source
	if explicitPath != "" {
		sources = []Source{ExplicitSource(explicitPath)}
	} else {
		sources, err = DefaultSources()
		if err != nil {
			return "", "", Source{}, err
		}
	}
	return FindIn(sources...)
}

// ExplicitSource tells the agent of a config by its extension.
func ExplicitSource(path string) Source {
	if strings.EqualFold(filepath.Ext(path), ".toml") {
		return Source{Agent: Codex, Path: path}
	}
	return Source{Agent: Claude, Path: path}
}

// DefaultSources returns the agents' configs in the order they are read:
// $CLAUDE_CONFIG_DIR/.claude.json or ~/.claude.json, then $CODEX_HOME/config.toml or
// ~/.codex/config.toml.
func DefaultSources() ([]Source, error) {
	claudeDir, codexDir := os.Getenv(ClaudeConfigDirEnv), os.Getenv(CodexHomeEnv)
	if claudeDir == "" || codexDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home directory: %w", err)
		}
		if claudeDir == "" {
			claudeDir = home
		}
		if codexDir == "" {
			codexDir = filepath.Join(home, ".codex")
		}
	}
	return []Source{
		{Agent: Claude, Path: filepath.Join(claudeDir, ".claude.json")},
		{Agent: Codex, Path: filepath.Join(codexDir, "config.toml")},
	}, nil
}

// FindIn reads the given configs in order and returns the first usable entry, or a
// *NotFoundError naming the reason for each config.
func FindIn(sources ...Source) (url, key string, src Source, err error) {
	notFound := &NotFoundError{}
	for _, s := range sources {
		url, key, err := read(s)
		if err == nil {
			return url, key, s, nil
		}
		notFound.Configs = append(notFound.Configs, &ConfigError{Source: s, Err: err})
	}
	return "", "", Source{}, notFound
}

func read(s Source) (url, key string, err error) {
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return "", "", fmt.Errorf("%w: no such file", ErrNoServer)
	}
	if err != nil {
		return "", "", fmt.Errorf("read: %w", err)
	}
	if s.Agent == Codex {
		return parseCodex(data)
	}
	return parseClaude(data)
}

// claudeConfig is the part of ~/.claude.json the binary reads: the user-scope servers.
type claudeConfig struct {
	MCPServers map[string]struct {
		Type    string            `json:"type"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	} `json:"mcpServers"`
}

func parseClaude(data []byte) (url, key string, err error) {
	var cfg claudeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", "", fmt.Errorf("parse JSON: %w", err)
	}
	server, ok := cfg.MCPServers[ServerName]
	if !ok {
		return "", "", ErrNoServer
	}
	if server.Type != "http" {
		return "", "", fmt.Errorf("%w: type %q", ErrNotHTTP, server.Type)
	}
	return entry(server.URL, server.Headers)
}

// codexConfig is the part of config.toml the binary reads.
type codexConfig struct {
	MCPServers map[string]struct {
		URL               string            `toml:"url"`
		HTTPHeaders       map[string]string `toml:"http_headers"`
		BearerTokenEnvVar string            `toml:"bearer_token_env_var"`
		EnvHTTPHeaders    map[string]string `toml:"env_http_headers"`
	} `toml:"mcp_servers"`
}

func parseCodex(data []byte) (url, key string, err error) {
	var cfg codexConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return "", "", fmt.Errorf("parse TOML: %w", err)
	}
	server, ok := cfg.MCPServers[ServerName]
	if !ok {
		return "", "", ErrNoServer
	}
	url, key, err = entry(server.URL, server.HTTPHeaders)
	if errors.Is(err, ErrNoKey) && (server.BearerTokenEnvVar != "" || authorization(server.EnvHTTPHeaders) != "") {
		return "", "", ErrKeyInEnv
	}
	return url, key, err
}

func entry(url string, headers map[string]string) (string, string, error) {
	if url == "" {
		return "", "", ErrNoURL
	}
	key := authorization(headers)
	if key == "" {
		return "", "", ErrNoKey
	}
	return url, key, nil
}

// authorization returns the Authorization header, its name matched in any case.
func authorization(headers map[string]string) string {
	for name, value := range headers {
		if strings.EqualFold(name, "Authorization") && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
