package analytics

import (
	"encoding/json"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"git.alva.dev/alva/harness-telemetry/pkg/redact"
)

// DisplayTool is the name a tool is shown by. Codex glues a namespace (collaboration, clock,
// image_gen, web) to its tool in the hooks (collaborationspawn_agent); that is shown as
// namespace.tool (collaboration.spawn_agent).
func DisplayTool(name string) string {
	for _, ns := range []string{"collaboration", "clock", "image_gen", "web"} {
		if strings.HasPrefix(name, ns) && len(name) > len(ns) && !strings.HasPrefix(name, ns+".") &&
			!strings.HasPrefix(name, "mcp__") {
			return ns + "." + strings.TrimLeft(name[len(ns):], "_")
		}
	}
	return name
}

// ToolSummary is a short text of one call, at most limit runes after Clean: the file of a patch,
// the question asked, the command, the skill, the subagent, the file, the pattern, the address or
// another telling argument, the first line of code, or else the tool's name. input is the call's
// input as the JSON text the agent sent.
func ToolSummary(tool, input string, limit int) string { return toolSummary(nil, tool, input, limit) }

// toolSummary is ToolSummary, masking through the build's memo.
func toolSummary(memo *redact.Memo, tool, input string, limit int) string {
	var args map[string]any
	if input == "" || json.Unmarshal([]byte(input), &args) != nil || args == nil {
		return cleanWith(memo, tool+" · "+input, limit)
	}
	if tool == "apply_patch" {
		files := patchFiles(args, input)
		more, first := "", ""
		if len(files) > 1 {
			more = " +" + strconv.Itoa(len(files)-1)
		}
		if len(files) > 0 {
			first = path.Base(files[0])
		}
		return cleanWith(memo, "apply_patch · "+first, limit-utf8.RuneCountInString(more)) + more
	}
	if isWaitTool(tool) {
		return cleanWith(memo, "вопрос вам · "+firstQuestion(args), limit)
	}
	for _, key := range []string{"command", "cmd"} {
		if s, ok := args[key].(string); ok {
			return cleanWith(memo, s, limit)
		}
	}
	name := DisplayTool(tool)
	switch tool {
	case "Skill":
		return cleanWith(memo, "skill · "+firstString(args, "skill", "name"), limit)
	case "Agent":
		return cleanWith(memo, "Agent → "+firstString(args, "subagent_type")+" · "+firstString(args, "description"), limit)
	}
	for _, key := range []string{"file_path", "path", "notebook_path"} {
		if s, ok := args[key].(string); ok {
			return cleanWith(memo, name+" · "+s, limit)
		}
	}
	if pattern, ok := args["pattern"].(string); ok {
		where := ""
		if p, ok := args["path"].(string); ok {
			where = " в " + p
		}
		return cleanWith(memo, name+" · "+pattern+where, limit)
	}
	for _, key := range []string{"url", "title", "task_name", "target", "query", "description", "skill"} {
		if s, ok := args[key].(string); ok && s != "" {
			return cleanWith(memo, name+" · "+s, limit)
		}
	}
	if code, ok := args["code"].(string); ok {
		first := ""
		for line := range strings.Lines(code) {
			if strings.TrimSpace(line) != "" {
				first = strings.TrimRight(line, "\r\n")
				break
			}
		}
		return cleanWith(memo, name+" · "+first, limit)
	}
	return cleanWith(memo, name, limit)
}

// firstQuestion is the title or text of the first question of a question tool's input.
func firstQuestion(args map[string]any) string {
	qs, _ := args["questions"].([]any)
	if len(qs) == 0 {
		return ""
	}
	q, _ := qs[0].(map[string]any)
	return firstString(q, "title", "question")
}

// firstString is the first of keys whose value in args is a non-empty value, as its text; ""
// when none is. A value that is not a string is shown as its JSON text.
func firstString(args map[string]any, keys ...string) string {
	for _, key := range keys {
		switch v := args[key].(type) {
		case nil:
		case string:
			if v != "" {
				return v
			}
		case bool:
			if v {
				return "True"
			}
		default:
			if s := compactJSON(v); s != "0" && s != "[]" && s != "{}" {
				return s
			}
		}
	}
	return ""
}

// isEditTool lists the tools that change files.
func isEditTool(tool string) bool {
	switch tool {
	case "apply_patch", "Edit", "Write", "MultiEdit", "NotebookEdit":
		return true
	}
	return false
}

// isWaitTool lists the tools that ask the person a question and wait for the answer.
func isWaitTool(tool string) bool {
	switch tool {
	case "request_user_input", "request_user_input_async", "AskUserQuestion":
		return true
	}
	return false
}
