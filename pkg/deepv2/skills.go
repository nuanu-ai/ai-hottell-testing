package deepv2

import (
	"encoding/json"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var opportunityID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,63}$`) //nolint:gochecknoglobals // compiled once, never written

// OpportunityKinds are the kinds of a skill opportunity: use a skill already installed,
// create a custom one, or review an external one for installation.
func OpportunityKinds() []string { return []string{"use_existing", "create", "install_candidate"} }

// OpportunityReadiness are the states of a skill opportunity's preparation.
func OpportunityReadiness() []string { return []string{"hypothesis", "candidate", "prepared"} }

// PublishedDeep is a published Deep report of one session: the document, as Decode read it, and
// the candidate_sha256 of that published version.
type PublishedDeep struct {
	Doc             any
	CandidateSHA256 string
}

// ValidateSkillReport is validate_skill_report of v2_skills.py: a cross-session report of skill
// opportunities, resting on the user's published Deep reports. deepReports maps each session to
// its published Deep report; the report's corpus covers every one of them at its source version
// and published candidate version, and each opportunity cites tasks and lines of that corpus. A
// custom skill needs evidence from two sessions; an existing skill must be in the report's
// inventory and says nothing of whether it was installed back then; an external skill needs an
// HTTPS source still to be reviewed.
//
// The inventory is the agent's local skills_inventory snapshot, which the server cannot compare
// with anything: only its form is checked (deep-review spec, open point 12).
//
// Errors carry no field path, as in Python: the path part of a *ValidationError names the
// report part, the opportunity or the field.
func ValidateSkillReport(report any, deepReports map[string]PublishedDeep) error {
	r, ok := report.(map[string]any)
	if !ok {
		return fail("skill report", "object required")
	}
	if version, isInt := intValue(r["schema_version"]); r["kind"] != "skill_opportunities" || !isInt || version != 1 {
		return fail("skill report", "invalid identity")
	}
	if _, err := text(r["analyzed_at"], "analyzed_at"); err != nil {
		return err
	}
	names, err := validateInventory(r["inventory"])
	if err != nil {
		return err
	}
	corpus, err := indexCorpus(r["corpus"], deepReports)
	if err != nil {
		return err
	}
	opportunities, ok := r["opportunities"].([]any)
	if !ok {
		return fail("opportunities", "array required")
	}
	seen := make(map[string]bool, len(opportunities))
	for _, item := range opportunities {
		if err := validateOpportunity(item, seen, names, corpus); err != nil {
			return err
		}
	}
	return nil
}

// VerifyInventoryCurrent is verify_inventory_current of v2_skills_report.py: a recommendation
// is published only while the installed skills are those the analysis saw. It runs where the
// snapshot is, on the user's machine: recorded is the snapshot the analysis used, current a
// fresh one or the inventory the report carries.
func VerifyInventoryCurrent(recorded, current map[string]any) error {
	if !reflect.DeepEqual(recorded["scope"], current["scope"]) || !reflect.DeepEqual(recorded["items"], current["items"]) {
		return fail("skill inventory", "changed since analysis; refresh before publication")
	}
	return nil
}

// text is _text: a string that is not only whitespace.
func text(value any, name string) (string, error) {
	s, ok := value.(string)
	if !ok || strings.TrimFunc(s, pythonSpace) == "" {
		return "", fail(name, "nonempty text required")
	}
	return s, nil
}

// validateInventory checks the report's snapshot of installed skills, one item per name, and
// returns their names.
func validateInventory(value any) ([]string, error) {
	inventory, ok := value.(map[string]any)
	if !ok {
		return nil, fail("inventory", "object required")
	}
	if _, err := text(inventory["snapshot_at"], "inventory.snapshot_at"); err != nil {
		return nil, err
	}
	items, ok := inventory["items"].([]any)
	if !ok {
		return nil, fail("inventory.items", "array required")
	}
	names := make([]string, 0, len(items))
	for _, value := range items {
		item, ok := value.(map[string]any)
		if !ok {
			return nil, fail("inventory item", "object required")
		}
		name, err := text(item["name"], "inventory.name")
		if err != nil {
			return nil, err
		}
		if _, err := text(item["description"], "inventory.description"); err != nil {
			return nil, err
		}
		if _, err := text(item["path_class"], "inventory.path_class"); err != nil {
			return nil, err
		}
		if sum, _ := item["description_sha256"].(string); !sha256Re.MatchString(sum) {
			return nil, fail("inventory", "invalid description hash")
		}
		if slices.Contains(names, name) {
			return nil, fail("inventory", "duplicate skill")
		}
		names = append(names, name)
	}
	return names, nil
}

// indexCorpus checks that the corpus lists every published Deep report once at its source
// version and published candidate version, and indexes those reports' tasks.
func indexCorpus(value any, deepReports map[string]PublishedDeep) (reportIndex, error) {
	corpus, ok := value.([]any)
	if !ok || len(corpus) != len(deepReports) {
		return nil, fail("corpus", "must cover all published Deep reports")
	}
	index := make(reportIndex, len(deepReports))
	for _, value := range corpus {
		entry, ok := value.(map[string]any)
		if !ok {
			return nil, fail("corpus entry", "object required")
		}
		sessionID, isString := entry["session_id"].(string)
		deep, published := deepReports[sessionID]
		if _, dup := index[sessionID]; !isString || !published || dup {
			return nil, fail("corpus", "unknown or duplicate session")
		}
		report := publishedReport(deep.Doc)
		if report.sha == "" || entry["source_sha256"] != report.sha {
			return nil, fail("corpus", "source version mismatch")
		}
		sum, _ := entry["deep_report_sha256"].(string)
		if !sha256Re.MatchString(sum) {
			return nil, fail("corpus", "Deep report hash required")
		}
		if sum != deep.CandidateSHA256 {
			return nil, fail("corpus", "Deep report version mismatch")
		}
		index[sessionID] = report
	}
	return index, nil
}

// publishedReport indexes the source version and task boundaries of a published Deep report;
// a field it cannot read stays empty, so nothing can cite it.
func publishedReport(value any) indexedReport {
	doc, _ := value.(map[string]any)
	sha, _ := doc["source_sha256"].(string)
	tasks, _ := doc["tasks"].([]any)
	bounds := make(map[string]span, len(tasks))
	for _, item := range tasks {
		task, _ := item.(map[string]any)
		id, isString := task["task_id"].(string)
		start, okStart := intValue(task["start_line"])
		end, okEnd := intValue(task["end_line"])
		if isString && okStart && okEnd {
			bounds[id] = span{start, end}
		}
	}
	return indexedReport{tasks: bounds, sha: sha}
}

func validateOpportunity(value any, seen map[string]bool, names []string, corpus reportIndex) error {
	item, ok := value.(map[string]any)
	if !ok {
		return fail("opportunity", "object required")
	}
	id, isString := item["id"].(string)
	if !isString || !opportunityID.MatchString(id) || seen[id] {
		return fail("opportunity", "invalid or duplicate ID")
	}
	seen[id] = true
	kind, _ := item["kind"].(string)
	if !slices.Contains(OpportunityKinds(), kind) {
		return fail(id, "invalid kind")
	}
	for _, field := range []string{"title", "recommendation", "why_skill", "alternative", "uncertainty", "verification"} {
		if _, err := text(item[field], id+"."+field); err != nil {
			return err
		}
	}
	if readiness, _ := item["readiness"].(string); !slices.Contains(OpportunityReadiness(), readiness) {
		return fail(id, "invalid readiness")
	}
	sessions, err := opportunitySources(item["sources"], id, corpus)
	if err != nil {
		return err
	}
	switch kind {
	case "create":
		if sessions < 2 {
			return fail(id, "custom skill needs cross-session evidence")
		}
		if _, err := text(item["copy_prompt"], id+".copy_prompt"); err != nil {
			return err
		}
		if truthy(item["installed_skill"]) || truthy(item["external_skill"]) {
			return fail(id, "unexpected skill target")
		}
	case "use_existing":
		if installed, _ := item["installed_skill"].(string); !slices.Contains(names, installed) {
			return fail(id, "skill absent from current inventory")
		}
		if truthy(item["copy_prompt"]) || truthy(item["external_skill"]) {
			return fail(id, "unexpected target")
		}
		if item["historical_availability"] != "unknown" {
			return fail(id, "cannot infer historical availability")
		}
	default:
		return validateExternalSkill(item, id)
	}
	return nil
}

// opportunitySources checks an opportunity's sources against the frozen corpus and returns how
// many distinct sessions they cite.
func opportunitySources(value any, id string, corpus reportIndex) (int, error) {
	sources, ok := value.([]any)
	if !ok || len(sources) == 0 {
		return 0, fail(id, "sources required")
	}
	sessions := make(map[string]bool, len(sources))
	for _, v := range sources {
		source, ok := v.(map[string]any)
		if !ok {
			return 0, fail(id, "invalid source")
		}
		sessionID, _ := source["session_id"].(string)
		report, ok := corpus[sessionID]
		if !ok || source["source_sha256"] != report.sha {
			return 0, fail(id, "source not in frozen corpus")
		}
		taskID, _ := source["task_id"].(string)
		bounds, ok := report.tasks[taskID]
		if !ok {
			return 0, fail(id, "unknown task")
		}
		refs, ok := source["evidence"].([]any)
		if !ok || len(refs) == 0 {
			return 0, fail(id, "evidence required")
		}
		for _, ref := range refs {
			s, _ := ref.(string)
			if !lineRef.MatchString(s) {
				return 0, fail(id, "invalid source line")
			}
			n, err := strconv.Atoi(s[1:])
			if err != nil || n < bounds.start || n > bounds.end {
				return 0, fail(id, "evidence outside task")
			}
		}
		sessions[sessionID] = true
	}
	return len(sessions), nil
}

// validateExternalSkill checks an install candidate: a named external skill at an HTTPS source
// that must be reviewed before installation, and no other target.
func validateExternalSkill(item map[string]any, id string) error {
	external, ok := item["external_skill"].(map[string]any)
	if !ok {
		return fail(id, "external skill source required")
	}
	if _, err := text(external["name"], id+".external_skill.name"); err != nil {
		return err
	}
	url, err := text(external["url"], id+".external_skill.url")
	if err != nil {
		return err
	}
	if !strings.HasPrefix(url, "https://") {
		return fail(id, "HTTPS source required")
	}
	if external["review_status"] != "source_review_required" {
		return fail(id, "install requires source review")
	}
	if truthy(item["copy_prompt"]) || truthy(item["installed_skill"]) {
		return fail(id, "unexpected target")
	}
	return nil
}

// truthy is Python's truth value of a decoded JSON value: absent, null, false, zero, an empty
// string, array or object are false.
func truthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	case int:
		return v != 0
	case float64:
		return v != 0
	case json.Number:
		f, err := v.Float64()
		return err != nil || f != 0
	default:
		return true
	}
}
