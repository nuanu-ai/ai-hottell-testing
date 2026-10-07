// Package redact masks secrets in free text: the one set of rules for analytics, the coach
// journal of hottell-local and the server's decision journal. It started as redact() of the
// colleague's ui/builder/telemetry.py; the rules for names with the keyword after "_", quoted
// keys, CLI flags, URL userinfo, JWT and slashed base64 secrets are ours (HT-375, HT-361).
//
// Every rule is an RE2 expression, so matching is linear in the input length; the bound of
// each is noted beside it.
package redact

import (
	"regexp"
	"slices"
	"strings"
)

// Mask replaces a secret.
const Mask = "[скрыто]"

// space is the body of a character class matching what Python's str.isspace accepts: Go's
// \s, the vertical tab, NEL, every separator (\p{Z}) and the information separators
// U+001C–U+001F. A token pasted after a no-break space or a line separator is still masked.
const space = `\s\v\x{85}\p{Z}\x{1c}-\x{1f}`

// keyword is a name part that marks the value after it as a secret.
const keyword = `token|secret|password|passwd|api[_-]?key|access[_-]?key|authorization`

// shortKeyword is a secret word that marks a value only as a whole part of a name: pass, pwd
// or auth inside passenger, bypass or author is not one (HT-394).
const shortKeyword = `pass|pwd|passphrase|auth|cookie|credentials?|session[_-]?(?:id|key)|private[_-]?key|` + keyword

// shellWord is a value read as the shell reads a word: unquoted characters, a backslash and
// the character after it, "…", '…', $'…' and `…` joined up to a blank outside quotes; a quote
// that never closes runs to the end of the line (HT-377, HT-390).
const shellWord = `(?:\$'(?:[^'\\]|\\(?s:.))*'|"(?:[^"\\]|\\(?s:.))*"|'(?:[^'\\]|\\(?s:.))*'|"[^"]*"|'[^']*'|` +
	"`[^`]*`" + `|\\(?s:.)|[^` + space + `"'` + "`" + `\\])+(?:["'` + "`" + `][^\n]*)?|["'` + "`" + `][^\n]*`

// quotedSeg is one quoted part of a shell word: "…" and $'…' with a backslash escaping the
// character after it, '…' and `…` with none, as the shell reads them.
const quotedSeg = `\$'(?:[^'\\]|\\(?s:.))*'|"(?:[^"\\]|\\(?s:.))*"|'[^']*'|` + "`[^`]*`"

// quotedArg is a flag's value that opens with a quote, read as the shell reads the word: its
// quoted parts, backslash pairs and plain characters up to a blank outside quotes, as in
// 'can'\'"t" or 'can'"'"'t; a quote that never closes, first or later, runs to the end of the
// line (HT-377). A flag's value cannot be a shellWord, which would take an unquoted next flag;
// only one that opens with a quote reads on past a blank.
const quotedArg = `(?:` + quotedSeg + `)(?:` + quotedSeg + `|\\(?s:.)|[^` + space + `"'` + "`" + `\\])*` +
	`(?:["'` + "`" + `][^\n]*)?|["'` + "`" + `][^\n]*`

// shortName is a name whose whole part is a short or another secret word (DB_PASS, MYSQL_PWD,
// Set-Cookie, sessionid; not passenger, bypass or author).
const shortName = `(?:[A-Za-z0-9]+[_.-])*(?:` + shortKeyword + `)(?:[_.-][A-Za-z0-9]+)*`

// keyValuePrefix is a name holding a keyword, an optional closing quote of a JSON or YAML key,
// one separator and an optional auth scheme: groups 1 and 2 of keyValueRe and keyEscapedValueRe.
const keyValuePrefix = `(?i)([A-Za-z0-9_.-]*(?:` + keyword + `)[A-Za-z0-9_.-]*)` +
	`(["']?[` + space + `]*[:=][` + space + `]*)(?:(?:bearer|basic|token)[` + space + `]+)?`

