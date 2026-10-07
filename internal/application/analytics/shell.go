package analytics

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The shell parsing below is a port of the session builder's Python command parser. The
// input is whatever command text an agent ran, so every function here is total: no input
// panics. The tokenizers and the heredoc index each pass over the text a fixed number of
// times; IsTestCmd splits the text once and each sh -c script it reads, at most
// maxShellDepth deep, once more. The heredoc search is indexed instead of rescanning the
// text per opener, but each opener looks up a prefix of its delimiter once per distinct
// closing-word length, so a text built for it hashes O(n·√n) bytes in the worst case
// rather than O(n).
//
// Character classes follow Python's str regular expressions, which are Unicode-aware:
// pyWord is \w, pySpace is \s, and a word boundary is a change between pyWord and not.

// emptyCmd and scriptCmd are the group names for a command with nothing to group and
// for an inline interpreter script.
const (
	emptyCmd  = "(пусто)"
	scriptCmd = "(скрипт)"
)

// SplitSegments splits a shell command into its simple commands, each a list of tokens.
// Heredoc bodies are removed first. Tokens follow POSIX quoting: single quotes are
// literal, a backslash inside double quotes escapes only a double quote or a backslash,
// a backslash elsewhere escapes any character, and '#' at the start of a word starts a
// comment that runs to the end of the line, the newline still separating. Any run of
// ; & | ( ) and newline, and a lone { or }, separates commands. When quoting is broken
// (an unclosed quote or a trailing backslash) the text is split on ||, &&, ;, | and
// newline and each piece on blanks instead.
func SplitSegments(cmd string) [][]string {
	text := stripHeredocs(cmd)
	tokens, ok := shellTokens(text)
	if !ok {
		tokens = fallbackTokens(text)
	}
	var segs [][]string
	var cur []string
	for _, tok := range tokens {
		if isSeparator(tok) {
			if len(cur) > 0 {
				segs = append(segs, cur)
			}
			cur = nil
			continue
		}
		cur = append(cur, tok)
	}
	if len(cur) > 0 {
		segs = append(segs, cur)
	}
	return segs
}

// StripPrefix drops what runs before the program of a simple command: variable
// assignments (NAME=value, value on one line), the wrappers sudo, time, env, nohup,
// command, exec and caffeinate given by name or path, and timeout with its duration. The
// result shares the backing array of tokens.
func StripPrefix(tokens []string) []string {
	i := 0
	for i < len(tokens) {
		t := tokens[i]
		if isAssignment(t) {
			i++
			continue
		}
		base := pyBasename(t)
		if isWrapper(base) {
			i++
			continue
		}
		if base == "timeout" && i+1 < len(tokens) {
			i += 2
			continue
		}
		break
	}
	return tokens[i:]
}

// Positional returns the arguments of a simple command that are not options, after the
// program itself. -C, -c, --git-dir, --work-tree, -R and --repo take the next token as
// their value, which is skipped too.
func Positional(tokens []string) []string {
	var out []string
	skip := false
	for _, t := range tokens[min(1, len(tokens)):] {
		if skip {
			skip = false
			continue
		}
		if strings.HasPrefix(t, "-") {
			skip = isValueFlag(t)
			continue
		}
		out = append(out, t)
	}
	return out
}

// NormalizeCmd groups a shell command by its first meaningful simple command into one to
// three words: "git status", "uv run pytest", "python3 -m unittest", "xcodebuild test",
// "sed -n", "rg". Navigation and environment commands (cd, export, …) never name the
// group; checks and output (echo, [, sleep, …) name it only when nothing else runs.
func NormalizeCmd(cmd string) string {
	var segs [][]string
	for _, s := range SplitSegments(cmd) {
		if s = StripPrefix(s); len(s) > 0 {
			segs = append(segs, s)
		}
	}
	var pick []string
	for _, s := range segs {
		base := pyBasename(s[0])
		if !isSkipProg(base) && !isWeakProg(base) && !strings.HasPrefix(s[0], "#") {
			pick = s
			break
		}
	}
	if pick == nil {
		for _, s := range segs {
			if !isSkipProg(pyBasename(s[0])) {
				pick = s
				break
			}
		}
	}
	if pick == nil {
		return emptyCmd
	}
	return normalizeSimple(pick)
}

