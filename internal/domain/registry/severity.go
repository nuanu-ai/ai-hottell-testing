package registry

import "strings"

// The severities of a proposal, AnalyticsSeverity of the contract.
const (
	SeverityBad  = "bad"
	SeverityWarn = "warn"
	SeverityInfo = "info"
)

// Severity is the colleague's _severity (conclusions.py) of proposal p over reports, the
// published Deep reports of its sources as decoded JSON: bad only when a check or an
// observation of the proposal's pattern is confirmed; warn when one is suspected or the
// priority is high or medium; info otherwise.
//
// The pattern link: a check is of the pattern when its id is the proposal's pattern_id (D12);
// an observation when its pattern is the pattern_id or starts with it and "_" (D12_stock_boundary),
// as the reports name them. A Deep v2 check has no confirmed status, so only an observation can
// confirm there; a check's confirmed is still read, as the summary of the reports reads it.
func Severity(p Proposal, reports []any) string {
	pattern := text(p, "pattern_id")
	suspected := false
	for _, r := range reports {
		doc, _ := r.(map[string]any)
		checks, _ := doc["checks"].([]any)
		for _, item := range checks {
			c, _ := item.(map[string]any)
			if pattern == "" || text(c, "id") != pattern {
				continue
			}
			switch text(c, "status") {
			case "confirmed":
				return SeverityBad
			case "suspected":
				suspected = true
			}
		}
		observations, _ := doc["observations"].([]any)
		for _, item := range observations {
			o, _ := item.(map[string]any)
			if name := text(o, "pattern"); pattern == "" || (name != pattern && !strings.HasPrefix(name, pattern+"_")) {
				continue
			}
			switch text(o, "status") {
			case "confirmed":
				return SeverityBad
			case "suspected":
				suspected = true
			}
		}
	}
	if priority := text(p, "priority"); suspected || priority == "high" || priority == "medium" {
		return SeverityWarn
	}
	return SeverityInfo
}
