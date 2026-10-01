package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Дрейнер — фоновый отправщик: синглтон на flock, живёт, пока в спуле есть
// работа (плюс linger), и умирает. Следующее событие поднимет его снова.
// Никакого резидентного сервиса: буфер на диске переживает и перезагрузку,
// и краш, недоставленное уходит при следующем подъёме.

func runDrain(cfg Config) {
	if err := os.MkdirAll(cfg.SpoolDir, 0o700); err != nil {
		cfg.debugf("drain: спул недоступен: %v", err)
		return
	}
	lock, err := os.OpenFile(filepath.Join(cfg.SpoolDir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return
	}
	defer lock.Close()
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return // дрейнер уже работает
	}

	backoff := time.Second
	idleSince := time.Now()
	held := map[string]heldEntry{} // события Codex, ждущие своей записи в rollout
	for {
		if n := spoolEnforce(cfg); n > 0 {
			cfg.debugf("drain: буфер переполнен, выброшено старых событий: %d", n)
		}
		entries := spoolList(cfg)
		if len(entries) == 0 {
			if time.Since(idleSince) > time.Duration(cfg.LingerSec)*time.Second {
				return
			}
			time.Sleep(200 * time.Millisecond)
			continue
		}
		idleSince = time.Now()

		ready, wait := readyEntries(entries, held, idleSince)
		if len(ready) == 0 {
			time.Sleep(wait) // всё в спуле ждёт rollout: не крутим CPU
			continue
		}
		batch := ready
		if len(batch) > cfg.BatchSize {
			batch = batch[:cfg.BatchSize]
		}
		events := make([]event, 0, len(batch))
		paths := make([]string, 0, len(batch))
		for _, e := range batch {
			raw, err := os.ReadFile(e.path)
			if err != nil {
				continue
			}
			var ev event
			if json.Unmarshal(raw, &ev) != nil {
				if os.Remove(e.path) == nil {
					updateDeliveryState(cfg, []event{{}}, "dropped") // session ID cannot be recovered
				}
				continue
			}
			events = append(events, ev)
			paths = append(paths, e.path)
		}
		if len(events) == 0 {
			continue
		}

		// Обогащение из rollout. Не найденное у свежего события — не потеря,
		// а гонка с записью rollout: файл остаётся в спуле до следующего
		// круга, остальные события уходят без него.
		now := time.Now()
		outcomes := enrichEvents(cfg, events, now)
		send, sendPaths, changed := events[:0:0], paths[:0:0], []bool{}
		for i, ev := range events {
			if outcomes[i].hold {
				held[paths[i]] = held[paths[i]].next(ev, now)
				continue
			}
			delete(held, paths[i])
			send = append(send, ev)
			sendPaths = append(sendPaths, paths[i])
			changed = append(changed, outcomes[i].changed)
		}
		if len(send) == 0 {
			continue
		}

		if err := sendOTLP(cfg, buildOTLP(cfg, send)); err != nil {
			updateDeliveryState(cfg, send, "send_failure")
			// Итог обогащения сохраняется в спул: ретрай не перечитывает
			// rollout, который к тому времени мог уйти далеко вперёд.
			for i, ev := range send {
				if changed[i] {
					if err := spoolRewrite(sendPaths[i], ev); err != nil {
						cfg.debugf("drain: сохранить обогащение: %v", err)
					}
				}
			}
			cfg.debugf("drain: %v; пауза %s, в буфере %d", err, backoff, len(entries))
			time.Sleep(backoff)
			backoff *= 2
			if max := time.Duration(cfg.BackoffMaxSec) * time.Second; backoff > max {
				backoff = max
			}
			continue
		}
		updateDeliveryState(cfg, send, "collector_accept")
		backoff = time.Second
		for _, p := range sendPaths {
			_ = os.Remove(p)
		}
	}
}

// heldEntry — расписание повторного обогащения отложенного спул-файла:
// 250 мс, 500 мс, 1 с… но не позже конца окна ожидания события, после
// которого оно уходит как есть (enrich_status=not_found).
type heldEntry struct {
	at       time.Time
	attempts int
}

func (h heldEntry) next(ev event, now time.Time) heldEntry {
	at := now.Add(enrichRetryBase << min(h.attempts, 6))
	if deadline := time.Unix(0, ev.TS).Add(enrichGrace); at.After(deadline) {
		at = deadline
	}
	if !at.After(now) {
		at = now.Add(10 * time.Millisecond)
	}
	return heldEntry{at: at, attempts: h.attempts + 1}
}

// readyEntries — файлы спула, которые можно брать в батч сейчас; отложенные
// ждут своего времени и не задерживают остальных. wait — сколько спать, если
// готовых нет. Записи об исчезнувших файлах вычищаются.
func readyEntries(entries []spoolEntry, held map[string]heldEntry, now time.Time) ([]spoolEntry, time.Duration) {
	if len(held) == 0 {
		return entries, 0
	}
	ready := make([]spoolEntry, 0, len(entries))
	present := make(map[string]bool, len(held))
	wait := 200 * time.Millisecond
	for _, e := range entries {
		if h, ok := held[e.path]; ok {
			present[e.path] = true
			if d := h.at.Sub(now); d > 0 {
				wait = min(wait, max(d, 10*time.Millisecond))
				continue
			}
		}
		ready = append(ready, e)
	}
	for p := range held {
		if !present[p] {
			delete(held, p)
		}
	}
	return ready, wait
}