func normalizeSimple(toks []string) string {
	prog := pyBasename(toks[0])
	switch {
	case isPython(prog):
		if i := indexOf(toks, "-m"); i >= 0 {
			if i+1 < len(toks) {
				return prog + " -m " + toks[i+1]
			}
			return prog + " -m"
		}
		if indexOf(toks, "-c") >= 0 || len(toks) == 1 || toks[1] == "-" {
			return prog + " " + scriptCmd
		}
		if pos := Positional(toks); len(pos) > 0 {
			return prog + " " + pyBasename(pos[0])
		}
		return prog
	case prog == "xcodebuild":
		for _, a := range []string{
			"test-without-building", "build-for-testing", "test", "build", "clean", "archive", "analyze",
			"-version", "-list", "-showsdks", "-showBuildSettings", "-showdestinations",
		} {
			if indexOf(toks, a) >= 0 {
				return "xcodebuild " + a
			}
		}
		return "xcodebuild"
	case prog == "xcrun":
		pos := Positional(toks)
		if len(pos) > 1 && pos[0] == "simctl" {
			return "xcrun simctl " + pos[1]
		}
		if len(pos) > 0 {
			return "xcrun " + pos[0]
		}
		return "xcrun"
	case prog == "sed":
		if indexOf(toks, "-n") >= 0 {
			return "sed -n"
		}
		return "sed"
	case hasSubcommand(prog):
		pos := Positional(toks)
		if len(pos) == 0 {
			return prog
		}
		if len(pos) > 1 && runsNamedTarget(prog) && isTargetVerb(pos[0]) {
			return prog + " " + pos[0] + " " + pyBasename(pos[1])
		}
		if strings.HasPrefix(pos[0], "/") || strings.HasPrefix(pos[0], "~") || strings.HasPrefix(pos[0], ".") {
			return prog
		}
		return prog + " " + pos[0]
	}
	return prog
}

// IsTestCmd reports whether a command runs tests: one of its simple commands, after
// StripPrefix, has as its program pytest, py.test, jest, vitest or mocha; python -m pytest
// or unittest; xcodebuild with the test or test-without-building action; go, cargo or
// swift test; node or tsx --test; make test or check; npm, pnpm, yarn or bun test, run
// test or a test:* script. A runner reached through a launcher counts too: npx, bunx, uvx,
// uv/poetry run, pnpm/npm exec, pnpm/yarn dlx, pnpm/yarn/bun followed by the runner itself,
// and sh, bash or zsh -c with the script nested at most maxShellDepth deep.
//
// This departs from the colleague's is_test_cmd on purpose. It searches the raw text for
// runner names at any word boundary, so echo 'pytest', cat pytest.ini, a comment or a
// heredoc body counted as a test run, and the test-dependent rules (thrash) took that as
// proof. Here only the program position counts, read from the parsed segments, so a
// runner name in an argument, a comment or a heredoc body does not; and a Unicode blank
// inside a word does not split it, as the shell does not split it either.
func IsTestCmd(cmd string) bool { return isTestCmd(cmd, 0) }

// maxShellDepth bounds how many sh -c scripts nested in each other IsTestCmd reads.
const maxShellDepth = 2

func isTestCmd(cmd string, depth int) bool {
	for _, seg := range SplitSegments(cmd) {
		if runsTests(StripPrefix(seg), depth) {
			return true
		}
	}
	return false
}

