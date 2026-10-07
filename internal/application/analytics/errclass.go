package analytics

import "regexp"

var (
	authErrRe       = regexp.MustCompile(`(?i)\b40[13]\b|unauthorized|forbidden|auth_required|invalid (api )?key|invalid token|token expired`)
	rateLimitErrRe  = regexp.MustCompile(`(?i)\b429\b|rate limit|too many requests|quota`)
	networkErrRe    = regexp.MustCompile(`(?i)econnrefused|etimedout|econnreset|could not resolve host|getaddrinfo|connection (refused|reset)|network is unreachable`)
	permissionErrRe = regexp.MustCompile(`(?i)eacces|permission denied|operation not permitted|sandbox.*denied`)
	dependencyErrRe = regexp.MustCompile(`(?i)command not found|modulenotfounderror|no module named|cannot find (module|package)`)
	notFoundErrRe   = regexp.MustCompile(`(?i)enoent|no such file|\b404\b|not found`)
	timeoutErrRe    = regexp.MustCompile(`(?i)timed out|timeout|deadline exceeded`)
	testFailErrRe   = regexp.MustCompile(`(?i)failed|assertionerror|\d+ failed|tests:.*failed`)
	usageErrRe      = regexp.MustCompile(`(?i)syntaxerror|usage:|invalid (option|argument)|unknown (option|flag)`)
)

// errorClassRule is one row of the table: a class, its pattern and whether it is an error of
// the environment.
type errorClassRule struct {
	class string
	re    *regexp.Regexp
	env   bool
}

// errorClasses is the table of §3.2 "Класс ошибки error_class" of the colleague's
// analytics/model.md (docs/ai-hottell/analytics/model.md), the source of truth: the first
// class whose pattern matches the error text, top to bottom, case-insensitively. Added to
// the table: "network is unreachable" under network, from card HT-284. Bare status codes match
// only as whole numbers.
func errorClasses() []errorClassRule {
	return []errorClassRule{
		{"auth", authErrRe, true},
		{"rate_limit", rateLimitErrRe, true},
		{"network", networkErrRe, true},
		{"permission", permissionErrRe, true},
		{"dependency", dependencyErrRe, true},
		{"not_found", notFoundErrRe, true},
		{"timeout", timeoutErrRe, false},
		{"test_failure", testFailErrRe, false},
		{"usage", usageErrRe, false},
	}
}

// ErrorClass is the class of a failed call's error text: auth, rate_limit, network,
// permission, dependency, not_found (the environment classes), timeout, test_failure, usage,
// or other. The rule reads the text only, no model.
func ErrorClass(text string) string {
	for _, c := range errorClasses() {
		if c.re.MatchString(text) {
			return c.class
		}
	}
	return "other"
}

// EnvironmentError reports whether class is an error of the environment rather than of the
// work, the "Среда" column of model.md §3.2, which the catalogue's D07, D24 and D03 read.
func EnvironmentError(class string) bool {
	for _, c := range errorClasses() {
		if c.class == class {
			return c.env
		}
	}
	return false
}