//nolint:gochecknoglobals // compiled once, never written
var (
	// Linear: the lazy body stops at the first END line or runs to the end of the text.
	pemRe = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY(?: BLOCK)?-----.*?(?:-----END [A-Z ]*PRIVATE KEY(?: BLOCK)?-----|$)`)
	// Linear: a scheme, then two character-class runs split by one colon. The user may be
	// empty, as in redis://:password@host (HT-393). user@host without a password is kept: it
	// names a host, not a secret.
	urlUserinfoRe = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^` + space + `/@:]*:[^` + space + `/@]*@`)
	// Linear: three character-class runs around a colon and an at sign. The user is required
	// here: without a scheme, :word@word is as often a decorator or a selector as a password.
	bareUserinfoRe = regexp.MustCompile(`\b[A-Za-z0-9._%+-]+:[^` + space + `/@:]+@([A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*)`)
	// Linear: a name holding a keyword (DB_PASSWORD, client_secret_value, --api-key, "token"),
	// an optional closing quote of a JSON or YAML key, one separator and one value, read as the
	// shell reads a word: unquoted characters, a backslash and the character after it, "…",
	// '…', $'…' and `…` joined up to a blank outside quotes, so abc" x"def is one value and a
	// key right before another key takes it whole (HT-390). In "…", '…' and $'…' a backslash
	// escapes the character after it, so an escaped quote does not end it; where that never
	// closes, the quote is read with no escapes, to the first matching quote. A quote that
	// never closes masks the value to the end of its line, since where it was meant to end is
	// unknown (HT-377). Every part is a character class or a literal inside one repetition.
	keyValueRe = regexp.MustCompile(keyValuePrefix + `(` + shellWord + `)`)
	// Linear: the reading of HT-377, one value token: a quoted value and the rest of its word,
	// a quote that never closes to the end of the line, else a run up to a blank. A shell word
	// may take in the next key without its value (password: abc\ and token: on the next line);
	// this rule still masks that key's value, so the union never masks less than it did.
	keyValueTokenRe = regexp.MustCompile(keyValuePrefix +
		`((?:"(?:[^"\\]|\\(?s:.))*"|'(?:[^'\\]|\\(?s:.))*'|"[^"]*"|'[^']*')[^` + space + `]*|` +
		`["'][^\n]*|[^` + space + `]+)`)
	// Linear: the name and separator of keyValueRe, then a value in quotes with backslashes
	// before them, password=\"a b\", up to the first quote with backslashes before it. The shell
	// reads \" as a plain character, so keyValueRe ends that value at the blank; Secrets masks
	// what either rule takes, so neither can unmask what the other hides (HT-390).
	keyEscapedValueRe = regexp.MustCompile(keyValuePrefix + `(\\+"(?:[^"\\]|\\+[^"\\])*\\+")`)
	// Linear: JSON escaped inside a string, once or more ({\"password\":\"…\"}, HT-392): a name
	// holding a keyword, its escaped closing quote, a colon and a value in escaped quotes, run
	// to the first quote with backslashes before it; a value whose quote never closes runs to
	// the next blank. Like every rule it reads the original text, so a value spanning a shell
	// quote or another assignment never takes away a key another rule masks (HT-392 review).
	escapedKeyValueRe = regexp.MustCompile(`(?i)([A-Za-z0-9_.-]*(?:` + keyword + `)[A-Za-z0-9_.-]*)` +
		`(\\+"[` + space + `]*:[` + space + `]*)` +
		`(\\+"(?:[^"\\]|\\+[^"\\])*\\+"|[^` + space + `]+)`)
	// Linear: a long flag holding a keyword, spaces and a value that is not the next flag; a
	// value in quotes is read by quotedArg.
	flagRe = regexp.MustCompile(`(?i)(^|[` + space + `])(--[a-z0-9-]*(?:` + keyword + `)[a-z0-9-]*)` +
		`([` + space + `]+)(` + quotedArg + `|[^` + space + `-][^` + space + `]*)`)
	// Linear: the client name, a lazy run to the first -p on the line and one value token. -p is
	// a password only for the MySQL clients; for mkdir, ssh or git it is something else.
	mysqlPassRe = regexp.MustCompile(`\b((?i:mysql|mysqldump|mysqladmin|mariadb)\b[^\n]*?[` + space + `]-p)` +
		`([` + space + `]*)(` + quotedArg + `|[^` + space + `-][^` + space + `]*)`)
	// Linear: a keyword and one run of token characters.
	bearerRe = regexp.MustCompile(`(?i)\bbearer[` + space + `]+[A-Za-z0-9._~+/=-]{6,}`)
	// Linear: three character-class runs split by dots.
	jwtRe = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{5,}\.eyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]*`)
	// Linear: a literal prefix and one character-class run.
	skRe = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{10,}`)
	// Linear: literal vendor prefixes, each followed by one character-class run.
	vendorRe = regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[abprs]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16})\b`)
	// Linear: one character-class run; maskBlob then reads it a fixed number of times.
	blobRe = regexp.MustCompile(`[A-Za-z0-9+/=_-]{32,}`)
	// Fixed length: anchored, at most 36 characters are read.
	uuidRe = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	// Linear: anchored, one pass over a path segment.
	wordRe = regexp.MustCompile(`^[A-Za-z][a-z]*(?:[A-Z][a-z]+)*[0-9]*$`)
	// Linear: a URL whose password holds a slash or an at sign (HT-394): the userinfo runs to
	// the last @ before a host. A password of digits then a slash is a port and a path, as in
	// https://registry:443/@scope/pkg, and is kept. Quotes and angle brackets end the URL, so
	// the run does not reach the @ of the next URL in a list; a user has no brackets, so the
	// colons of an IPv6 host ([::1]) are not a password separator.
	urlWideUserinfoRe = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://([^` + space + `/@:\[\]]*:` +
		`(?:[^` + space + `/@0-9"'<>][^` + space + `"'<>]*|[0-9]+[^` + space + `/0-9"'<>][^` + space + `"'<>]*))@[A-Za-z0-9.-]+`)
	// Linear: a name whose whole part is a short or another secret word (DB_PASS, MYSQL_PWD,
	// Set-Cookie, sessionid; not passenger, bypass or author), a separator, ASCII or fullwidth,
	// and one value read as a shell word (HT-394).
	shortKeyRe = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9_.-])(` + shortName + `)` +
		`(["']?[` + space + `]*[:=：＝][` + space + `]*)(` + shellWord + `)`)
	// Linear: a Cookie or Set-Cookie header; every pair after it is hidden to the end of the line.
	cookieRe = regexp.MustCompile(`(?i)\b(?:set-)?cookie[` + space + `]*[:=][ \t]*([^\n]+)`)
	// Linear: a .netrc line, a lazy run to its password and one value token.
	netrcRe = regexp.MustCompile(`(?i)\bmachine[ \t]+[^\n]*?[ \t]password[ \t]+(` + shellWord + `)`)
	// Linear: setx NAME value, the name holding a secret word.
	setxRe = regexp.MustCompile(`(?i)\bsetx[ \t]+(?:[A-Za-z0-9_]*(?:` + keyword + `)[A-Za-z0-9_]*|` + shortName + `)[ \t]+(` + shellWord + `)`)
	// Linear: docker login, a lazy run to -p and one value token.
	dockerPassRe = regexp.MustCompile(`(?i)\bdocker[ \t]+login\b[^\n]*?[ \t]-p[ \t]+(` + shellWord + `)`)
	// Linear: -u or --user with user:password, as curl takes it.
	// A quoted value holds its colon inside the quotes: 'bob:a b' is hidden whole, with the
	// rest of its shell word ("bob:a"b).
	userFlagRe = regexp.MustCompile(`(?:^|[` + space + `])(?:-u|--user)(?:[` + space + `]+|=)` +
		`((?:'[^'\n]*:[^']*'|"[^"\n]*:(?:[^"\\]|\\(?s:.))*")(?:` + shellWord + `)?|[^` + space + `:'"]+:(?:` + shellWord + `)?)`)
	// Linear: a secret flag whose value starts with a single dash.
	dashFlagValueRe = regexp.MustCompile(`(?i)(?:^|[` + space + `])--[a-z0-9-]*(?:` + keyword + `)[a-z0-9-]*[` + space + `]+(-[^` + space + `-][^` + space + `]*)`)
	// Linear: literal vendor prefixes, each followed by one character-class run (HT-394).
	moreVendorRe = regexp.MustCompile(`\b(?:glpat-[A-Za-z0-9_-]{20,}|(?:sk|rk)_(?:live|test)_[A-Za-z0-9]{16,}|` +
		`AIza[0-9A-Za-z_-]{35}|npm_[A-Za-z0-9]{36}|hf_[A-Za-z0-9]{30,}|ASIA[0-9A-Z]{16})`)
	// Linear: a literal host and one character-class run, the path of a Slack webhook.
	slackHookRe = regexp.MustCompile(`hooks\.slack\.com/services/([A-Za-z0-9/_-]+)`)
	// Linear: a YAML block scalar under a secret key: the indented lines after | or >.
	// Blank lines belong to the block; it ends at the next line that starts without a blank.
	// RE2 has no back-references, so a nested block also takes deeper siblings after it: more
	// is hidden, never less.
	yamlBlockRe = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9_.-])(?:[A-Za-z0-9_.-]*(?:` + keyword + `)[A-Za-z0-9_.-]*|` + shortName + `)` +
		`["']?[ \t]*:[ \t]*[|>][-+0-9]*[ \t]*(?:#[^\n]*)?\r?\n((?:[ \t]*\r?\n)*[ \t]+[^\n]*(?:\n(?:[ \t]*\r?\n)*[ \t]+[^\n]*)*)`)
	// Linear: an XML element named with a secret word, up to the next tag.
	xmlRe = regexp.MustCompile(`(?i)<(?:[A-Za-z0-9_.:-]*(?:` + keyword + `)[A-Za-z0-9_.:-]*|` + shortName + `)>([^<]*)</`)
	// Linear: a URL-encoded key=value, the = written %3D.
	urlEncodedRe = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9_.-])(?:[A-Za-z0-9_.-]*(?:` + keyword + `)[A-Za-z0-9_.-]*|` + shortName + `)%3D([^&` + space + `]+)`)
	// Linear: a literal prefix and one character-class run.
	homeRe = regexp.MustCompile(`/Users/[^/\s"']+`)
)