// runsTests reads one simple command whose program is its first token. A launcher hands
// over to the command it runs, which is read the same way. A launcher step reads only
// the tokens it then drops, and a step that returns reads the rest once, so a chain of
// launchers costs time linear in its tokens.
func runsTests(toks []string, depth int) bool {
	for len(toks) > 0 {
		prog := pyBasename(toks[0])
		switch {
		case isTestRunner(prog):
			return true
		case isPython(prog):
			i := indexOf(toks, "-m")
			return i > 0 && i+1 < len(toks) && (toks[i+1] == "pytest" || toks[i+1] == "unittest")
		case prog == "xcodebuild":
			return indexOf(toks, "test") > 0 || indexOf(toks, "test-without-building") > 0
		case prog == "go" || prog == "cargo" || prog == "swift":
			i := firstPositional(toks)
			return i < len(toks) && toks[i] == "test"
		case prog == "node" || prog == "tsx":
			return indexOf(toks, "--test") > 0
		case prog == "make":
			pos := Positional(toks)
			return indexOf(pos, "test") >= 0 || indexOf(pos, "check") >= 0
		case prog == "sh" || prog == "bash" || prog == "zsh":
			i := shellScriptArg(toks)
			return i > 0 && depth < maxShellDepth && isTestCmd(toks[i], depth+1)
		case prog == "npx" || prog == "bunx" || prog == "uvx":
			toks = afterOptions(toks, 1)
		case prog == "uv" || prog == "poetry":
			i := firstPositional(toks)
			if i == len(toks) || toks[i] != "run" {
				return false
			}
			toks = afterOptions(toks, i+1)
		case prog == "npm" || prog == "pnpm" || prog == "yarn" || prog == "bun":
			i := firstPositional(toks)
			if i == len(toks) {
				return false
			}
			verb := toks[i]
			switch {
			case isTestScript(verb):
				return true
			case verb == "run" || verb == "run-script":
				j := i + firstPositional(toks[i:])
				return j < len(toks) && isTestScript(toks[j])
			case verb == "exec" || verb == "dlx" || verb == "x":
				toks = afterOptions(toks, i+1)
			case prog != "npm" && isTestRunner(pyBasename(verb)):
				return true
			default:
				return false
			}
		default:
			return false
		}
	}
	return false
}

// firstPositional returns the index of the first token after the program that Positional
// would return, or len(toks) when there is none; it reads only the tokens before it.
func firstPositional(toks []string) int {
	for i := 1; i < len(toks); i++ {
		t := toks[i]
		if !strings.HasPrefix(t, "-") {
			return i
		}
		if isValueFlag(t) {
			i++
		}
	}
	return len(toks)
}

// isValueFlag lists the options Positional reads as taking the next token as their value.
func isValueFlag(t string) bool {
	switch t {
	case "-C", "-c", "--git-dir", "--work-tree", "-R", "--repo":
		return true
	}
	return false
}

func isTestRunner(prog string) bool {
	switch prog {
	case "pytest", "py.test", "jest", "vitest", "mocha":
		return true
	}
	return false
}

// isTestScript is a package script named test or test:<variant>.
func isTestScript(name string) bool { return name == "test" || strings.HasPrefix(name, "test:") }

// afterOptions returns the tokens from the first one at or after i that is not an option.
func afterOptions(toks []string, i int) []string {
	for i < len(toks) && strings.HasPrefix(toks[i], "-") {
		i++
	}
	return toks[min(i, len(toks)):]
}

// shellScriptArg returns the index of the script a shell runs with -c, or a short option
// group ending in c such as -lc, or 0 when there is none.
func shellScriptArg(toks []string) int {
	for i := 1; i+1 < len(toks); i++ {
		t := toks[i]
		if !strings.HasPrefix(t, "-") || strings.HasPrefix(t, "--") {
			return 0
		}
		if strings.HasSuffix(t, "c") {
			return i + 1
		}
	}
	return 0
}

