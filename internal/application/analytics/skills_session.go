package analytics

import (
	"encoding/json"
	"path"
	"regexp"
	"strings"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// The skill sources of the colleague's skill_source (telemetry.py), the classes HT-312 matches to
// the path_class of the skills snapshot. A source outside them is the folder above the skill's
// folder, cut to 80 characters.
const (
	// SkillSourceClaude is a skill Claude Code activated with its Skill tool.
	SkillSourceClaude = "Claude Skill"
	// SkillSourcePluginPrefix starts "плагин <marketplace>/<plugin>" of a plugin's skill.
	SkillSourcePluginPrefix = "плагин "
	SkillSourceCodexSystem  = "системные (~/.codex/skills/.system)"
	SkillSourceCodexPersona = "личные (~/.codex/skills)"
	SkillSourceAgents       = "~/.agents/skills"
	SkillSourceUnknown      = "неизвестно"
)

var (
	pluginSkillRe = regexp.MustCompile(`/plugins/cache/([^/]+)/([^/]+)/`)
	frontNameRe   = regexp.MustCompile(`^name:\s*['"]?([^'"]+?)['"]?\s*$`)
	// codexPreambleRe is the preamble Codex puts before a command's output, as the ClickHouse
	// adapter strips it before hashing.
	codexPreambleRe = regexp.MustCompile(`^(?:(?:Chunk ID|Wall time|Process exited with code|Original token count|Exit code)[^\n]*\n)*(?:Output:\n)?`)
)

// SkillActivation is one use of a skill in a session: Claude's Skill tool, a Claude Read of a
// SKILL.md, or a shell command that reads one.
type SkillActivation struct {
	At   time.Time
	Name string
	// Path is the SKILL.md read, "" for the Skill tool.
	Path   string
	Source string
	// Tool is the tool of the call: Skill, Read or the shell tool.
	Tool      string
	ToolUseID string
	// SizeKtok is the skill's size in thousands of tokens, the response length / 4 / 1000, for a
	// read of the whole file; nil otherwise.
	SizeKtok *float64
}

// SessionSkills lists the skill activations among one session's calls (BuildCalls) in the order
// of the calls, as the "skills" block of the colleague's build_session. A shell path is joined to
// cwd, the session's most common one (SessionCwd).
func SessionSkills(calls []Call, cwd string) []SkillActivation {
	var out []SkillActivation
	for _, c := range calls {
		if c.Tool == "Skill" {
			name, _ := c.Args["skill"].(string)
			if name == "" {
				name, _ = c.Args["name"].(string)
			}
			if name == "" {
				name = "?"
			}
			out = append(out, SkillActivation{
				At: c.At, Name: name, Source: SkillSourceClaude, Tool: c.Tool, ToolUseID: c.ToolUseID,
			})
			continue
		}
		var paths []string
		whole := false
		if c.Tool == "Read" {
			if file, _ := c.Args["file_path"].(string); strings.HasSuffix(file, "SKILL.md") {
				paths = []string{file}
				_, hasOff := c.Args["offset"]
				_, hasLim := c.Args["limit"]
				whole = !hasOff && !hasLim
			}
		} else if c.Cmd != "" {
			paths = SkillMdReads(c.Cmd, cwd)
			whole = ReadSpan(c.Cmd) == spanWholeFile
		}
		for _, p := range paths {
			a := SkillActivation{
				At: c.At, Name: SkillName(p, c.RespHead), Path: p, Source: SkillSource(p), Tool: c.Tool,
				ToolUseID: c.ToolUseID,
			}
			if whole && c.RespLen > 0 {
				size := float64(c.RespLen) / 4 / 1000
				a.SizeKtok = &size
			}
			out = append(out, a)
		}
	}
	return out
}

// SkillName is a skill's name: the name field of the frontmatter at the start of the recorded
// response head (the block between a first line --- and the next ---; a name: after it does not
// count), otherwise the SKILL.md's folder, or the folder above it for the generic skill and
// skills. The colleague read the frontmatter from the disk; the server only has the response.
func SkillName(p, head string) string {
	text := responseText(head)
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for _, ln := range lines[1:] {
			if strings.TrimSpace(ln) == "---" {
				break
			}
			if m := frontNameRe.FindStringSubmatch(strings.TrimSpace(ln)); m != nil {
				return strings.TrimSpace(m[1])
			}
		}
	}
	dir := path.Dir(p)
	name := path.Base(dir)
	if dir == "." || dir == "/" {
		name = ""
	}
	if l := strings.ToLower(name); l == "skill" || l == "skills" || l == "" {
		if up := path.Base(path.Dir(dir)); up != "." && up != "/" && dir != "." {
			name = up
		}
	}
	if name == "" {
		return p
	}
	return name
}