// Secrets masks what looks like a secret: private keys, URL userinfo, key=value secrets,
// secret CLI flags, bearer tokens, JWT and vendor tokens. The key and the separator stay, so
// the text still says what was hidden.
//
// It masks the union of two readings, each naming the spans of the original text it hides.
// In the first every rule reads the original text, so no rule takes away the input of
// another, as a wide value swallowing the key, flag or Bearer a later rule needs would. The
// second replays the rules one after another on the text the earlier ones masked, as Secrets
// did before HT-390, so a match one rule's scan skips is still found once an earlier rule has
// masked what hid it. The union masks no less than either; each is a fixed number of linear
// passes.
func Secrets(text string) string {
	var spans []span
	lower := strings.ToLower(text)
	matches := make(map[*regexp.Regexp][][]int)
	for _, r := range rules() {
		if !strings.Contains(lower, r.needs) {
			continue
		}
		matches[r.re] = r.re.FindAllStringSubmatchIndex(text, -1)
		for _, m := range matches[r.re] {
			if start, end := r.hide(m); start < end {
				spans = append(spans, span{start, end, r.mask})
			}
		}
	}
	return maskSpans(text, append(spans, sequentialSpans(text, lower, matches)...), true)
}

// SecretsAndBlobs is Secrets and the long-blob guess: a key, hash or encoded payload of 32 or
// more characters is masked even with no keyword before it. Analytics uses it, because every
// signed-in user sees every user's texts there; the guess also hides some long names.
func SecretsAndBlobs(text string) string {
	return blobRe.ReplaceAllStringFunc(Secrets(text), maskBlob)
}