// stripHeredocs replaces every heredoc, from << to its closing delimiter, with a blank.
//
// The source is the Python pattern
//
//	<<-?\s*(['"]?)([A-Za-z_][\w]*)\1[^\n]*\n.*?\n\s*\2\b   (DOTALL)
//
// whose backreferences RE2 cannot express, so it is matched by hand with the same
// semantics: after <<, an optional dash and blanks (newlines included), the delimiter
// either quoted, then exactly the word between matching quotes, or bare, then the longest
// prefix of the word that closes; the rest of that line; then the body up to the first
// newline that is followed by blanks and the delimiter as a whole word. The body may be
// empty but the newline before the closing line may not be the header's own, so
// "<<EOF\nEOF" is not a heredoc. Unclosed openers stay in the text.
func stripHeredocs(text string) string {
	if !strings.Contains(text, "<<") {
		return text
	}
	closers := heredocClosers(text)
	var out strings.Builder
	last := 0
	for i := 0; i+1 < len(text); {
		if text[i] != '<' || text[i+1] != '<' {
			i++
			continue
		}
		end, ok := heredocAt(text, i, closers)
		if !ok {
			i++
			continue
		}
		out.WriteString(text[last:i])
		out.WriteByte(' ')
		last, i = end, end
	}
	if last == 0 {
		return text
	}
	out.WriteString(text[last:])
	return out.String()
}

// heredocIndex lists, per closing word, the newlines a closing line with that word follows
// and where the word ends, in text order.
type heredocIndex struct {
	byWord   map[string][]heredocCloser
	lengths  []int // distinct word lengths, longest first
	newlines []int // every newline position, ascending
}

type heredocCloser struct{ newline, end int }

// heredocClosers scans the text once. Newlines are blanks too, so the newlines of one
// blank run all reach the same word: the run's end and that word are found by the first
// of them and reused by the rest, which keeps the index linear in the text however many
// blank lines it holds.
func heredocClosers(text string) heredocIndex {
	idx := heredocIndex{byWord: map[string][]heredocCloser{}}
	seen := map[int]bool{}
	runEnd, wordEnd := -1, -1 // the blank run last scanned ends at runEnd, its word at wordEnd
	for p := 0; p < len(text); p++ {
		if text[p] != '\n' {
			continue
		}
		idx.newlines = append(idx.newlines, p)
		if p+1 > runEnd {
			runEnd = skipPySpace(text, p+1)
			wordEnd = skipPyWord(text, runEnd)
		}
		k, e := runEnd, wordEnd
		if e == k {
			continue
		}
		w := text[k:e]
		idx.byWord[w] = append(idx.byWord[w], heredocCloser{newline: p, end: e})
		if !seen[len(w)] {
			seen[len(w)] = true
			idx.lengths = append(idx.lengths, len(w))
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(idx.lengths)))
	return idx
}

// heredocAt matches a heredoc starting at the << at i and returns where it ends.
func heredocAt(text string, i int, idx heredocIndex) (int, bool) {
	j := i + 2
	if j < len(text) && text[j] == '-' {
		j++
	}
	j = skipPySpace(text, j)
	quote := byte(0)
	if j < len(text) && (text[j] == '\'' || text[j] == '"') {
		quote = text[j]
		j++
	}
	if j >= len(text) || !isNameStart(text[j]) {
		return 0, false
	}
	wordEnd := skipPyWord(text, j)
	afterName := wordEnd
	if quote != 0 {
		if wordEnd >= len(text) || text[wordEnd] != quote {
			return 0, false
		}
		afterName = wordEnd + 1
	}
	// [^\n]*\n: the header line ends at the first newline after the delimiter, whichever
	// prefix of the word is the delimiter.
	nl := sort.SearchInts(idx.newlines, afterName)
	if nl == len(idx.newlines) {
		return 0, false
	}
	bodyStart := idx.newlines[nl] + 1
	for _, l := range idx.lengths {
		if l > wordEnd-j || (quote != 0 && l != wordEnd-j) {
			continue
		}
		cs := idx.byWord[text[j:j+l]]
		k := sort.Search(len(cs), func(n int) bool { return cs[n].newline >= bodyStart })
		if k < len(cs) {
			return cs[k].end, true
		}
	}
	return 0, false
}