// SkillSource classes where a SKILL.md lives, as the colleague's skill_source.
func SkillSource(p string) string {
	if m := pluginSkillRe.FindStringSubmatch(p); m != nil {
		return SkillSourcePluginPrefix + m[1] + "/" + m[2]
	}
	switch {
	case strings.Contains(p, "/.codex/skills/.system/"):
		return SkillSourceCodexSystem
	case strings.Contains(p, "/.codex/skills/"):
		return SkillSourceCodexPersona
	case strings.Contains(p, "/.agents/skills/"):
		return SkillSourceAgents
	}
	parent := path.Dir(path.Dir(p))
	if p == "" || parent == "." {
		return SkillSourceUnknown
	}
	return Clean(parent, 80)
}

// responseText is what a response head shows of the file: the content of a Claude Read response
// ({"type":"text","file":{…,"content":"…"}}, whose JSON the cut may leave unclosed), or the
// command output without the Codex preamble.
func responseText(head string) string {
	if strings.HasPrefix(strings.TrimSpace(head), "{") {
		const key = `"content":"`
		i := strings.Index(head, key)
		if i < 0 {
			return ""
		}
		return jsonStringPrefix(head[i+len(key):])
	}
	return codexPreambleRe.ReplaceAllString(head, "")
}

// jsonStringPrefix decodes a JSON string body up to its closing quote or, when the cut removed
// it, to the end.
func jsonStringPrefix(s string) string {
	end := len(s)
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == '"' {
			end = i
			break
		}
	}
	body := s[:end]
	for len(body) > 0 {
		var out string
		if json.Unmarshal([]byte(`"`+body+`"`), &out) == nil {
			return out
		}
		body = body[:len(body)-1] // a cut escape at the end
	}
	return ""
}

// SessionProject is the project of a session (HT-507): the name of its git repository, which
// the binary sends with each hook event (hottell.repo.remote, hottell.repo.root), so that the
// sessions of every worktree of a repository are one project. The repository is the one of the
// events in the session's folder cwd, else of the first event that names one; its name is the
// last segment of the remote without .git, else of the common git directory's repository. A
// session no event names a repository of is the last folder of cwd (HT-492).
func SessionProject(events []telemetry.HookEvent, cwd string) string {
	var repo *telemetry.HookEvent
	for i := range events {
		e := &events[i]
		if e.RepoRoot == "" && e.RepoRemote == "" {
			continue
		}
		if e.Cwd == cwd {
			repo = e
			break
		}
		if repo == nil {
			repo = e
		}
	}
	if repo != nil {
		if name := remoteName(repo.RepoRemote); name != "" {
			return name
		}
		if name := gitDirName(repo.RepoRoot); name != "" {
			return name
		}
	}
	return projectName(cwd)
}

// remoteName is the repository's name in a remote address: the last segment of its path, after
// a slash or the colon of a host:path address, without .git; https://host/dev/demo.git and
// git@host:demo.git name demo. Empty for an address with no name.
func remoteName(remote string) string {
	r := strings.TrimRight(strings.TrimSpace(remote), "/")
	if i := strings.LastIndexAny(r, "/:"); i >= 0 {
		r = r[i+1:]
	}
	return strings.TrimSuffix(r, ".git")
}

// gitDirName is the repository's name of its common git directory: the folder that holds .git
// (/Users/dev/demo/.git names demo), or a bare repository's own folder without .git
// (/srv/demo.git names demo). Empty for none.
func gitDirName(root string) string {
	root = strings.TrimRight(root, "/")
	if root == "" {
		return ""
	}
	if path.Base(root) == ".git" {
		root = path.Dir(root)
	}
	name := strings.TrimSuffix(path.Base(root), ".git")
	if name == "/" || name == "." {
		return ""
	}
	return name
}

// SessionCwd is where a session started: the cwd of its first SessionStart or UserPromptSubmit
// that carries one, so work in a subfolder (web, internal/…) does not rename the project. Without
// such a cwd it is the cwd most of the events carry; the first seen wins a tie. All the events are
// counted first: a running count would hand a tie to the cwd that reached it last.
func SessionCwd(events []telemetry.HookEvent) string {
	for _, e := range events {
		if e.Cwd != "" && (e.Event == "SessionStart" || e.Event == "UserPromptSubmit") {
			return e.Cwd
		}
	}
	count := map[string]int{}
	var order []string
	for _, e := range events {
		if e.Cwd == "" {
			continue
		}
		if count[e.Cwd] == 0 {
			order = append(order, e.Cwd)
		}
		count[e.Cwd]++
	}
	best := ""
	for _, cwd := range order {
		if count[cwd] > count[best] {
			best = cwd
		}
	}
	return best
}
