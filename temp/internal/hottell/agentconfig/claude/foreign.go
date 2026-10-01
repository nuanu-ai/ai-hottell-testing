package claude

import (
	"encoding/json"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/agentconfig/hookcmd"
)

// ForeignHooks lists the command hooks of the settings file at path that are not hottell's
// own but run the binary at binaryPath first, as the hooks of the hottell prototype do:
// such a hook gets what the binary prints and its exit status, and sends every event a
// second time. A command that mentions the path anywhere but in its first word is not
// listed. The file is only read; a missing one has none.
func ForeignHooks(path, binaryPath, home string) ([]hookcmd.Hook, error) {
	var found []hookcmd.Hook
	err := edit(path, func(hooks *object) (bool, error) {
		found = nil
		for _, event := range hooks.keys() {
			groups, err := eventGroups(hooks, event)
			if err != nil {
				return false, err
			}
			for _, raw := range groups {
				_, handlers, ok, err := groupHandlers(raw)
				if err != nil {
					return false, err
				}
				if !ok {
					continue
				}
				for _, h := range handlers {
					if command, ok := foreignCommand(h, binaryPath, home); ok {
						found = append(found, hookcmd.Hook{Event: event, Command: command})
					}
				}
			}
		}
		return false, nil
	})
	return found, err
}

// RemoveForeignHooks takes the hooks ForeignHooks lists out of the settings file at path
// the way RemoveHooks takes out hottell's, and returns how many it removed. A file without
// them is not written.
func RemoveForeignHooks(path, binaryPath, home string) (int, error) {
	removed := 0
	drop := func(raw json.RawMessage) bool {
		_, ok := foreignCommand(raw, binaryPath, home)
		return ok
	}
	err := edit(path, func(hooks *object) (bool, error) {
		// A repeated edit after a concurrent change counts again from the fresh read.
		removed = 0
		for _, event := range hooks.keys() {
			n, err := removeMatching(hooks, event, drop)
			if err != nil {
				return false, err
			}
			removed += n
		}
		return removed > 0, nil
	})
	if err != nil {
		return 0, err
	}
	return removed, nil
}

// foreignCommand returns the command of a handler that runs the binary at binaryPath first
// and is not hottell's own.
func foreignCommand(raw json.RawMessage, binaryPath, home string) (string, bool) {
	var h struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	if !isKind(raw, '{') || json.Unmarshal(raw, &h) != nil || h.Type != "command" || isOurs(raw) {
		return "", false
	}
	return h.Command, hookcmd.Runs(h.Command, binaryPath, home)
}
