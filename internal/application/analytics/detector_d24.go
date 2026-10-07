package analytics

import (
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
)

// The parameters of D24 in the colleague's analytics/catalogue.yaml; its period_days is
// repeatPeriod.
const (
	// d24MinErrors is how many errors of a server make an episode in one session.
	d24MinErrors = 3
	// d24MinSessions is in how many sessions of one person within repeatPeriod one error each
	// makes an episode.
	d24MinSessions = 2
)

// d24Classes are the error classes D24 counts.
func d24Class(class string) bool {
	return class == "auth" || class == "rate_limit" || class == "network"
}

// d24Session is what one session tells of one MCP server: its counted errors, the failed calls of
// the server after the first of them, and whether the agent called it again at all.
type d24Session struct {
	s       FindingSession
	server  string
	errors  []Call
	firstAt time.Time
	after   int
	again   bool
}

// d24Sessions are the sessions in which the agent called a server again after an error of it
// that D24 counts: auth, rate_limit or network, less a rate_limit the next call of the server
// recovers from. A session whose server answered a call successfully after its last error is
// left out: the server recovered, as the analyzer's single 429 with a successful retry. after
// counts only the failed calls after the first error: a successful one cost nothing.
func d24Sessions(s FindingSession) []d24Session {
	byServer := map[string][]int{}
	var servers []string
	for i := range s.Calls {
		srv := s.Calls[i].MCP
		if srv == "" {
			continue
		}
		if byServer[srv] == nil {
			servers = append(servers, srv)
		}
		byServer[srv] = append(byServer[srv], i)
	}
	var out []d24Session
	for _, srv := range servers {
		idx := byServer[srv]
		d := d24Session{s: s, server: srv}
		recovered := false
		for k, i := range idx {
			c := &s.Calls[i]
			if len(d.errors) > 0 {
				d.again = true
				if c.State == StateError {
					d.after++
				}
			}
			switch c.State {
			case StateError:
				recovered = false
			case StateSuccess:
				recovered = len(d.errors) > 0
				continue
			default:
				continue
			}
			class := ErrorClass(callErrorText(c))
			if !d24Class(class) {
				continue
			}
			if class == "rate_limit" && k+1 < len(idx) && s.Calls[idx[k+1]].State == StateSuccess {
				continue
			}
			if len(d.errors) == 0 {
				d.firstAt = c.At
			}
			d.errors = append(d.errors, *c)
		}
		if len(d.errors) > 0 && d.again && !recovered {
			out = append(out, d)
		}
	}
	return out
}

// d24Key is a person's MCP server.
type d24Key struct {
	user   uuid.UUID
	server string
}

// D24Findings are the cards of D24 «Неработающий MCP», one per server, ordered by server: the
// sessions where the agent called the server again after an error of it and either it erred
// d24MinErrors times in the session, or the person met its errors in d24MinSessions sessions
// within repeatPeriod. Each card is bad, the catalogue's high, kind diagnostic; its impact is the
// failed calls of the server after the first error.
func D24Findings(sessions []FindingSession) []Finding {
	byKey := map[d24Key][]d24Session{}
	for _, s := range sessions {
		for _, d := range d24Sessions(s) {
			k := d24Key{s.Key.UserID, d.server}
			byKey[k] = append(byKey[k], d)
		}
	}
	byServer := map[string][]d24Session{}
	for k, ds := range byKey {
		byServer[k.server] = append(byServer[k.server], d24Flagged(ds)...)
	}
	servers := make([]string, 0, len(byServer))
	for srv, ds := range byServer {
		if len(ds) > 0 {
			servers = append(servers, srv)
		}
	}
	slices.Sort(servers)
	out := make([]Finding, 0, len(servers))
	for _, srv := range servers {
		out = append(out, d24Finding(srv, byServer[srv]))
	}
	return out
}

// d24Flagged are the sessions of one person and server D24 flags: those with d24MinErrors
// errors, and those d24MinSessions of which fall within repeatPeriod.
func d24Flagged(ds []d24Session) []d24Session {
	slices.SortFunc(ds, func(a, b d24Session) int { return a.firstAt.Compare(b.firstAt) })
	flagged := make([]bool, len(ds))
	for i, d := range ds {
		if len(d.errors) >= d24MinErrors {
			flagged[i] = true
		}
		j := i
		for j+1 < len(ds) && ds[j+1].firstAt.Sub(d.firstAt) <= repeatPeriod {
			j++
		}
		if j-i+1 >= d24MinSessions {
			for k := i; k <= j; k++ {
				flagged[k] = true
			}
		}
	}
	var out []d24Session
	for i, d := range ds {
		if flagged[i] {
			out = append(out, d)
		}
	}
	return out
}

// d24Finding is the card of one server over its flagged sessions.
func d24Finding(server string, ds []d24Session) Finding {
	name := Clean(server, 60)
	var (
		ev     []Evidence
		sids   []string
		bySID  = map[string]float64{}
		after  int
		errors int
	)
	for _, d := range ds {
		sid := d.s.Key.SessionID
		sids = append(sids, sid)
		bySID[sid] += float64(d.after)
		after += d.after
		errors += len(d.errors)
		for i := range d.errors {
			c := &d.errors[i]
			ev = append(ev, Evidence{
				SID: sid, Line: d.s.callLine(c), At: ISO(c.At),
				Text: Clean("ошибка MCP "+server+" · "+ErrorClass(callErrorText(c))+" · "+c.Note, 160),
			})
		}
	}
	slices.Sort(sids)
	sids = slices.Compact(sids)
	what := fmt.Sprintf("MCP-сервер %s отвечал ошибками среды (auth, rate_limit, network) — %d в %d сессиях, "+
		"а агент обращался к нему снова и сервер так и не ответил успешно: %d вызовов с ошибкой после первой. "+
		"Каждый такой вызов — затраты без результата и риск ложных выводов агента.", name, errors, len(sids), after)
	f := NewFinding("D24:"+server, SevBad, Clean("Неработающий MCP: "+server, 80), what, ev, sids)
	f.PatternID = "D24"
	f.Episodes = len(ds)
	f.Impact = &Impact{Value: fmt.Sprintf("%d вызовов", after), Label: "после первой ошибки"}
	f.ImpactBySession = bySID
	f.ImpactUnit = "вызовов"
	f.Where = "настройки MCP-сервера " + name
	f.Snip = "Кандидат правки: обновить ключ или токен MCP-сервера " + name + " либо отключить его в настройках проекта."
	f.AlternativeCauses = []string{
		"Сервер был недоступен короткое время, и сбой уже прошёл.",
		"Ошибки вызывает запрос агента, а не настройки сервера.",
	}
	f.Preconditions = []string{"Ошибки сервера повторяются в новых сессиях."}
	verification := "Вызовов сервера " + name + " с ошибкой в новых сессиях нет (mcp_error_calls = 0)."
	f.Verification = &verification
	return f
}
