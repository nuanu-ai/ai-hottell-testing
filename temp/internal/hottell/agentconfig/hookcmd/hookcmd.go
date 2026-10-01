// Package hookcmd tells which program an agent's hook command runs first, the way the
// shell the agent runs it with would see it.
package hookcmd

import (
	"path/filepath"
	"slices"
	"strings"
)

// Hook is a command hook handler of an agent's config: the event it is on and its command.
type Hook struct {
	Event   string
	Command string
}

// homeVar is the one variable the first word may use: the hook runs with the user's HOME.
const homeVar = "HOME"

// Runs reports whether the first word of the shell command, with its quotes removed and a
// leading ~ or $HOME expanded to home, is the absolute path binaryPath. A command that only
// mentions the path elsewhere, or whose first word depends on anything else the shell would
// expand, does not run it as far as Runs can tell. Neither does a word with a .. segment:
// the shell resolves it after any symlink before it, which cleaning the path does not.
func Runs(command, binaryPath, home string) bool {
	word, ok := firstWord(command, home)
	return ok && filepath.IsAbs(word) && !slices.Contains(strings.Split(word, "/"), "..") &&
		filepath.Clean(word) == filepath.Clean(binaryPath)
}

// firstWord returns the first word of command as the shell would pass it to exec. ok is
// false for an empty command, an unterminated quote, an assignment, and a word with an
// expansion other than ~ and $HOME, or with an unquoted $HOME the shell splits.
func firstWord(command, home string) (string, bool) {
	s := strings.TrimLeft(joinLines(command), " \t\n")
	var word strings.Builder
	if rest, ok := strings.CutPrefix(s, "~"); ok && (rest == "" || strings.ContainsRune("/ \t\n;&|<>()", rune(rest[0]))) {
		word.WriteString(home)
		s = rest
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case strings.IndexByte(" \t\n;&|<>()", c) >= 0:
			return word.String(), word.Len() > 0
		case c == '\\':
			i++
			if i == len(s) {
				return "", false
			}
			word.WriteByte(s[i])
		case c == '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return "", false
			}
			word.WriteString(s[i+1 : i+1+end])
			i += end + 1
		case c == '"':
			n, ok := doubleQuoted(s[i+1:], home, &word)
			if !ok {
				return "", false
			}
			i += n
		case c == '$':
			// Unquoted, the expansion is split into words at the blanks of home.
			if strings.ContainsAny(home, " \t\n") {
				return "", false
			}
			n, ok := expandHome(s[i:], home, &word)
			if !ok {
				return "", false
			}
			i += n - 1
		case c == '=' && isName(s[:i]):
			// An assignment before the command: the word is not the command.
			return "", false
		case c == '`', c == '*', c == '?', c == '[', c == '{':
			// Command substitution, a glob or brace expansion: the word is not a plain path.
			return "", false
		default:
			word.WriteByte(c)
		}
	}
	return word.String(), word.Len() > 0
}

// joinLines removes the line continuations of command, a backslash before a newline,
// unquoted and inside double quotes, as the shell does before it splits words; inside
// single quotes they stay as they are.
func joinLines(command string) string {
	var out strings.Builder
	var quote byte
	for i := 0; i < len(command); i++ {
		c := command[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			}
		case c == '\\' && i+1 < len(command):
			i++
			if command[i] == '\n' {
				continue
			}
			out.WriteByte(c)
			c = command[i]
		case c == '\'' && quote == 0:
			quote = c
		case c == '"':
			quote ^= '"'
		}
		out.WriteByte(c)
	}
	return out.String()
}

// doubleQuoted appends the text of a double-quoted string that s continues after its
// opening quote, and returns how many bytes of s it took, the closing quote included.
func doubleQuoted(s, home string, word *strings.Builder) (int, bool) {
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '"':
			return i + 1, true
		case '\\':
			if i+1 < len(s) && strings.IndexByte("$`\"\\", s[i+1]) >= 0 {
				i++
				word.WriteByte(s[i])
				continue
			}
			word.WriteByte(c)
		case '$':
			n, ok := expandHome(s[i:], home, word)
			if !ok {
				return 0, false
			}
			i += n - 1
		case '`':
			return 0, false
		default:
			word.WriteByte(c)
		}
	}
	return 0, false
}

// expandHome appends home for $HOME or ${HOME} at the start of s and returns their length;
// any other expansion is not ok.
func expandHome(s, home string, word *strings.Builder) (int, bool) {
	for _, form := range []string{"${" + homeVar + "}", "$" + homeVar} {
		rest, ok := strings.CutPrefix(s, form)
		if !ok || form[1] != '{' && rest != "" && isNameByte(rest[0]) {
			continue
		}
		word.WriteString(home)
		return len(form), true
	}
	return 0, false
}

// isName reports whether s is a shell variable name, which an assignment starts with.
func isName(s string) bool {
	if s == "" || s[0] >= '0' && s[0] <= '9' {
		return false
	}
	for i := range len(s) {
		if !isNameByte(s[i]) {
			return false
		}
	}
	return true
}

func isNameByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