// shellTokens is Python's shlex.shlex(text, posix=True, punctuation_chars=";&|()\n") with
// whitespace_split on and whitespace " \t\r", except for comments, where it follows the
// shell instead: '#' starts a comment only at the start of a word, and the newline ending
// a comment is kept as a separator. shlex also reads '#' inside a word as a comment and
// drops the newline after it, which glues the next line's command onto this one. ok is
// false where shlex raises: an unclosed quote or a backslash at the end of the text.
func shellTokens(text string) (tokens []string, ok bool) {
	const (
		stBlank = iota
		stWord
		stPunct
		stSingle
		stDouble
		stEscape
	)
	var tok strings.Builder
	state, escaped := stBlank, stWord
	quoted := false
	emit := func() {
		if tok.Len() > 0 || quoted {
			tokens = append(tokens, tok.String())
		}
		tok.Reset()
		quoted = false
	}
	// skipComment returns the last position of the comment starting at i: the loop's
	// next step reads the newline that ends it, which still separates commands.
	skipComment := func(i int) int {
		if nl := strings.IndexByte(text[i:], '\n'); nl >= 0 {
			return i + nl - 1
		}
		return len(text) - 1
	}
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch state {
		case stBlank:
			switch {
			case isShlexBlank(c):
			case c == '#':
				i = skipComment(i)
			case c == '\\':
				state, escaped = stEscape, stWord
			case isShlexPunct(c):
				tok.WriteByte(c)
				state = stPunct
			case c == '\'':
				state = stSingle
			case c == '"':
				state = stDouble
			default:
				tok.WriteByte(c)
				state = stWord
			}
		case stWord, stPunct:
			switch {
			case isShlexBlank(c):
				state = stBlank
				emit()
			case state == stPunct:
				if isShlexPunct(c) {
					tok.WriteByte(c)
					continue
				}
				i-- // pushed back: read again as the start of the next token
				state = stBlank
				emit()
			case c == '\'':
				state = stSingle
			case c == '"':
				state = stDouble
			case c == '\\':
				state, escaped = stEscape, stWord
			case isShlexPunct(c):
				i--
				state = stBlank
				emit()
			default:
				tok.WriteByte(c)
			}
		case stSingle:
			quoted = true
			if c == '\'' {
				state = stWord
			} else {
				tok.WriteByte(c)
			}
		case stDouble:
			quoted = true
			switch c {
			case '"':
				state = stWord
			case '\\':
				state, escaped = stEscape, stDouble
			default:
				tok.WriteByte(c)
			}
		case stEscape:
			if escaped == stDouble && c != '\\' && c != '"' {
				tok.WriteByte('\\')
			}
			tok.WriteByte(c)
			state = escaped
		}
	}
	switch state {
	case stSingle, stDouble, stEscape:
		return nil, false
	}
	emit()
	return tokens, true
}

// fallbackTokens splits text on ||, &&, ;, | and newline, each piece on blanks, and ends
// every piece with a ";" separator.
func fallbackTokens(text string) []string {
	var tokens []string
	start := 0
	piece := func(end int) {
		tokens = append(tokens, strings.FieldsFunc(text[start:end], isPySpace)...)
		tokens = append(tokens, ";")
	}
	for i := 0; i < len(text); {
		w := 0
		switch {
		case strings.HasPrefix(text[i:], "||"), strings.HasPrefix(text[i:], "&&"):
			w = 2
		case text[i] == ';' || text[i] == '|' || text[i] == '\n':
			w = 1
		}
		if w == 0 {
			i++
			continue
		}
		piece(i)
		i += w
		start = i
	}
	piece(len(text))
	return tokens
}