// rule is one masking rule: its expression, the part of a match it hides and what it puts
// in its place.
type rule struct {
	re   *regexp.Regexp
	hide func(m []int) (start, end int)
	mask string
	// needs is lower-case text every match holds; a text without it skips the rule.
	needs string
}

// span is a part of the text to hide and its replacement.
type span struct {
	start, end int
	mask       string
}

// rules are the rules of Secrets. Each hides a match but its key, separator, scheme or host.
// keyValueRe, keyValueTokenRe and keyEscapedValueRe read one value three ways (a shell word,
// the token of HT-377, \"…\"); the union keeps what each one hides.
func rules() []rule {
	whole := func(m []int) (int, int) { return m[0], m[1] }
	first := func(m []int) (int, int) { return m[2], m[3] }          // group 1
	afterSeparator := func(m []int) (int, int) { return m[5], m[7] } // an auth scheme and the value
	return []rule{
		{pemRe, whole, Mask, "-----begin"},
		{urlUserinfoRe, func(m []int) (int, int) { return m[3], m[1] - 1 }, Mask, "://"}, // between scheme and @
		{bareUserinfoRe, func(m []int) (int, int) { return m[0], m[2] - 1 }, Mask, "@"},  // before @host
		{keyValueRe, afterSeparator, Mask, ""},
		{keyValueTokenRe, afterSeparator, Mask, ""},
		{keyEscapedValueRe, afterSeparator, Mask, `\"`},
		{escapedKeyValueRe, func(m []int) (int, int) { return m[6], m[7] }, Mask, `\"`},
		{flagRe, func(m []int) (int, int) { return m[8], m[9] }, Mask, "--"},
		{mysqlPassRe, func(m []int) (int, int) { return m[6], m[7] }, Mask, "-p"},
		{bearerRe, whole, "Bearer " + Mask, "bearer"},
		{jwtRe, whole, Mask, "eyj"},
		{skRe, whole, Mask, "sk-"},
		{vendorRe, whole, Mask, ""},
		{urlWideUserinfoRe, first, Mask, "://"},
		{shortKeyRe, func(m []int) (int, int) { return m[5], m[7] }, Mask, ""},
		{cookieRe, first, Mask, "cookie"},
		{netrcRe, first, Mask, "machine"},
		{setxRe, first, Mask, "setx"},
		{dockerPassRe, first, Mask, "docker"},
		{userFlagRe, first, Mask, "-u"},
		{dashFlagValueRe, first, Mask, "--"},
		{moreVendorRe, whole, Mask, ""},
		{slackHookRe, first, Mask, "hooks.slack.com/services/"},
		{yamlBlockRe, first, Mask, "\n"},
		{xmlRe, first, Mask, "</"},
		{urlEncodedRe, first, Mask, "%3d"},
	}
}

