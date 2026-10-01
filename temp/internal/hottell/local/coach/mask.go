package coach

import "regexp"

// maskText replaces a secret in the journal.
const maskText = "[скрыто]"

// masker hides secrets and the home directory with the rules of redact() in
// ui/builder/telemetry.py, without its "looks like a key" guess. Client paths and names of
// third parties cannot be recognised this way; the hottell-coach skill masks them.
type masker struct {
	pem, kv, bearer, sk, vendor, home *regexp.Regexp
}

func newMasker() masker {
	return masker{
		pem:    regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`),
		kv:     regexp.MustCompile(`(?i)\b(token|secret|password|passwd|api[_-]?key|authorization)(\s*[:=]\s*)(?:(?:bearer|basic|token)\s+)?("[^"]*"|'[^']*'|\S+)`),
		bearer: regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{6,}`),
		sk:     regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{10,}`),
		vendor: regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[abprs]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16})\b`),
		home:   regexp.MustCompile(`/Users/[^/\s"']+`),
	}
}

func (k masker) text(s string) string {
	s = k.pem.ReplaceAllString(s, maskText)
	s = k.kv.ReplaceAllString(s, "${1}${2}"+maskText)
	s = k.bearer.ReplaceAllString(s, "Bearer "+maskText)
	s = k.sk.ReplaceAllString(s, maskText)
	s = k.vendor.ReplaceAllString(s, maskText)
	return k.home.ReplaceAllString(s, "~")
}
