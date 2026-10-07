package analytics

import (
	"encoding/json"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// What a read of a file covers, as the colleague's builder names it.
const (
	spanWholeFile = "весь файл"
	replyOpen     = "<send_user_message_question_reply>"
	replyClose    = "</send_user_message_question_reply>"
	myRequest     = "## My request:"
	replyLabel    = "ответ на вопрос агента"

	// PromptKindPrompt is a prompt the person typed.
	PromptKindPrompt = "prompt"
	// PromptKindReply is the person's answer to a question the agent asked (request_user_input).
	PromptKindReply = "reply"
	// PromptKindSystem is a notice of the agent's own sent as a prompt: a task-notification or a
	// cross-session message with nothing of the person's beside it. It is not the person's prompt.
	PromptKindSystem = "system"

	taskNotificationTag = "<task-notification"
	crossSessionTag     = "<cross-session-message"
)

var (
	sedSpanRe  = regexp.MustCompile(`sed\s+-n\s+['"]?(\d+)\s*,\s*(\d+)\s*p`)
	headTailRe = regexp.MustCompile(`\b(head|tail)\s+(?:-n\s*)?-?(\d+)`)
	digitsRe   = regexp.MustCompile(`^\d+$`)
	wordOrDot  = regexp.MustCompile(`[\p{L}\p{N}_.]`)
	skillTokRe = regexp.MustCompile(`^[\p{L}\p{N}_./~@+-]*SKILL\.md$`)
	answerRe   = regexp.MustCompile(`"answer"\s*:\s*"((?:[^"\\]|\\.)*)"`)
	openTagRe  = regexp.MustCompile(`<([a-z_-]+)[^>]*>`)
	filesRe    = regexp.MustCompile(`(?ms)^# Files mentioned by the user:.*?(?:\n\s*\n|$)`)
	// patchFileRe finds the files an apply_patch adds, updates or deletes.
	patchFileRe = regexp.MustCompile(`\*\*\* (?:Add|Update|Delete) File: ([^\s\\]+)`)
)

// ReadTarget is a file a call read and the place of it the call read.
type ReadTarget struct {
	Path string
	// Span is "строки a–b", "первые N", "последние N" or "весь файл".
	Span string
}

// ReadSpan tells which place of a file a shell command reads: the lines of sed -n 'a,bp', the
// first or last N lines of head or tail, or the whole file. One span stands for the whole
// command.
func ReadSpan(cmd string) string {
	if m := sedSpanRe.FindStringSubmatch(cmd); m != nil {
		return "строки " + m[1] + "–" + m[2]
	}
	if m := headTailRe.FindStringSubmatch(cmd); m != nil {
		if m[1] == "head" {
			return "первые " + m[2]
		}
		return "последние " + m[2]
	}
	return spanWholeFile
}

// ReadTargets returns the files a shell command reads with cat, sed, head, tail, nl, less or bat,
// in order, each with the span of ReadSpan. A relative path is joined to cwd when cwd is set.
func ReadTargets(cmd, cwd string) []ReadTarget {
	var out []ReadTarget
	span := ReadSpan(cmd)
	for _, seg := range SplitSegments(cmd) {
		toks := StripPrefix(seg)
		if len(toks) == 0 || !isShellReader(path.Base(toks[0])) {
			continue
		}
		var pos []string
		for _, t := range toks[1:] {
			if !strings.HasPrefix(t, "-") {
				pos = append(pos, t)
			}
		}
		if path.Base(toks[0]) == "sed" && len(pos) > 0 {
			pos = pos[1:]
		}
		for _, p := range pos {
			if digitsRe.MatchString(p) || !wordOrDot.MatchString(p) || p == "|" || p == ">" {
				continue
			}
			out = append(out, ReadTarget{Path: joinCwd(cwd, p), Span: span})
		}
	}
	return out
}

// CallReadTargets returns what one call read: a Claude Read by its file_path, offset and limit,
// otherwise what its shell command reads. args is the call's input as an object, cmd its shell
// command or "".
func CallReadTargets(tool string, args map[string]any, cmd, cwd string) []ReadTarget {
	if file, ok := args["file_path"].(string); ok && tool == "Read" {
		off, hasOff := wholeArg(args["offset"])
		lim, hasLim := wholeArg(args["limit"])
		span := spanWholeFile
		switch {
		case hasOff && hasLim:
			span = "строки " + strconv.FormatInt(off, 10) + "–" + strconv.FormatInt(off+lim, 10)
		case hasLim:
			span = "первые " + strconv.FormatInt(lim, 10)
		}
		return []ReadTarget{{Path: file, Span: span}}
	}
	if cmd == "" {
		return nil
	}
	return ReadTargets(cmd, cwd)
}

// EditedPaths returns the files a call changes: Edit, Write, MultiEdit and NotebookEdit by
// file_path or notebook_path, apply_patch by the files its patch names, joined to cwd. input is
// the call's raw input, read for the patch when args holds neither command nor patch.
func EditedPaths(tool string, args map[string]any, input, cwd string) []string {
	if tool == "apply_patch" {
		var out []string
		for _, f := range patchFiles(args, input) {
			out = append(out, joinCwd(cwd, f))
		}
		return out
	}
	if !isEditTool(tool) {
		return nil
	}
	if p, ok := args["file_path"].(string); ok && p != "" {
		return []string{p}
	}
	if p, ok := args["notebook_path"].(string); ok {
		return []string{p}
	}
	return nil
}

// SkillMdReads returns the SKILL.md files a command reads with a reader program, in order and
// each once. The first positional of rg, grep, sed and awk is their pattern or script unless -e
// or -f gave it. A relative path is joined to cwd; one starting with ~ stays as written, since
// the server does not know the person's home.
func SkillMdReads(cmd, cwd string) []string {
	var found []string
	for _, seg := range SplitSegments(cmd) {
		toks := StripPrefix(seg)
		if len(toks) == 0 {
			continue
		}
		prog := path.Base(toks[0])
		if !isSkillReader(prog) {
			continue
		}
		var pos []string
		skip, explicit := false, false
		for _, t := range toks[1:] {
			switch {
			case skip:
				skip = false
			case isFlagWithValue(t):
				skip = true
				explicit = explicit || t == "-e" || t == "--regexp" || t == "-f" || t == "--file"
			case strings.HasPrefix(t, "-"):
			default:
				pos = append(pos, t)
			}
		}
		if (prog == "rg" || prog == "grep" || prog == "sed" || prog == "awk") && !explicit && len(pos) > 0 {
			pos = pos[1:]
		}
		for _, t := range pos {
			if !skillTokRe.MatchString(t) {
				continue
			}
			p := t
			if !strings.HasPrefix(p, "~") {
				p = joinCwd(cwd, p)
			}
			if !slices.Contains(found, p) {
				found = append(found, p)
			}
		}
	}
	return found
}

// PromptText turns the text of a UserPromptSubmit into what a person reads and its kind. An
// answer to the agent's question (request_user_input) becomes "ответ на вопрос агента: …" of kind
// PromptKindReply; a prompt with "## My request:" is the text after its last one; otherwise the
// client's wrapped blocks (<tag>…</tag>) and the "# Files mentioned by the user:" line are dropped.
// A prompt that starts with a task-notification or a cross-session message and holds nothing else
// is the agent's notice, of kind PromptKindSystem.
func PromptText(raw string) (text, kind string) {
	// A notice is told apart first: a reply or a "## My request:" it quotes is not the person's.
	if NoticeLabel(raw) != "" && strings.TrimSpace(filesRe.ReplaceAllLiteralString(dropWrappedBlocks(raw), " ")) == "" {
		return "", PromptKindSystem
	}
	if _, body, ok := strings.Cut(raw, replyOpen); ok {
		body, _, _ = strings.Cut(body, replyClose)
		answers := replyAnswers(strings.TrimSpace(body))
		if len(answers) == 0 {
			return replyLabel, PromptKindReply
		}
		return replyLabel + ": " + strings.Join(answers, " / "), PromptKindReply
	}
	if i := strings.LastIndex(raw, myRequest); i >= 0 {
		return strings.TrimSpace(raw[i+len(myRequest):]), PromptKindPrompt
	}
	t := dropWrappedBlocks(raw)
	return strings.TrimSpace(filesRe.ReplaceAllLiteralString(t, " ")), PromptKindPrompt
}

// NoticeLabel names the agent's notice raw starts with: «уведомление» for a task-notification,
// «сообщение агента» for a cross-session message; empty for any other text.
func NoticeLabel(raw string) string {
	raw = strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(raw, taskNotificationTag):
		return "уведомление"
	case strings.HasPrefix(raw, crossSessionTag):
		return "сообщение агента"
	}
	return ""
}

