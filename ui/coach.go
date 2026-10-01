package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
)

// Журнал hottell-coach для раздела «Исправлено»: тот же файл, что пишет локальный MCP
// hottell-local (coach_journal), только чтение. Проверки (check_of) сворачиваются в
// исходную запись — так же, как fold в temp/internal/hottell/local/coach/journal.go:
// действует последняя проверка, checks — сколько их было. Файл читается целиком, без
// предела длины строки. coach_journal дописывает запись вместе с "\n" за один write,
// поэтому последняя строка без "\n" — запись, которая ещё пишется: её пропускаем и
// битой не считаем.

func readCoachJournal(path string) (entries []map[string]any, broken int, err error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	lines := bytes.Split(b, []byte("\n"))
	lines = lines[:len(lines)-1] // после последнего "\n": пусто или недописанная запись
	idx := map[string]map[string]any{}
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		if json.Unmarshal(line, &m) != nil || m == nil { // `null` разбирается без ошибки, но это не запись
			broken++
			continue
		}
		if of, _ := m["check_of"].(string); of != "" {
			if o := idx[of]; o != nil {
				n, _ := o["checks"].(int)
				o["result"], o["observations"], o["repeats"], o["checked_at"], o["checks"] = m["result"], m["observations"], m["repeats"], m["at"], n+1
			}
			continue
		}
		id, _ := m["id"].(string)
		idx[id] = m
		entries = append(entries, m)
	}
	return entries, broken, nil
}

func (s *server) coachJournal(w http.ResponseWriter, r *http.Request) {
	entries, broken, err := readCoachJournal(s.journal)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if entries == nil {
		entries = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "broken_lines": broken})
}