// sequentialRules are the rules in the order Secrets replaced them before HT-390: one value
// token for a key (keyValueTokenRe) and the escaped JSON of HT-392 last.
func sequentialRules() []rule {
	all := rules()
	byRe := make(map[*regexp.Regexp]rule, len(all))
	for _, r := range all {
		byRe[r.re] = r
	}
	out := make([]rule, 0, len(all))
	for _, re := range []*regexp.Regexp{
		pemRe, urlUserinfoRe, bareUserinfoRe, keyValueTokenRe, flagRe, mysqlPassRe,
		bearerRe, jwtRe, skRe, vendorRe, escapedKeyValueRe,
	} {
		out = append(out, byRe[re])
	}
	return out
}

// sequentialSpans replays sequentialRules, each on the text the rules before it masked, and
// returns what they hide as spans of the original text: a byte of a mask stands for the
// whole span it hides. Until a rule hides something the text is the original, and the
// matches of the original text, read already, are reused.
// A rule whose literal the original text lacks finds nothing in the masked text either: a
// mask adds none of them.
func sequentialSpans(text, lower string, matches map[*regexp.Regexp][][]int) []span {
	var hidden []span
	for _, r := range sequentialRules() {
		ms, at := matches[r.re], []int(nil)
		if len(hidden) > 0 && strings.Contains(lower, r.needs) {
			var cur string
			cur, at = render(text, hidden)
			ms = r.re.FindAllStringSubmatchIndex(cur, -1)
		}
		var found []span
		for _, m := range ms {
			if start, end := r.hide(m); start < end {
				lo, _ := original(hidden, at, start)
				_, hi := original(hidden, at, end-1)
				found = append(found, span{lo, hi, r.mask})
			}
		}
		if len(found) > 0 {
			hidden = mergeSpans(append(hidden, found...), false)
		}
	}
	return hidden
}

