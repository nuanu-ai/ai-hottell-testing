package analytics

import (
	"fmt"
	"sort"
)

// The thresholds of the coldcache signal, as the colleague's builder names them.
const (
	// ColdPauseMin is the pause, in minutes, from the last request to the person's prompt above
	// which the cache may have gone cold.
	ColdPauseMin = 5
	// ColdRatio is the share of the input read from the cache below which the first request
	// after the prompt is cold.
	ColdRatio = 0.5
)

// DetectColdCache finds where the person came back after a pause and the cache had gone cold:
// a prompt more than ColdPauseMin minutes after the last request to the model, whose first
// request after it reads less than ColdRatio of its input from the cache. The episode costs that
// request's cost, nil when it recorded none. Only Claude's native api_request counts (SrcClaude):
// a request read from the transcript has no cost and no proven order. A request is one episode at
// most, however many prompts came before it, so its cost is counted once; a request with no
// input, or whose cache read is negative or above its input, is no evidence. prompts are the session's
// prompts (the agent's notices are not pauses of the person) and api its requests, each oldest first; sid is its id.
func DetectColdCache(sid string, prompts []Prompt, api []APIRecord) []FrictionEpisode {
	var claude []APIRecord
	for _, a := range api {
		if a.Src == SrcClaude {
			claude = append(claude, a)
		}
	}
	var out []FrictionEpisode
	used := map[int]bool{}
	for _, p := range PersonPrompts(prompts) {
		i := sort.Search(len(claude), func(k int) bool { return !claude[k].At.Before(p.At) })
		if i == 0 || i >= len(claude) || used[i] {
			continue
		}
		pause := Minutes(claude[i-1].At, p.At)
		first := claude[i]
		if pause <= ColdPauseMin || first.Input <= 0 || first.Cached < 0 || first.Cached > first.Input {
			continue
		}
		used[i] = true
		ratio := float64(first.Cached) / float64(first.Input)
		if ratio >= ColdRatio {
			continue
		}
		input := first.Input
		text := fmt.Sprintf("пауза %.0f мин → кэш %.0f%% из %s входа", pause, ratio*100, FmtKtok(&input))
		out = append(out, FrictionEpisode{
			Evidence: []Evidence{frictionEvidence(sid, ISO(first.At), text)},
			Cost:     first.Cost,
		})
	}
	return out
}
