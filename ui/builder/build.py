#!/usr/bin/env python3
"""Build the hottell UI v3 dataset from private local sources.

    python3 ui/builder/build.py [--ids ID,ID] [--sessions-root ~/.codex/sessions]
    # без --ids: все сессии, у которых есть отчёты в <reports> или тестовый разбор в <tests>
                                [--reports local-data/reports] [--tests local-data/tests]
                                [--out local-data/ui]

Writes <out>/dataset.json and <out>/sessions/<id>.json (files 0600, dirs 0700).
Telemetry comes from telemetry.py (raw Codex rollouts); task/outcome/checks/findings
come from the sibling conclusions.py when it is available. Standard library only.
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import re
import socket
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO = HERE.parent.parent
if str(HERE) not in sys.path:
    sys.path.insert(0, str(HERE))

import telemetry as T  # noqa: E402

DEFAULT_IDS: list[str] = []  # без --ids и без отчётов собирать нечего
_UUID = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")


def discover_ids(reports: Path, tests: Path) -> list[str]:
    """Сессии, по которым уже есть разбор: Deep 2.0, Deep v1, технический отчёт или тестовый разбор."""
    found = set()
    for d in (reports / "v2" / "deep", reports / "deep", reports / "telemetry"):
        if d.is_dir():
            found.update(p.stem for p in d.glob("*.json") if _UUID.match(p.stem))
    if tests.is_dir():
        found.update(p.name for p in tests.iterdir() if p.is_dir() and _UUID.match(p.name))
    return sorted(found) or list(DEFAULT_IDS)
# Session keys conclusions.py may set; everything else stays as counted from telemetry.
CONCLUSION_KEYS = {"title", "outcome", "outcome_basis", "tasks", "checks", "deep", "corrections", "sources"}


def warn(msg):
    print(f"предупреждение: {msg}", file=sys.stderr)


def find_rollout(root, sid):
    paths = sorted(Path(root).glob(f"**/rollout-*-{sid}.jsonl"))
    if not paths:
        return None
    if len(paths) > 1:
        warn(f"{sid}: найдено {len(paths)} rollout-файлов, беру самый свежий")
        paths.sort(key=lambda p: p.stat().st_mtime)
    return paths[-1]


def load_conclusions():
    try:
        import conclusions  # noqa: WPS433 — written by a sibling agent, optional
    except Exception as exc:  # ImportError or an error inside the module
        warn(f"conclusions.py недоступен ({exc.__class__.__name__}: {exc}); задания, проверки и выводы будут пустыми")
        return None
    return conclusions


def ensure_dir(path):
    path.mkdir(parents=True, exist_ok=True)
    os.chmod(path, 0o700)


def write_json(path, obj):
    """Atomic write with mode 0600 (the server may read while we write)."""
    fd, tmp = tempfile.mkstemp(prefix=f".{path.name}.", suffix=".tmp", dir=path.parent)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            json.dump(obj, f, ensure_ascii=False, separators=(",", ":"))
            f.write("\n")
            f.flush()
            os.fsync(f.fileno())
        os.replace(tmp, path)
    finally:
        if os.path.exists(tmp):
            os.unlink(tmp)
    os.chmod(path, 0o600)


def as_line(value):
    """Evidence line -> int (accepts 1273, '1273', 'L1273', 'local_transcript:L1273')."""
    if isinstance(value, bool):
        return None
    if isinstance(value, int):
        return value
    if isinstance(value, float) and value.is_integer():
        return int(value)
    if isinstance(value, str):
        m = re.search(r"(\d+)\s*$", value)
        return int(m.group(1)) if m else None
    return None


def fill_ev_at(items, parsed_by_sid, default_sid=None):
    for ev in items or []:
        if not isinstance(ev, dict):
            continue
        line = as_line(ev.get("line"))
        if line is not None:
            ev["line"] = line
        sid = ev.get("sid") or default_sid
        P = parsed_by_sid.get(sid)
        if ev.get("at") is None and P is not None and line is not None:
            ev["at"] = T.line_at(P, line)


def lines_at(P, lines):
    return [T.line_at(P, as_line(x)) for x in (lines or [])]


def fill_session_times(sess, P):
    """Timestamps for line references in conclusions-provided fields (additive: `at`, `start_at`, `end_at`)."""
    for chk in sess.get("checks") or []:
        if isinstance(chk, dict) and chk.get("lines"):
            chk["lines"] = [as_line(x) for x in chk["lines"] if as_line(x) is not None]
            chk["at"] = lines_at(P, chk["lines"])
    for task in sess.get("tasks") or []:
        if isinstance(task, dict):
            task["start_at"] = T.line_at(P, as_line(task.get("start_line")))
            task["end_at"] = T.line_at(P, as_line(task.get("end_line")))
    deep = sess.get("deep")
    if isinstance(deep, dict):
        for obs in deep.get("observations") or []:
            if isinstance(obs, dict) and obs.get("lines"):
                obs["lines"] = [as_line(x) for x in obs["lines"] if as_line(x) is not None]
                obs["at"] = lines_at(P, obs["lines"])


def merge_conclusions(core, c):
    sf = (c or {}).get("session_fields") or {}
    for key, value in sf.items():
        if key == "sources":
            core["sources"].update(value or {})
        elif key == "title":
            if value:
                core["title"] = value
        elif key in CONCLUSION_KEYS:
            core[key] = value
        elif core.get(key) is None:
            core[key] = value   # extend only; telemetry numbers are kept


def parse_args(argv):
    ap = argparse.ArgumentParser(description="Собрать датасет hottell UI v3 из локальных источников.")
    ap.add_argument("--ids", default="", help="id сессий через запятую; по умолчанию — все сессии с отчётами")
    ap.add_argument("--sessions-root", default="~/.codex/sessions")
    ap.add_argument("--reports", default=str(REPO / "local-data" / "reports"))
    ap.add_argument("--tests", default=str(REPO / "local-data" / "tests"))
    ap.add_argument("--out", default=str(REPO / "local-data" / "ui"))
    return ap.parse_args(argv)


def build(args):
    root = Path(os.path.expanduser(args.sessions_root))
    reports, tests, out = Path(args.reports).resolve(), Path(args.tests).resolve(), Path(args.out).resolve()
    ids = [x.strip() for x in args.ids.split(",") if x.strip()] or discover_ids(reports, tests)
    conc = load_conclusions()
    gaps = list(T.GENERAL_GAPS)
    if conc is None:
        gaps.append("conclusions.py недоступен: задания, исходы, 13 проверок, выводы и разметка коррекций не собраны.")

    # Все сессии сразу: общий реестр предложений и объединение карточек между сессиями.
    # Старый conclusions.py без load_all — по сессиям, как раньше.
    bulk = None
    if conc is not None and hasattr(conc, "load_all"):
        try:
            bulk = conc.load_all(ids, str(reports), str(tests))
        except Exception as exc:
            warn(f"conclusions.load_all упал ({exc.__class__.__name__}: {exc}); собираю выводы по сессиям")
            gaps.append(f"Общий сбор выводов не удался ({exc.__class__.__name__}): карточки собраны по сессиям.")
            bulk = None
        if bulk is not None:
            gaps.extend(g for g in (bulk.get("gaps") or []) if isinstance(g, str) and g.strip())

    parsed, sessions, timelines, findings = [], [], {}, []
    classes_by_sid = {}
    for sid in ids:
        path = find_rollout(root, sid)
        if path is None:
            warn(f"{sid}: rollout не найден в {root}")
            gaps.append(f"{T.short_id(sid)}: rollout не найден — сессия пропущена.")
            continue
        P = T.parse_rollout(path)
        P["sid"] = sid
        core = T.session_core(P)
        timeline = T.session_timeline(P)
        gaps.extend(T.session_gaps(P))
        c = None
        if bulk is not None:
            c = (bulk.get("sessions") or {}).get(sid)
        elif conc is not None:
            try:
                c = conc.load_session(sid, str(reports), str(tests))
            except Exception as exc:
                warn(f"{sid}: conclusions.load_session упал ({exc.__class__.__name__}: {exc})")
                gaps.append(f"{T.short_id(sid)}: выводы не загружены — {exc.__class__.__name__}.")
        classes = {}
        if c:
            merge_conclusions(core, c)
            for k, v in (c.get("human_turn_classes") or {}).items():
                try:
                    classes[int(k)] = v
                except (TypeError, ValueError):
                    continue
            if bulk is None:  # при load_all карточки берутся из общего списка ниже
                findings.extend(f for f in (c.get("findings") or []) if isinstance(f, dict))
            episodes = [e for e in (c.get("episodes") or []) if isinstance(e, dict)]
            for e in episodes:
                e["lines"] = [as_line(x) for x in e.get("lines") or [] if as_line(x) is not None]
                e["at"] = lines_at(P, e["lines"])
                fill_ev_at(e.get("evidence"), {sid: P}, sid)
            gaps.extend(f"{T.short_id(sid)}: {g}" for g in (c.get("gaps") or []) if isinstance(g, str) and g.strip())
            if episodes:
                timeline["episodes"] = episodes
        if classes:
            classes_by_sid[sid] = classes
            core["corrections"] = sum(1 for v in classes.values() if v == "correction")
            if core["corrections"] and "correction" not in core["flags"]:
                core["flags"].append("correction")
        elif not (c and isinstance((c.get("session_fields") or {}).get("corrections"), int)):
            core["corrections"] = None
        fill_session_times(core, P)
        parsed.append(P)
        sessions.append(core)
        timelines[sid] = timeline

    if bulk is not None:
        # Карточки с сессиями, rollout которых не найден, остаются: их ссылки без `at`.
        findings = [f for f in (bulk.get("findings") or []) if isinstance(f, dict)]
    by_sid = {P["sid"]: P for P in parsed}
    for f in findings:
        sids = f.get("sessions") or []
        fill_ev_at(f.get("ev"), by_sid, sids[0] if len(sids) == 1 else None)

    if parsed and not classes_by_sid:
        gaps.append("Разметки реплик (review.json) нет ни для одной сессии: corrections = null, "
                    "friction «correction» без данных, skills.corrections_after = 0.")
    elif parsed:
        missing = [T.short_id(P["sid"]) for P in parsed if P["sid"] not in classes_by_sid]
        if missing:
            gaps.append(f"Разметки реплик нет для {', '.join(missing)}: corrections = null, коррекции там не считаются.")

    catalog = []
    if conc is not None:
        try:
            catalog = conc.load_catalogue() or []
        except Exception as exc:
            warn(f"conclusions.load_catalogue упал ({exc.__class__.__name__}: {exc})")
            gaps.append("Каталог проверок не загружен.")

    starts = [s["start"] for s in sessions if s["start"]]
    ends = [s["end"] for s in sessions if s["end"]]
    dataset = {
        "schema_version": 1,
        "generated_at": T.iso(dt.datetime.now(dt.timezone.utc)),
        "host": socket.gethostname(),
        "window": {"from": min(starts) if starts else None, "to": max(ends) if ends else None},
        "pricing": T.pricing_for(parsed),
        "sessions": sessions,
        "tools": T.aggregate_tools(parsed),
        "mcp": T.aggregate_mcp(parsed),
        "commands": T.aggregate_commands(parsed, sessions),
        "permissions": T.aggregate_permissions(parsed),
        "friction": T.aggregate_friction(parsed, classes_by_sid),
        "skills": T.aggregate_skills(parsed, classes_by_sid),
        "findings": findings,
        "done": [],
        "checks_catalog": catalog,
        "gaps": list(dict.fromkeys(gaps)),
    }

    ensure_dir(out)
    ensure_dir(out / "sessions")
    for sid, timeline in timelines.items():
        write_json(out / "sessions" / f"{sid}.json", timeline)
    write_json(out / "dataset.json", dataset)
    return dataset, out


def main(argv=None):
    args = parse_args(sys.argv[1:] if argv is None else argv)
    dataset, out = build(args)
    print(f"hottell ui: {out / 'dataset.json'}")
    for s in dataset["sessions"]:
        cost = f"${s['cost_usd']:.2f}" if s["cost_usd"] is not None else "—"
        print(f"  {s['short']}  ходов {s['turns']}, реплик {s['prompts']}, вызовов {s['calls']} "
              f"(ошибок {s['errors']}, неизвестно {s['unknown_results']}), ответов модели {s['reqs']}, "
              f"оценка {cost}, флаги: {', '.join(s['flags']) or '—'}")
    print(f"  выводов {len(dataset['findings'])}, skills доступно {dataset['skills']['available']}, "
          f"активировано {sum(1 for r in dataset['skills']['rows'] if r['activations'])}, пробелов {len(dataset['gaps'])}")
    return 0 if dataset["sessions"] else 1


if __name__ == "__main__":
    sys.exit(main())