// isSeparator reports a token made only of ; & | ( ) and newline, or a lone { or }.
func isSeparator(tok string) bool {
	if tok == "{" || tok == "}" {
		return true
	}
	return tok != "" && strings.Trim(tok, ";&|()\n") == ""
}

func isShlexBlank(c byte) bool { return c == ' ' || c == '\t' || c == '\r' }

func isShlexPunct(c byte) bool {
	switch c {
	case ';', '&', '|', '(', ')', '\n':
		return true
	}
	return false
}

// isAssignment is the full match of [A-Za-z_][A-Za-z0-9_]*=.* where . excludes newline.
func isAssignment(t string) bool {
	eq := strings.IndexByte(t, '=')
	if eq < 1 || !isNameStart(t[0]) || strings.ContainsRune(t[eq:], '\n') {
		return false
	}
	for i := 1; i < eq; i++ {
		if c := t[i]; !isNameStart(c) && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func isNameStart(c byte) bool { return c == '_' || (c|0x20 >= 'a' && c|0x20 <= 'z') }

// isPython matches python, python3 and python3.<digits>.
func isPython(prog string) bool {
	if prog == "python" || prog == "python3" {
		return true
	}
	rest, ok := strings.CutPrefix(prog, "python3.")
	if !ok || rest == "" {
		return false
	}
	for _, r := range rest {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// pyBasename is Python's os.path.basename: everything after the last slash.
func pyBasename(p string) string {
	return p[strings.LastIndexByte(p, '/')+1:]
}

func indexOf(toks []string, s string) int {
	for i, t := range toks {
		if t == s {
			return i
		}
	}
	return -1
}

func isWrapper(base string) bool {
	switch base {
	case "sudo", "time", "env", "nohup", "command", "exec", "caffeinate":
		return true
	}
	return false
}

func isSkipProg(base string) bool {
	switch base {
	case "cd", "export", "set", "source", ".", "true", "pushd", "popd", "unset", "ulimit", "trap", "local":
		return true
	}
	return false
}

func isWeakProg(base string) bool {
	switch base {
	case "[", "[[", "test", "echo", "printf", "sleep", ":":
		return true
	}
	return false
}

// hasSubcommand lists programs grouped with their first positional argument.
func hasSubcommand(prog string) bool {
	switch prog {
	case "git", "gh", "npm", "pnpm", "yarn", "uv", "cargo", "go", "docker", "kubectl", "brew", "swift",
		"make", "pip", "pip3", "poetry", "bun", "npx", "launchctl", "defaults", "codex", "tectd", "hottell",
		"open", "xcode-select", "security", "tailscale", "ssh", "node", "deno", "terraform", "helm", "mise":
		return true
	}
	return false
}

// runsNamedTarget lists programs whose verbs below take a named target worth keeping.
func runsNamedTarget(prog string) bool {
	switch prog {
	case "uv", "npm", "pnpm", "yarn", "bun", "gh", "poetry":
		return true
	}
	return false
}

func isTargetVerb(verb string) bool {
	switch verb {
	case "run", "pr", "repo", "issue", "release", "auth", "exec", "workflow", "run-script":
		return true
	}
	return false
}

// isPySpace is Python's str.isspace, which \s matches.
func isPySpace(r rune) bool { return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f) }

// isPyWord is Python's \w on str: letters, numbers and the underscore.
func isPyWord(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r) }

func skipPySpace(s string, i int) int {
	for i < len(s) {
		r, w := utf8.DecodeRuneInString(s[i:])
		if !isPySpace(r) {
			break
		}
		i += w
	}
	return i
}

func skipPyWord(s string, i int) int {
	for i < len(s) {
		r, w := utf8.DecodeRuneInString(s[i:])
		if !isPyWord(r) {
			break
		}
		i += w
	}
	return i
}
