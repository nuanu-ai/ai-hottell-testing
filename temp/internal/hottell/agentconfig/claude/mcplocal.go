package claude

import (
	"encoding/json"
	"fmt"
)

// The local MCP server hottell-local lives in ~/.claude.json (CLAUDE_CONFIG_DIR/.claude.json)
// at the user scope, so every project sees it. Only its own entry of mcpServers is touched,
// and of that entry hottell owns only type, command and args: env and any other key the
// user put there stay.

// EnsureMCPLocal sets type, command and args of mcpServers.<name> to start binaryPath
// mcp-local over stdio, keeping the entry's other keys; a new entry also gets an empty env.
// An entry that already holds that leaves the file unwritten.
func EnsureMCPLocal(claudeJSON, name, binaryPath string) error {
	owned := []member{}
	for _, f := range []struct {
		key   string
		value any
	}{{"type", "stdio"}, {"command", binaryPath}, {"args", []string{"mcp-local"}}} {
		value, err := marshal(f.value)
		if err != nil {
			return err
		}
		owned = append(owned, member{key: f.key, value: value})
	}
	return editSection(claudeJSON, "mcpServers", func(servers *object) (bool, error) {
		entry := &object{}
		raw, found := servers.get(name)
		if found {
			if !isKind(raw, '{') {
				return false, fmt.Errorf("%w: mcpServers.%s is not an object", ErrMalformed, name)
			}
			var err error
			if entry, err = parseObject(raw); err != nil {
				return false, err
			}
		}
		changed := !found
		for _, m := range owned {
			if cur, ok := entry.get(m.key); ok && sameJSON(cur, m.value) {
				continue
			}
			entry.set(m.key, m.value)
			changed = true
		}
		if !changed {
			return false, nil
		}
		if !found {
			entry.set("env", json.RawMessage("{}"))
		}
		encoded, err := entry.encode()
		if err != nil {
			return false, err
		}
		servers.set(name, encoded)
		return true, nil
	})
}

// RemoveMCPLocal takes mcpServers.<name> out; the other servers stay.
func RemoveMCPLocal(claudeJSON, name string) error {
	return editSection(claudeJSON, "mcpServers", func(servers *object) (bool, error) {
		if _, ok := servers.get(name); !ok {
			return false, nil
		}
		servers.remove(name)
		return true, nil
	})
}