// IsNotice tells whether the raw text of a UserPromptSubmit is the agent's notice, not the person's.
func IsNotice(raw string) bool {
	_, kind := PromptText(raw)
	return kind == PromptKindSystem
}

// replyAnswers reads the answers of a question reply: the "answer" of each object of a JSON list
// or object, a non-string answer as its JSON text; when the body is not JSON, every "answer"
// string the text holds.
func replyAnswers(body string) []string {
	var answers []string
	var data any
	if err := json.Unmarshal([]byte(body), &data); err == nil {
		items, ok := data.([]any)
		if !ok {
			items = []any{data}
		}
		for _, item := range items {
			q, ok := item.(map[string]any)
			if !ok {
				continue
			}
			switch a := q["answer"].(type) {
			case nil:
			case string:
				if a != "" {
					answers = append(answers, a)
				}
			default:
				answers = append(answers, compactJSON(a))
			}
		}
		return answers
	}
	for _, m := range answerRe.FindAllStringSubmatch(body, -1) {
		var s string
		if err := json.Unmarshal([]byte(`"`+m[1]+`"`), &s); err != nil {
			s = m[1]
		}
		answers = append(answers, s)
	}
	return answers
}

// dropWrappedBlocks replaces each <name …>…</name> block with a space, the closing tag being the
// first after the opening one, as the colleague's <([a-z_-]+)[^>]*>.*?</\1> does. Go's regexp
// has no back-reference, so the closing tag is searched for by hand; like that pattern, a name
// without a closing tag is retried with its shorter prefixes.
func dropWrappedBlocks(t string) string {
	var b strings.Builder
	i := 0
	for i < len(t) {
		loc := openTagRe.FindStringSubmatchIndex(t[i:])
		if loc == nil {
			break
		}
		start, nameEnd := i+loc[2], i+loc[3]
		tagStart := i + loc[0]
		end := closingTag(t, start, nameEnd)
		if end < 0 {
			b.WriteString(t[i : tagStart+1])
			i = tagStart + 1
			continue
		}
		b.WriteString(t[i:tagStart])
		b.WriteString(" ")
		i = end
	}
	b.WriteString(t[i:])
	return b.String()
}

