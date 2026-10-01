package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Пульс шапки «Живых данных» (README пакета v5.1, «Анимация»): события хуков за 10 минут
// по 10 с и сессии, идущие сейчас. ClickHouse — тот же, что у live.py; ответ кэшируется на 5 с,
// чтобы несколько вкладок не нагружали стенд. Только loopback.

const (
	pulseTTL     = 5 * time.Second
	pulseTimeout = 4 * time.Second
)

const pulseBarsSQL = `SELECT toString(toStartOfInterval(Timestamp, INTERVAL 10 SECOND)) t, LogAttributes['agent'] agent, toUInt32(count()) n
FROM otel_logs
WHERE ServiceName = 'agent-hooks' AND Timestamp > now() - INTERVAL 10 MINUTE
GROUP BY t, agent ORDER BY t
FORMAT JSONEachRow`

const pulseActiveSQL = `SELECT LogAttributes['session_id'] sid, any(LogAttributes['agent']) agent,
  anyIf(LogAttributes['cwd'], LogAttributes['cwd'] != '') cwd, toString(max(Timestamp)) last_at,
  toUInt32(countIf(Timestamp > now() - INTERVAL 1 MINUTE)) per_min
FROM otel_logs
WHERE ServiceName = 'agent-hooks' AND Timestamp > now() - INTERVAL 2 MINUTE AND LogAttributes['session_id'] != ''
GROUP BY sid ORDER BY last_at DESC LIMIT 20
FORMAT JSONEachRow`

type pulseBar struct {
	T     string `json:"t"`
	Agent string `json:"agent"`
	N     int    `json:"n"`
}

type pulseActive struct {
	SID     string `json:"sid"`
	Agent   string `json:"agent"`
	Project string `json:"project"`
	First   string `json:"first,omitempty"`
	LastAt  string `json:"last_at"`
	PerMin  int    `json:"per_min"`
}

type pulse struct {
	At     time.Time     `json:"at"`
	Bars   []pulseBar    `json:"bars"`
	Active []pulseActive `json:"active"`
	Error  string        `json:"error,omitempty"`
}

// pulseSource is the ClickHouse behind /api/pulse; an empty address means no pulse (:8800).
type pulseSource struct {
	clickhouse string // HTTP interface, e.g. http://127.0.0.1:8123
	db         string
	mu         sync.Mutex // one ClickHouse query for all tabs: the others wait and take the cache
	last       *pulse
}

func (s *server) pulseHandler(w http.ResponseWriter, r *http.Request) {
	if !loopbackOnly(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "только с этой машины"})
		return
	}
	if s.pulse.clickhouse == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "пульса в этой версии нет"})
		return
	}
	s.pulse.mu.Lock()
	defer s.pulse.mu.Unlock()
	if p := s.pulse.last; p != nil && time.Since(p.At) < pulseTTL {
		writeJSON(w, http.StatusOK, p)
		return
	}
	// не контекст запроса: ответ общий для всех вкладок, и закрытая вкладка не должна оставить в кэше «context canceled»
	ctx, cancel := context.WithTimeout(context.Background(), pulseTimeout)
	defer cancel()
	p := s.fetchPulse(ctx)
	s.pulse.last = &p
	writeJSON(w, http.StatusOK, p)
}

func (s *server) fetchPulse(ctx context.Context) pulse {
	p := pulse{At: time.Now(), Bars: []pulseBar{}, Active: []pulseActive{}}
	err := s.chRows(ctx, pulseBarsSQL, func(line []byte) error {
		var b pulseBar
		if err := json.Unmarshal(line, &b); err != nil {
			return err
		}
		b.T = chTime(b.T)
		p.Bars = append(p.Bars, b)
		return nil
	})
	if err == nil {
		known := s.knownSessions()
		err = s.chRows(ctx, pulseActiveSQL, func(line []byte) error {
			var a struct {
				pulseActive
				Cwd string `json:"cwd"`
			}
			if err := json.Unmarshal(line, &a); err != nil {
				return err
			}
			a.LastAt = chTime(a.LastAt)
			if a.Cwd != "" {
				a.Project = path.Base(a.Cwd)
			}
			if k, ok := known[a.SID]; ok { // первая реплика может быть старше окна: она — из датасета
				a.First = k.First
				if k.Project != "" {
					a.Project = k.Project
				}
			}
			p.Active = append(p.Active, a.pulseActive)
			return nil
		})
	}
	if err != nil {
		p.Bars, p.Active, p.Error = []pulseBar{}, []pulseActive{}, err.Error()
	}
	return p
}

// chRows runs a query over ClickHouse's HTTP interface and hands every JSONEachRow line to row.
// Timestamps come back in UTC whatever the server's time zone is.
func (s *server) chRows(ctx context.Context, sql string, row func([]byte) error) error {
	u := s.pulse.clickhouse + "/?" + url.Values{"database": {s.pulse.db}, "session_timezone": {"UTC"}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(sql))
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("ClickHouse недоступен: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 600))
		return fmt.Errorf("ClickHouse: %s", bytes.TrimSpace(b))
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		if line := bytes.TrimSpace(sc.Bytes()); len(line) > 0 {
			if err := row(line); err != nil {
				return err
			}
		}
	}
	return sc.Err()
}

type knownSession struct {
	Project string `json:"project"`
	First   string `json:"first"`
}

// knownSessions reads the project and the first prompt of the dataset's sessions; the builder
// has already redacted and cut them.
func (s *server) knownSessions() map[string]knownSession {
	var ds struct {
		Sessions []struct {
			ID string `json:"id"`
			knownSession
		} `json:"sessions"`
	}
	out := map[string]knownSession{}
	if b, err := os.ReadFile(filepath.Join(s.dataDir, "dataset.json")); err == nil && json.Unmarshal(b, &ds) == nil {
		for _, x := range ds.Sessions {
			out[x.ID] = x.knownSession
		}
	}
	return out
}

// chTime turns ClickHouse's "2026-10-01 06:58:10.000000000" (UTC) into RFC3339.
func chTime(s string) string {
	t, err := time.Parse("2006-01-02 15:04:05.999999999", s)
	if err != nil {
		return s
	}
	return t.UTC().Format(time.RFC3339)
}

// loopbackOnly is true for requests from this machine. TODO(merge): the same check is fromLoopback in
// sendstatus.go (PR /api/status); keep one when both are in camp.
func loopbackOnly(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	return err == nil && ip != nil && ip.IsLoopback()
}
