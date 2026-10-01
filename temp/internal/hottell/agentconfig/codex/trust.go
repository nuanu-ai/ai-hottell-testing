package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/codextrust"
)

// ErrLayout means config.toml keeps the trust of a hottell hook in a form hottell does not
// edit, such as an inline table; the file is left as it is.
var ErrLayout = errors.New("unsupported layout of hooks.state in config.toml")

// trustedHashKey is the key of the hash in a [hooks.state."<key>"] table.
const trustedHashKey = "trusted_hash"

// EnsureTrust writes the trust Codex needs to run the hottell hooks of hooks.json in
// codexHome: for every handler running the binary at binaryPath, trusted_hash in the
// [hooks.state."<key>"] table of its current position, computed from the handler as it
// is in the file. Entries of other positions are left alone, even ones that trusted a
// hottell hook before it moved: they now name someone else's hook. A file that already
// holds that is not written.
func EnsureTrust(codexHome, binaryPath string) error {
	command, err := Command(binaryPath)
	if err != nil {
		return err
	}
	hooks, err := ourHooks(codexHome)
	if err != nil {
		return err
	}
	set := make(map[string]string)
	for _, h := range hooks {
		if h.command == command {
			set[h.key] = h.hash
		}
	}
	return editTrust(codexHome, func(state map[string]trustEntry) (map[string]string, []string) {
		for key, hash := range set {
			if state[key].hash == hash {
				delete(set, key)
			}
		}
		return set, nil
	})
}

// RemoveTrust removes from config.toml in codexHome the trusted_hash of every hottell hook
// of hooks.json, at its current position, and of any other position of the same event
// that trusts exactly such a hook; a table left empty goes with it. It has to run before
// RemoveHooks, which removes what tells hottell's entries apart.
func RemoveTrust(codexHome string) error {
	hooks, err := ourHooks(codexHome)
	if err != nil {
		return err
	}
	return editTrust(codexHome, func(state map[string]trustEntry) (map[string]string, []string) {
		var remove []string
		for key, entry := range state {
			if !entry.hasHash {
				continue
			}
			for _, h := range hooks {
				if key == h.key || (entry.hash == h.hash && strings.HasPrefix(key, h.prefix)) {
					remove = append(remove, key)
					break
				}
			}
		}
		return nil, remove
	})
}

// ourHook is a hottell handler found in hooks.json with the trust Codex keeps for it.
type ourHook struct {
	command string
	// prefix starts the keys of every position of the hook's event.
	prefix string
	key    string
	hash   string
}

// ourHooks finds the hottell handlers of hooks.json and computes their trust keys and
// hashes from their positions and fields.
func ourHooks(codexHome string) ([]ourHook, error) {
	var found []ourHook
	source := HooksFile(codexHome)
	err := editHooks(codexHome, func(hooks *object) (bool, error) {
		for _, event := range hooks.keys() {
			first, err := codextrust.Key(source, event, 0, 0)
			if err != nil {
				// Codex ignores an event it does not know; so does hottell.
				continue
			}
			prefix := strings.TrimSuffix(first, "0:0")
			groups, err := eventGroups(hooks, event)
			if err != nil {
				return false, err
			}
			for gi, raw := range groups {
				hs, err := ourHandlers(event, source, prefix, gi, raw)
				if err != nil {
					return false, err
				}
				found = append(found, hs...)
			}
		}
		return false, nil
	})
	return found, err
}

// fileGroup and fileHandler are a matcher group and a command handler as Codex reads them.
type fileGroup struct {
	Matcher *string `json:"matcher"`
}

type fileHandler struct {
	Command                string  `json:"command"`
	Timeout                *uint64 `json:"timeout"`
	Async                  bool    `json:"async"`
	StatusMessage          *string `json:"statusMessage"`
	AdditionalContextLimit *uint64 `json:"additionalContextLimit"`
}

func ourHandlers(event, source, prefix string, groupIndex int, raw json.RawMessage) ([]ourHook, error) {
	_, handlers, ok, err := groupHandlers(raw)
	if err != nil || !ok {
		return nil, err
	}
	var found []ourHook
	for hi, h := range handlers {
		if !isOurs(h) {
			continue
		}
		hook, err := handlerTrust(event, source, groupIndex, hi, raw, h)
		if err != nil {
			return nil, err
		}
		hook.prefix = prefix
		found = append(found, hook)
	}
	return found, nil
}

// handlerTrust computes the trust key and hash of the command handler h at handlerIndex
// of the matcher group at groupIndex of event, from its position and fields.
func handlerTrust(event, source string, groupIndex, handlerIndex int, group, h json.RawMessage) (ourHook, error) {
	var g fileGroup
	if err := json.Unmarshal(group, &g); err != nil {
		return ourHook{}, fmt.Errorf("%w: hooks.%s[%d]: %w", ErrMalformed, event, groupIndex, err)
	}
	var fh fileHandler
	if err := json.Unmarshal(h, &fh); err != nil {
		return ourHook{}, fmt.Errorf("%w: hooks.%s[%d].hooks[%d]: %w", ErrMalformed, event, groupIndex, handlerIndex, err)
	}
	key, err := codextrust.Key(source, event, groupIndex, handlerIndex)
	if err != nil {
		return ourHook{}, err
	}
	hash, err := codextrust.Hash(event, g.Matcher, codextrust.Handler{
		Command:                fh.Command,
		TimeoutSec:             fh.Timeout,
		Async:                  fh.Async,
		StatusMessage:          fh.StatusMessage,
		AdditionalContextLimit: fh.AdditionalContextLimit,
	})
	if err != nil {
		return ourHook{}, err
	}
	return ourHook{command: fh.Command, key: key, hash: hash}, nil
}

// trustEntry is what a [hooks.state."<key>"] table holds for hottell.
type trustEntry struct {
	hash    string
	hasHash bool
}

// editTrust reads config.toml in codexHome, asks change which trusted_hash values to set
// and which to remove, applies them to their lines and writes the file back atomically.
// The result is scanned again and must hold exactly the changed trust, or nothing is
// written.
func editTrust(codexHome string, change func(state map[string]trustEntry) (set map[string]string, remove []string)) error {
	path := ConfigFile(codexHome)
	data, err := readConfig(path)
	if err != nil {
		return err
	}
	doc, err := parseTrust(data)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}

	before := doc.entries()
	set, remove := change(before)
	if len(set) == 0 && len(remove) == 0 {
		return nil
	}
	out, err := doc.edit(set, remove)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}

	edited, err := parseTrust(out)
	if err != nil {
		return fmt.Errorf("%s: %w: %w", path, ErrLayout, err)
	}
	want := maps.Clone(before)
	for key, hash := range set {
		want[key] = trustEntry{hash: hash, hasHash: true}
	}
	for _, key := range remove {
		delete(want, key)
	}
	got := edited.entries()
	for key, entry := range got {
		if !entry.hasHash && !slices.Contains(remove, key) {
			continue
		}
		if want[key] != entry {
			return fmt.Errorf("%s: %w: trust of %q", path, ErrLayout, key)
		}
	}
	for key, entry := range want {
		if entry.hasHash && got[key] != entry {
			return fmt.Errorf("%s: %w: trust of %q", path, ErrLayout, key)
		}
	}
	if string(out) == string(data) {
		return nil
	}
	return writeConfig(path, out)
}

// sortedKeys returns the keys of m in order, so that appended tables come out the same
// on every run.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