// closingTag returns the end of the block whose name starts at start: the longest prefix of the
// name t[start:nameEnd] that a closing tag follows after the end of the opening tag, or -1.
func closingTag(t string, start, nameEnd int) int {
	for n := nameEnd; n > start; n-- {
		gt := strings.IndexByte(t[n:], '>')
		if gt < 0 {
			return -1
		}
		after := n + gt + 1
		if j := strings.Index(t[after:], "</"+t[start:n]+">"); j >= 0 {
			return after + j + len("</"+t[start:n]+">")
		}
	}
	return -1
}

// patchFiles are the files an apply_patch names, from its command or patch argument or, without
// them, its raw input.
func patchFiles(args map[string]any, input string) []string {
	text := input
	for _, key := range []string{"command", "patch"} {
		if s, ok := args[key].(string); ok && s != "" {
			text = s
			break
		}
	}
	var files []string
	for _, m := range patchFileRe.FindAllStringSubmatch(text, -1) {
		files = append(files, m[1])
	}
	return files
}

// joinCwd joins a relative p to cwd and cleans it; an absolute p, or any p without cwd, is
// returned as it is.
func joinCwd(cwd, p string) string {
	if strings.HasPrefix(p, "/") || cwd == "" {
		return p
	}
	return path.Join(cwd, p)
}

// wholeArg is an argument that is a whole JSON number, as Python's isinstance(v, int) holds for
// the colleague's builder.
func wholeArg(v any) (int64, bool) {
	f, ok := v.(float64)
	if !ok || f != float64(int64(f)) {
		return 0, false
	}
	return int64(f), true
}

// compactJSON is the JSON text of v without HTML escaping.
func compactJSON(v any) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return ""
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// isShellReader lists the programs whose file arguments ReadTargets counts as read.
func isShellReader(prog string) bool {
	switch prog {
	case "cat", "sed", "head", "tail", "nl", "less", "bat":
		return true
	}
	return false
}

// isSkillReader lists the programs whose file arguments SkillMdReads looks at; rg, grep, sed and
// awk take a pattern or script first.
func isSkillReader(prog string) bool {
	switch prog {
	case "cat", "sed", "head", "tail", "less", "more", "nl", "bat", "rg", "grep", "awk", "view":
		return true
	}
	return false
}

// isFlagWithValue lists the reader flags whose next token is their value, not a file.
func isFlagWithValue(flag string) bool {
	switch flag {
	case "-g", "--glob", "-e", "--regexp", "-t", "--type", "-m", "--max-count", "-A", "-B", "-C",
		"--max-columns", "-f", "--file":
		return true
	}
	return false
}
