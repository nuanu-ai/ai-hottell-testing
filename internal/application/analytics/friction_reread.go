package analytics

import (
	"cmp"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// RereadMin is how many reads in a row of one place of a file make a reread episode.
const RereadMin = 3

// maxEpisodeEvidence is how many evidence lines one reread episode keeps: the newest.
const maxEpisodeEvidence = 8

// readErrorRe is a reader program's error at the start of a response: the agent saw no content.
var readErrorRe = regexp.MustCompile(`^(?:cat|sed|head|tail|nl|less|bat): [^\n]*?: ` +
	`(?:No such file or directory|Permission denied|Is a directory|Operation not permitted)`)

// ReadSignature is the hash of a read's recorded response, as the store computes it over the whole
// response; false when the call has no Post or an empty response, whose content is unknown.
func ReadSignature(c *Call) (string, bool) {
	if !c.HasPost() || c.RespLen == 0 {
		return "", false
	}
	return c.RespHash, true
}

// ReadFailed tells a read the agent saw no content of: its outcome is an error, or the first line
// of its response, past the Codex preamble, is a reader program's error.
func ReadFailed(c *Call) bool {
	head := codexPreambleRe.ReplaceAllString(strings.TrimLeft(c.RespHead, " \t\r\n"), "")
	return c.State == StateError || readErrorRe.MatchString(head)
}

// RereadRead is one read of a place of a file with known content: its call and response hash.
type RereadRead struct {
	Call *Call
	Sig  string
}

// RereadRuns returns the reread runs of the reads of one agent and one place of a file, in time
// order: reads in a row with the same content and no cut (a compaction or an edit of the file;
// cuts sorted) between them, RereadMin or more.
func RereadRuns(items []RereadRead, cuts []time.Time) [][]*Call {
	var runs [][]*Call
	var run []*Call
	seg, last := -1, ""
	for _, it := range items {
		k := sort.Search(len(cuts), func(i int) bool { return cuts[i].After(it.Call.At) })
		if len(run) > 0 && (k != seg || it.Sig != last) {
			runs = append(runs, run)
			run = nil
		}
		run = append(run, it.Call)
		seg, last = k, it.Sig
	}
	if len(run) > 0 {
		runs = append(runs, run)
	}
	return slices.DeleteFunc(runs, func(r []*Call) bool { return len(r) < RereadMin })
}

// rereadKey is the agent and the place of a file whose reads make one series.
type rereadKey struct {
	agentID string
	target  ReadTarget
}

// RereadFriction returns the reread episodes of the session sid: one place of a file read
// RereadMin or more times in a row by one agent (each subagent apart) with the same response hash,
// no compaction and no edit of the file between the reads. A read without a result, with an empty
// response or an error, a command on several files and a tail of a log (another process appends
// to it) do not count. cwd is the session's working folder, which relative paths join.
func RereadFriction(sid string, calls []Call, compacts []telemetry.HookEvent, cwd string) []FrictionEpisode {
	compAt := make([]time.Time, 0, len(compacts))
	for _, c := range compacts {
		compAt = append(compAt, c.Time)
	}
	edits := map[string][]time.Time{}
	for i := range calls {
		c := &calls[i]
		for _, p := range EditedPaths(c.Tool, c.Args, c.Input, cwd) {
			edits[p] = append(edits[p], c.At)
		}
	}

	var keys []rereadKey
	reads := map[rereadKey][]RereadRead{}
	for i := range calls {
		c := &calls[i]
		targets := uniqueTargets(CallReadTargets(c.Tool, c.Args, c.Cmd, cwd))
		if len(targets) != 1 {
			continue
		}
		sig, ok := ReadSignature(c)
		if !ok || ReadFailed(c) || strings.HasPrefix(targets[0].Span, "последние") {
			continue
		}
		k := rereadKey{agentID: c.AgentID, target: targets[0]}
		if _, seen := reads[k]; !seen {
			keys = append(keys, k)
		}
		reads[k] = append(reads[k], RereadRead{Call: c, Sig: sig})
	}
	slices.SortStableFunc(keys, func(a, b rereadKey) int { return cmp.Compare(len(reads[b]), len(reads[a])) })

	var out []FrictionEpisode
	for _, k := range keys {
		cuts := slices.Concat(compAt, edits[k.target.Path])
		slices.SortFunc(cuts, time.Time.Compare)
		name := rereadName(k)
		for _, run := range RereadRuns(reads[k], cuts) {
			ep := FrictionEpisode{Path: k.target.Path, Extra: len(run) - 1}
			for _, x := range run[1:] {
				ep.Bytes += x.RespLen
			}
			for n, x := range run {
				ep.Evidence = append(ep.Evidence, frictionEvidence(sid, ISO(x.At),
					name+" · чтение "+strconv.Itoa(n+1)+"/"+strconv.Itoa(len(run))))
			}
			ep.Evidence = ep.Evidence[max(0, len(ep.Evidence)-maxEpisodeEvidence):]
			out = append(out, ep)
		}
	}
	return out
}

// rereadName is how evidence names the place: the file's base name, the span unless it is the
// whole file, and a subagent's mark.
func rereadName(k rereadKey) string {
	name := path.Base(k.target.Path)
	if k.target.Span != spanWholeFile {
		name += " " + k.target.Span
	}
	if k.agentID != "" {
		name += " · субагент"
	}
	return name
}

// uniqueTargets is targets with each repeat dropped, in order: one file named twice is one read.
func uniqueTargets(targets []ReadTarget) []ReadTarget {
	var out []ReadTarget
	for _, t := range targets {
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}
