package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Спул — буфер шиппера: один файл = одно событие, имя начинается с наносекундного
// таймстампа, так что лексикографический порядок = хронологический. Писатели
// (хуки) и читатель (дрейнер) не разделяют никаких локов: запись — temp+rename,
// чтение — только целые файлы, удаление — после подтверждённой отправки.

type spoolEntry struct {
	path string
	size int64
}

func spoolWrite(cfg Config, data []byte) error {
	if err := os.MkdirAll(cfg.SpoolDir, 0o700); err != nil {
		return err
	}
	var rnd [4]byte
	_, _ = rand.Read(rnd[:])
	name := fmt.Sprintf("%020d-%s.json", time.Now().UnixNano(), hex.EncodeToString(rnd[:]))
	tmp := filepath.Join(cfg.SpoolDir, "."+name+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(cfg.SpoolDir, name))
}

// spoolList — события от старых к новым; временные и чужие файлы не считаются.
func spoolList(cfg Config) []spoolEntry {
	dirents, err := os.ReadDir(cfg.SpoolDir)
	if err != nil {
		return nil
	}
	var out []spoolEntry
	for _, de := range dirents {
		if de.IsDir() || filepath.Ext(de.Name()) != ".json" {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		out = append(out, spoolEntry{path: filepath.Join(cfg.SpoolDir, de.Name()), size: info.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

// spoolEnforce — политика переполнения буфера: лишнее сверх лимитов дропается
// с головы (старые события), свежее ценнее. Возвращает число выброшенных.
func spoolEnforce(cfg Config) int {
	entries := spoolList(cfg)
	var total int64
	for _, e := range entries {
		total += e.size
	}
	maxBytes := int64(cfg.BufferMaxMB) * 1024 * 1024
	dropped := 0
	var droppedEvents []event
	for i := 0; i < len(entries); i++ {
		overCount := len(entries)-i > cfg.BufferMaxEvents
		overSize := maxBytes > 0 && total > maxBytes
		if !overCount && !overSize {
			break
		}
		var ev event
		raw, readErr := os.ReadFile(entries[i].path)
		if readErr != nil || json.Unmarshal(raw, &ev) != nil {
			ev = event{}
		}
		if os.Remove(entries[i].path) == nil {
			dropped++
			droppedEvents = append(droppedEvents, ev)
		}
		total -= entries[i].size
	}
	if len(droppedEvents) > 0 {
		updateDeliveryState(cfg, droppedEvents, "dropped")
	}
	return dropped
}

// spoolRewrite — дрейнер (единственный, кто трогает уже записанные файлы)
// сохраняет изменённое событие на место: temp+rename, как при записи.
func spoolRewrite(path string, ev event) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".rewrite-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err = tmp.Chmod(0o600); err == nil {
		_, err = tmp.Write(data)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