// render is the text with the spans masked and where each mask starts in it.
func render(text string, spans []span) (string, []int) {
	var out strings.Builder
	out.Grow(len(text))
	at := make([]int, 0, len(spans))
	last := 0
	for _, sp := range spans {
		out.WriteString(text[last:sp.start])
		at = append(at, out.Len())
		out.WriteString(sp.mask)
		last = sp.end
	}
	out.WriteString(text[last:])
	return out.String(), at
}

// original is the span [lo, hi) of the original text that byte i of the rendered text
// stands for: itself, or the whole span its mask hides. at is render's.
func original(spans []span, at []int, i int) (int, int) {
	k, _ := slices.BinarySearch(at, i+1) // spans whose mask starts at or before i
	k--
	if k < 0 {
		return i, i + 1
	}
	sp := spans[k]
	if i < at[k]+len(sp.mask) {
		return sp.start, sp.end
	}
	o := sp.end + i - (at[k] + len(sp.mask))
	return o, o + 1
}

// mergeSpans sorts the spans and joins those that overlap, and with touching those that
// touch, into one span under one Mask; a span alone keeps its own mask.
func mergeSpans(spans []span, touching bool) []span {
	slices.SortFunc(spans, func(a, b span) int { return a.start - b.start })
	var out []span
	for i := 0; i < len(spans); {
		cur := spans[i]
		for i++; i < len(spans) && (spans[i].start < cur.end || touching && spans[i].start == cur.end); i++ {
			if spans[i].start != cur.start || spans[i].end != cur.end || spans[i].mask != cur.mask {
				cur.mask = Mask
			}
			cur.end = max(cur.end, spans[i].end)
		}
		out = append(out, cur)
	}
	return out
}

// maskSpans replaces each span with its mask after mergeSpans; the text left between masks
// is what no rule hid.
func maskSpans(text string, spans []span, touching bool) string {
	if len(spans) == 0 {
		return text
	}
	out, _ := render(text, mergeSpans(spans, touching))
	return out
}

// Home replaces /Users/<name> with ~.
func Home(text string) string {
	return homeRe.ReplaceAllLiteralString(text, "~")
}

// maskBlob masks a run as a whole when it is a base64 secret with slashes in it (each part
// may be shorter than 32), else each part between the slashes that looks like a blob.
func maskBlob(run string) string {
	if slashedSecret(run) {
		return Mask
	}
	parts := strings.Split(run, "/")
	for i, p := range parts {
		if looksLikeBlob(p) {
			parts[i] = Mask
		}
	}
	return strings.Join(parts, "/")
}

// slashedSecret: a run not starting with a slash (an absolute path does), whose parts joined
// look like a blob and at least half of whose parts are not words: a relative path such as
// src/components/Button2/IndexPage is words, wJalrXUtnFEMI/K7MDENG/bPxRfiCY… is not.
func slashedSecret(run string) bool {
	if !strings.Contains(run, "/") || strings.HasPrefix(run, "/") {
		return false
	}
	parts := strings.Split(run, "/")
	if !looksLikeBlob(strings.Join(parts, "")) {
		return false
	}
	nonWords := 0
	for _, p := range parts {
		if p != "" && !strings.ContainsAny(p, "_-.") && !wordRe.MatchString(p) {
			nonWords++
		}
	}
	return 2*nonWords >= len(parts)
}

// looksLikeBlob tells a key, hash or encoded payload from a long ordinary name: kebab-case
// names, dated file names and UUIDs stay readable.
func looksLikeBlob(part string) bool {
	core := strings.Trim(part, "=-_+")
	if len(core) < 32 || uuidRe.MatchString(core) {
		return false
	}
	if strings.Trim(core, "0123456789abcdefABCDEF") == "" {
		return true
	}
	longest := 0
	for p := range strings.SplitSeq(core, "-") {
		longest = max(longest, len(p))
	}
	if longest < 20 {
		return false
	}
	var digits, upper, lower int
	for _, c := range core {
		switch {
		case c >= '0' && c <= '9':
			digits++
		case c >= 'A' && c <= 'Z':
			upper++
		case c >= 'a' && c <= 'z':
			lower++
		}
	}
	if digits == 0 {
		return false
	}
	if upper > 0 && lower > 0 {
		return true
	}
	return 4*digits >= len(core) // at least a quarter digits
}
