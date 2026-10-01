"""Выводы для hottell UI v3: задания, 13 проверок и карточки «Что исправить».

Модуль читает только готовые разборы и ничего не додумывает:

* Deep v2 — ``<reports>/v2/deep/<id>.json``, затем ``<reports>/rebuild/<id>/deep.v2.json``
  (принимается только ``kind: deep``, ``schema_version: 2`` и тот же ``session_id``);
* Deep v1 — ``<reports>/deep/<id>.json``;
* покрытие проверок — ``<reports>/coverage/<id>.json`` (если в Deep v2 нет ``checks``);
* тестовый разбор — ``<tests>/<id>/review.json`` (классы реплик, эпизоды и предложения).

Отсутствующее значение — ``None``. Каждая карточка несёт источник (``source``), язык
(``lang``) и четыре независимые оси: готовность, решение, исполнение, эффект.
Эпизод тестового разбора сам по себе карточку не создаёт — он только добавляет
доказательства к карточке с тем же паттерном.

Английский текст разборов показывается по-русски только через приватный словарь
``<reports>/../ui/translations.ru.json`` вида ``{sha256(английский текст): русский}``.
Нет перевода — текст остаётся как есть, ``lang: "en"``. Пути, идентификаторы, код
и команды не переводятся.

Только стандартная библиотека Python 3.
"""

from __future__ import annotations

import hashlib
import json
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
if HERE not in sys.path:
    sys.path.insert(0, HERE)

import telemetry as T  # noqa: E402 — короткий id сессии одинаков во всех сборщиках

CATALOGUE_PATH = os.path.join(HERE, "catalogue.json")

CHECK_IDS = ("D01", "D03", "D05", "D07", "D10", "D12", "D13", "D16", "D19", "D22", "D24", "D25", "D26")

_UUID = r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}"
# `local_transcript:L1273`, `L1273`, `#1273`, `<uuid>:L12`, `L1-L5859`, `L1-L5859: token_count scan`.
# Для диапазона берётся первая строка.
_REF = re.compile(rf"^\s*(?:(?P<sid>{_UUID})\s*:\s*|local_transcript\s*:\s*)?[L#](?P<line>\d+)", re.I)
_CYR = re.compile("[А-Яа-яЁё]")
_LAT = re.compile("[A-Za-z]")
_DCODE = re.compile(r"(?<![A-Za-z0-9])D\d{2}(?!\d)")

# Deep v2 (analytics/v2_contract.py) → контракт UI.
_READINESS = {
    "hypothesis": "hypothesis",
    "needs_specification": "needs_spec",
    "needs_spec": "needs_spec",
    "prepared_verified": "prepared",
    "prepared": "prepared",
}
_DECISIONS = {"not_requested", "accepted", "rejected", "revision_requested"}
_EXECUTIONS = {"not_applied", "applied"}
_EFFECTS = {"not_measured", "helped", "no_effect", "worse", "insufficient_data"}
# change_type Deep v2 → kind карточки. В плане 2.0 «скрипт или workflow» — один способ
# изменения, поэтому workflow → script. Настройка, продукт и диагностика → diagnostic
# («диагностика, точечная настройка или техническая правка»). Исходный change_type
# сохраняется в карточке отдельным полем.
_KIND = {
    "personalization": "personalization",
    "project_rule": "project_rule",
    "skill": "skill",
    "hook": "hook",
    "script": "script",
    "workflow": "script",
    "automation": "automation",
    "settings": "diagnostic",
    "product": "diagnostic",
    "diagnosis": "diagnostic",
    "diagnostic": "diagnostic",
    "habit": "habit",
}
_OUTCOME_RU = (("verified", "подтверждено"), ("partial", "частично"), ("failed", "не удалось"), ("unknown", "неизвестно"))


# ---------------------------------------------------------------- catalogue

def load_catalogue() -> list[dict]:
    """13 проверок каталога: ``[{"id", "name", "how"}]`` в порядке Deep v2."""
    with open(CATALOGUE_PATH, encoding="utf-8") as fh:
        data = json.load(fh)
    items = data["checks"] if isinstance(data, dict) else data
    return [{"id": c["id"], "name": c["name"], "how": c["how"]} for c in items]


# ---------------------------------------------------------------- evidence refs

def normalize_ref(ref) -> tuple[str | None, int | None]:
    """Ссылка на доказательство → ``(session_id | None, строка сырого rollout | None)``.

    * строки: ``local_transcript:L1273``, ``L1273``, ``#1273``, ``<uuid>:L12`` → 1273 / 12;
      диапазон ``L1-L5859`` → первая строка (1);
    * события тестового разбора ``{"kind": "event", "id": "1218"}``: id — это
      source_ordinal (с 0), поэтому строка = id + 1 → 1219;
    * ``{"line": 12}`` и целое число считаются уже номером строки.
    Нераспознанная ссылка → ``(None, None)``.
    """
    if isinstance(ref, bool) or ref is None:
        return None, None
    if isinstance(ref, int):
        return (None, ref) if ref >= 1 else (None, None)
    if isinstance(ref, dict):
        sid = ref.get("sid") or ref.get("session_id")
        line = ref.get("line")
        if isinstance(line, int) and not isinstance(line, bool):
            return (sid, line) if line >= 1 else (None, None)
        rid = ref.get("id")
        if ref.get("kind") == "event" and rid is not None and str(rid).strip().isdigit():
            return sid, int(str(rid).strip()) + 1
        if isinstance(rid, str):
            found_sid, found_line = normalize_ref(rid)
            return (found_sid or sid, found_line) if found_line else (None, None)
        return None, None
    if isinstance(ref, str):
        m = _REF.match(ref)
        if m:
            line = int(m.group("line"))
            if line >= 1:
                sid = m.group("sid")
                return (sid.lower() if sid else None), line
    return None, None


def ref_lines(refs, session_id: str) -> list[int]:
    """Номера строк этой сессии (ссылки на другие сессии отбрасываются), по возрастанию."""
    lines = set()
    for ref in refs or []:
        sid, line = normalize_ref(ref)
        if line and (sid is None or sid == session_id):
            lines.add(line)
    return sorted(lines)


# ---------------------------------------------------------------- translation

def text_key(text: str) -> str:
    """Ключ словаря переводов: sha256 точного английского текста (UTF-8)."""
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


def translation_path(reports_dir: str) -> str:
    return os.path.join(os.path.dirname(os.path.abspath(reports_dir)), "ui", "translations.ru.json")


def load_translations(path: str) -> dict:
    try:
        with open(path, encoding="utf-8") as fh:
            data = json.load(fh)
    except (OSError, ValueError):
        return {}
    if not isinstance(data, dict):
        return {}
    return {k: v for k, v in data.items() if isinstance(k, str) and isinstance(v, str) and v.strip()}


def _is_english(text: str) -> bool:
    """Латиница без кириллицы и хотя бы два слова; одиночный токен (slug, путь, id) — не проза."""
    return bool(_LAT.search(text)) and not _CYR.search(text) and len(text.split()) >= 2


class _Translator:
    """Переводит строку по словарю и запоминает, что осталось без перевода."""

    def __init__(self, mapping: dict):
        self.mapping = mapping
        self.missing: list[str] = []
        self.translated = 0

    def __call__(self, text, record: bool = True):
        """→ ``(текст, lang)``; lang: ``ru`` | ``ru-translated`` | ``en`` | ``None`` (пусто/идентификатор).

        ``record=False`` — не считать отсутствие перевода пробелом (адреса правок).
        """
        if not isinstance(text, str) or not text.strip():
            return None, None
        if not _is_english(text):
            return text, ("ru" if _CYR.search(text) else None)  # None: идентификатор, не влияет на lang
        ru = self.mapping.get(text_key(text))
        if ru:
            self.translated += 1
            return ru, "ru-translated"
        if record and text not in self.missing:
            self.missing.append(text)
        return text, "en"

    def many(self, items):
        out, langs = [], []
        for item in items or []:
            text, lang = self(item)
            if text is not None:
                out.append(text)
                langs.append(lang)
        return out, langs


def merge_lang(langs) -> str:
    """Худший язык набора: есть английский → en; есть перевод → ru-translated; иначе ru."""
    langs = [lang for lang in langs if lang]
    if "en" in langs:
        return "en"
    if "ru-translated" in langs:
        return "ru-translated"
    return "ru"


# ---------------------------------------------------------------- helpers

def _read_json(path: str, gaps: list, label: str):
    if not os.path.isfile(path):
        return None
    try:
        with open(path, encoding="utf-8") as fh:
            return json.load(fh)
    except (OSError, ValueError) as exc:
        gaps.append(f"{label}: файл не прочитан ({type(exc).__name__})")
        return None


def _int(value):
    return value if isinstance(value, int) and not isinstance(value, bool) else None


def _str(value):
    return value if isinstance(value, str) and value.strip() else None


def _codes(pattern) -> set:
    return set(_DCODE.findall(pattern)) if isinstance(pattern, str) else set()


def _pattern_match(a, b) -> bool:
    """Один паттерн: точное совпадение или общий код проверки (D12 ~ D12_project_boundary)."""
    if not isinstance(a, str) or not isinstance(b, str):
        return False
    return a == b or bool(_codes(a) & _codes(b))


def _plural_corrections(n: int) -> str:
    if n % 10 == 1 and n % 100 != 11:
        return f"{n} коррекция"
    if n % 10 in (2, 3, 4) and n % 100 not in (12, 13, 14):
        return f"{n} коррекции"
    return f"{n} коррекций"


def _lower_first(text: str) -> str:
    """«Результат …» → «результат …» после двоеточия; аббревиатуры (AI, MCP) не трогаем."""
    if len(text) > 1 and text[0].isupper() and text[1].islower():
        return text[0].lower() + text[1:]
    return text


def _is_prose_locator(text) -> bool:
    """Адрес правки — проза, а не путь/идентификатор: ≥ 3 слов и нет «/»."""
    return isinstance(text, str) and _is_english(text) and "/" not in text and len(text.split()) >= 3


def _short(text, limit: int = 140):
    if not isinstance(text, str):
        return None
    text = " ".join(text.split())
    return text if len(text) <= limit else text[: limit - 1].rstrip() + "…"


def _find_v2(session_id: str, reports_dir: str, gaps: list):
    for path in (
        os.path.join(reports_dir, "v2", "deep", f"{session_id}.json"),
        os.path.join(reports_dir, "rebuild", session_id, "deep.v2.json"),
    ):
        data = _read_json(path, gaps, "Deep v2")
        if data is None:
            continue
        if isinstance(data, dict) and data.get("kind") == "deep" and data.get("schema_version") == 2 and data.get("session_id") == session_id:
            return data
        gaps.append("Deep v2: файл не соответствует схеме (kind/schema_version/session_id) и пропущен")
    return None


# ---------------------------------------------------------------- severity
# Правило sev карточки (одно для всех источников):
#   bad  — паттерн подтверждён в том же разборе: наблюдение этого паттерна или проверка
#          со статусом confirmed. Только подтверждение делает проблему «красной»;
#   warn — паттерн подозревается (проверка suspected, подозреваемое наблюдение, эпизод
#          тестового разбора) или источник ставит ему priority/severity high/medium —
#          важность без подтверждения остаётся гипотезой;
#   info — всё остальное, в том числе все черновики Deep v1: у них нет паттерна,
#          подтверждения и оценки важности.

def _severity(pattern_id, observations, checks, priority, episodes) -> str:
    codes = _codes(pattern_id)
    confirmed = any(o.get("status") == "confirmed" and _pattern_match(pattern_id, o.get("pattern")) for o in observations)
    confirmed = confirmed or any(c.get("status") == "confirmed" and c.get("id") in codes for c in checks)
    if confirmed:
        return "bad"
    suspected = any(c.get("status") == "suspected" and c.get("id") in codes for c in checks)
    suspected = suspected or any(o.get("status") == "suspected" and _pattern_match(pattern_id, o.get("pattern")) for o in observations)
    important = priority in ("high", "medium") or any(e.get("severity") in ("high", "medium") for e in episodes)
    if suspected or episodes or important:
        return "warn"
    return "info"


# ---------------------------------------------------------------- sections

def _tasks_v2(v2, tr):
    out = []
    for t in v2.get("tasks") or []:
        if not isinstance(t, dict):
            continue
        goal, lang = tr(t.get("goal"))
        out.append({
            "task_id": t.get("task_id"),
            "goal": goal,
            "outcome": t.get("outcome"),
            "start_line": _int(t.get("start_line")),
            "end_line": _int(t.get("end_line")),
            "lang": merge_lang([lang]),
        })
    return out


def _checks(items, session_id, tr):
    by_id, order = {}, []
    for c in items or []:
        if not isinstance(c, dict) or not c.get("id") or c["id"] in by_id:
            continue
        summary, l1 = tr(c.get("summary"))
        missing, l2 = tr.many(c.get("missing_data"))
        by_id[c["id"]] = {
            "id": c["id"],
            "status": c.get("status"),
            "summary": summary,
            "lines": ref_lines(c.get("evidence"), session_id),
            "missing_data": missing,
            "task_ids": [x for x in c.get("task_ids") or [] if isinstance(x, str)],
            "lang": merge_lang([l1, *l2]),
        }
        order.append(c["id"])
    if not by_id:
        return []
    # Проверка каталога, которой нет в источнике, — «нет данных» (status None), а не выдуманный статус.
    for cid in CHECK_IDS:
        if cid not in by_id:
            by_id[cid] = {"id": cid, "status": None, "summary": None, "lines": [], "missing_data": [], "task_ids": [], "lang": "ru"}
            order.append(cid)
    rank = {cid: i for i, cid in enumerate(CHECK_IDS)}
    return [by_id[cid] for cid in sorted(order, key=lambda x: (rank.get(x, 99), x))]


def _observations(items, session_id, tr, with_tasks: bool):
    out = []
    for o in items or []:
        if not isinstance(o, dict):
            continue
        finding, lang = tr(o.get("finding"))
        pattern = o.get("pattern")
        if isinstance(pattern, str) and _CYR.search(pattern) is None and " " in pattern.strip():
            pattern, plang = tr(pattern)  # v1: прозаическое название паттерна
            lang = merge_lang([lang, plang])
        row = {"pattern": pattern, "finding": finding, "status": o.get("status"), "lines": ref_lines(o.get("evidence"), session_id)}
        if with_tasks:
            row["task_ids"] = [x for x in o.get("task_ids") or [] if isinstance(x, str)]
        row["lang"] = merge_lang([lang])
        out.append(row)
    return out


def _episodes(review, session_id):
    episodes = []
    for ep in (review or {}).get("new_episodes") or []:
        if not isinstance(ep, dict) or not ep.get("pattern_id"):
            continue
        evidence = []
        for item in ep.get("evidence") or []:
            sid, line = normalize_ref(item)
            if line and (sid is None or sid == session_id):
                note = item.get("note") if isinstance(item, dict) else None
                evidence.append({"line": line, "note": _str(note)})
        span = ep.get("span") if isinstance(ep.get("span"), dict) else {}
        episodes.append({
            "pattern_id": ep["pattern_id"],
            "lines": sorted({e["line"] for e in evidence}),
            "text": _str(ep.get("explanation")),
            "status": ep.get("status"),
            "severity": ep.get("severity"),
            "turn_from": _int(span.get("turn_from")),
            "turn_to": _int(span.get("turn_to")),
            "evidence": evidence,
            "attached_to": [],
        })
    return episodes


def _impact(groups):
    """Импакт — только счётный факт из разметки: коррекции человека внутри эпизодов.

    ``groups``: ``[(метка сессии | None, эпизоды, классы реплик этой сессии)]``.
    """
    total, parts = 0, []
    for label, episodes, turn_classes in groups:
        if not episodes or not turn_classes:
            continue
        for ep in episodes:
            a, b = ep.get("turn_from"), ep.get("turn_to")
            if a is None or b is None:
                continue
            n = sum(1 for i in range(a, b + 1) if turn_classes.get(i) == "correction")
            if n:
                total += n
                parts.append((f"{label} · " if label else "") + f"{ep['pattern_id']}, реплики {a}–{b}")
    if not total:
        return None
    head = "в эпизоде " if len(parts) == 1 else "в эпизодах "
    return {"value": _plural_corrections(total), "label": head + "; ".join(parts) + " (разметка тестового разбора)"}


def _episode_impact(episodes, turn_classes):
    return _impact([(None, episodes, turn_classes)])


def _impact_count(impact) -> int:
    m = re.match(r"\s*(\d+)", (impact or {}).get("value") or "")
    return int(m.group(1)) if m else 0


def _add_ev(ev: list, index: dict, sid: str, line: int, text, src: str):
    key = (sid, line)
    if key in index:
        item = index[key]
        if src not in item["src"].split(", "):
            item["src"] = item["src"] + ", " + src
        if src == "test-review" and text:
            item["text"] = text  # заметка эпизода конкретнее ссылки на задание
        return
    item = {"sid": sid, "line": line, "at": None, "text": text, "src": src}
    index[key] = item
    ev.append(item)


def _sort_ev(finding):
    """Сначала строки сессий карточки в их порядке, затем чужие ссылки; внутри — по строке."""
    order = {sid: i for i, sid in enumerate(finding.get("sessions") or [])}
    finding["ev"].sort(key=lambda e: (order.get(e["sid"], len(order)), e["sid"] or "", e["line"]))


def _attach_episodes(finding, episodes, session_id):
    idx = {(e["sid"], e["line"]): e for e in finding["ev"]}
    for ep in episodes:
        for e in ep["evidence"]:
            note = f"Эпизод {ep['pattern_id']}: {e['note']}" if e["note"] else f"Эпизод {ep['pattern_id']} (тестовый разбор)"
            _add_ev(finding["ev"], idx, session_id, e["line"], note, "test-review")
        if finding["id"] not in ep["attached_to"]:
            ep["attached_to"].append(finding["id"])
    _sort_ev(finding)


def _v2_label(source, ssid, ctxs) -> str:
    """Текст доказательства Deep v2: задание, в котором строка названа основанием."""
    task_id = source.get("task_id")
    goal = ((ctxs.get(ssid) or {}).get("tasks_by_id") or {}).get(task_id, {}).get("goal")
    if task_id and goal:
        return f"Deep v2 · {task_id}: {_short(goal)}"
    return f"Deep v2 · {task_id or 'задание не указано'}"


def _proposal_cards(proposals, ctxs: dict, tr, default_sid, registry: bool = False):
    """Карточки из предложений Deep v2 (кандидаты одной сессии или общий реестр).

    ``ctxs`` — контексты загруженных сессий (задания, наблюдения, проверки, эпизоды,
    классы реплик). Предложение может ссылаться на несколько сессий: доказательства
    берутся из всех источников, sev считается по наблюдениям и проверкам всех
    загруженных сессий-источников, эпизоды прикрепляются в каждой такой сессии.
    Одинаковые ``group_key``/``proposal_id`` дают одну карточку.
    → ``(карточки, {id: {session_id: строки источников}})``.
    """
    findings, seen, own_lines = [], {}, {}
    for p in proposals or []:
        if not isinstance(p, dict):
            continue
        keys = [k for k in (p.get("group_key"), p.get("proposal_id")) if _str(k)]
        dup = next((seen[k] for k in keys if k in seen), None)
        sources = [s for s in p.get("sources") or [] if isinstance(s, dict)]
        if dup is not None:  # тот же group_key/proposal_id: одна карточка, основания объединяются
            idx = {(e["sid"], e["line"]): e for e in dup["ev"]}
            for s in sources:
                ssid = s.get("session_id") or default_sid
                for ref in s.get("evidence") or []:
                    sid, line = normalize_ref(ref)
                    if line:
                        _add_ev(dup["ev"], idx, sid or ssid, line, _v2_label(s, ssid, ctxs), "deep-v2")
                if ssid and ssid not in dup["sessions"]:
                    dup["sessions"].append(ssid)
                if ssid:
                    own_lines[dup["id"]].setdefault(ssid, set()).update(ref_lines(s.get("evidence"), ssid))
            _sort_ev(dup)
            continue

        langs = []

        def t(value):
            text, lang = tr(value)
            langs.append(lang)
            return text

        def tl(values):
            texts, ls = tr.many(values)
            langs.extend(ls)
            return texts

        title = t(p.get("action"))
        observed = t(p.get("observed"))
        cause = t(p.get("cause"))
        what = " ".join(x for x in (observed, f"Вероятная причина: {_lower_first(cause)}" if cause else None) if x) or None

        target = p.get("target") if isinstance(p.get("target"), dict) else {}
        locator = _str(target.get("locator"))
        # Путь или идентификатор не переводится; прозаический адрес — через словарь, как остальной текст.
        where, where_lang = tr(locator, record=_is_prose_locator(locator))
        if where_lang == "ru-translated" or _is_prose_locator(locator):
            langs.append(where_lang)

        ev, idx, sessions, lines_by_sid = [], {}, [], {}
        for s in sources:
            ssid = s.get("session_id") or default_sid
            if ssid and ssid not in sessions:
                sessions.append(ssid)
            label = _v2_label(s, ssid, ctxs)
            for ref in s.get("evidence") or []:
                sid, line = normalize_ref(ref)
                if line:
                    _add_ev(ev, idx, sid or ssid, line, label, "deep-v2")
            if ssid:
                lines_by_sid.setdefault(ssid, set()).update(ref_lines(s.get("evidence"), ssid))
        if not sessions and default_sid:
            sessions = [default_sid]

        rule = p.get("existing_rule") if isinstance(p.get("existing_rule"), dict) else {}
        readiness = p.get("readiness") if isinstance(p.get("readiness"), dict) else {}
        decision = (p.get("decision") or {}).get("status") if isinstance(p.get("decision"), dict) else None
        execution = (p.get("execution") or {}).get("status") if isinstance(p.get("execution"), dict) else None
        effect = (p.get("effect") or {}).get("status") if isinstance(p.get("effect"), dict) else None
        pid = _str(p.get("proposal_id")) or _str(p.get("group_key")) or "p2:" + text_key(json.dumps([sessions, p.get("action"), p.get("change")], ensure_ascii=False))[:20]
        pattern_id = _str(p.get("pattern_id"))

        finding = {
            "id": pid,
            "sev": "info",
            "title": title,
            "what": what,
            "ev": ev,
            "impact": None,
            "where": where,
            "snip": t(p.get("change")),
            "kind": _KIND.get(p.get("change_type"), "diagnostic"),
            "pattern_id": pattern_id,
            "readiness": _READINESS.get(readiness.get("status"), "hypothesis"),
            "decision": decision if decision in _DECISIONS else "not_requested",
            "execution": execution if execution in _EXECUTIONS else "not_applied",
            "effect": effect if effect in _EFFECTS else "not_measured",
            "source": "deep-v2",
            "lang": "ru",
            "sessions": sessions,
            "cause": cause,
            "alternative_causes": tl(p.get("alternative_causes")),
            "preconditions": tl(p.get("preconditions")),
            "exceptions": tl(p.get("exceptions")),
            "verification": t(p.get("verification")),
            "rollback": t(p.get("rollback")),
            "expected_effect": t(p.get("expected_effect")),
            # Дополнительно к контракту — для проверки машинной готовности.
            "group_key": _str(p.get("group_key")),
            "change_type": p.get("change_type"),
            "priority": p.get("priority"),
            "scope": t(p.get("scope")),
            "selection_reason": t(p.get("selection_reason")),
            "readiness_reason": t(readiness.get("reason")),
            "existing_rule": {
                "status": rule.get("status"),
                "description": t(rule.get("description")),
                "locator": _str(rule.get("locator")),
                "version": _str(rule.get("version")),
            } if rule else None,
            "target": {"kind": _str(target.get("kind")), "locator": locator, "version": _str(target.get("version"))},
            "history_review": t(p.get("history_review")),
            "analysis_versions": [x for x in p.get("analysis_versions") or [] if isinstance(x, str)],
            "replaces": [],
        }
        if registry:
            finding["registry"] = True

        groups, matched_all, observations, checks = [], [], [], []
        loaded = [sid for sid in sessions if sid in ctxs]
        for sid in loaded:
            ctx = ctxs[sid]
            observations += ctx.get("observations") or []
            checks += ctx.get("checks") or []
            matched = [ep for ep in ctx.get("episodes") or [] if pattern_id and _pattern_match(ep["pattern_id"], pattern_id)]
            if matched:
                _attach_episodes(finding, matched, sid)
                matched_all += matched
                groups.append((T.short_id(sid) if len(loaded) > 1 else None, matched, ctx.get("turn_classes") or {}))
        _sort_ev(finding)
        finding["impact"] = _impact(groups)
        finding["sev"] = _severity(pattern_id, observations, checks, p.get("priority"), matched_all)
        finding["lang"] = merge_lang(langs)
        findings.append(finding)
        own_lines[pid] = lines_by_sid
        for k in keys:
            seen[k] = finding
    return findings, own_lines


def _v2_findings(ctx, tr):
    """Кандидаты одной сессии → карточки и строки их оснований в этой сессии (для черновиков v1)."""
    sid = ctx["sid"]
    findings, own = _proposal_cards(ctx["v2"].get("proposal_candidates"), {sid: ctx}, tr, sid)
    return findings, {pid: by_sid.get(sid, set()) for pid, by_sid in own.items()}


def _infer_kind_v1(target) -> str:
    """Осторожно: тип только по явному объекту правки, иначе diagnostic."""
    text = (target or "").lower()
    if "agents.md" in text or "claude.md" in text:
        return "project_rule"
    if "skill.md" in text or re.search(r"\bskills?\b", text):
        return "skill"
    if re.search(r"\bhooks?\b", text):
        return "hook"
    return "diagnostic"


def _v1_findings(v1, session_id, tr, v1_observations, v2_findings, v2_lines, episodes):
    findings, seen = [], {}
    for r in v1.get("recommendations") or []:
        if not isinstance(r, dict) or not _str(r.get("action")):
            continue
        key = "v1:" + text_key(json.dumps([r.get("action"), r.get("target"), r.get("change")], ensure_ascii=False))[:20]
        refs = [normalize_ref(x) for x in r.get("evidence") or []]
        refs = [(sid or session_id, line) for sid, line in refs if line]
        if key in seen:
            idx = {(e["sid"], e["line"]): e for e in seen[key]["ev"]}
            for sid, line in refs:
                _add_ev(seen[key]["ev"], idx, sid, line, "Deep v1 · ссылка черновика", "deep-v1")
            continue
        own = {line for sid, line in refs if sid == session_id}

        langs = []

        def t(value):
            text, lang = tr(value)
            langs.append(lang)
            return text

        title = t(r.get("action"))
        # Черновик, уже пересмотренный Deep v2: основания в основном совпадают с карточкой v2.
        superseded = None
        for f in v2_findings:
            overlap = own & v2_lines.get(f["id"], set())
            if own and len(overlap) >= 2 and 2 * len(overlap) >= len(own):
                superseded = f
                break
        if superseded is not None:
            superseded["replaces"].append({"id": key, "source": "deep-v1", "title": title, "shared_lines": sorted(own & v2_lines[superseded["id"]])})
            continue

        cited = {}
        for o in v1_observations:
            n = len(own & set(o["lines"]))
            if n and o.get("pattern"):
                cited[o["pattern"]] = max(n, cited.get(o["pattern"], 0))
        names = [name for name, _ in sorted(cited.items(), key=lambda kv: -kv[1])[:3]]
        what = "В черновике Deep v1 наблюдение и причина не записаны."
        if names:
            what += " Наблюдения Deep v1 с теми же строками: " + ", ".join(f"«{n}»" for n in names) + "."

        ev, idx = [], {}
        for sid, line in refs:
            label = "Deep v1 · ссылка черновика"
            if sid == session_id:
                hits = [o["pattern"] for o in v1_observations if line in o["lines"] and o.get("pattern")]
                if hits:
                    label = f"Deep v1 · {hits[0]}"
            _add_ev(ev, idx, sid, line, label, "deep-v1")
        ev.sort(key=lambda e: (e["sid"] != session_id, e["sid"] or "", e["line"]))

        target = _str(r.get("target"))
        codes = _codes(" ".join(x for x in (r.get("action"), target) if isinstance(x, str)))
        pattern_id = next(iter(codes)) if len(codes) == 1 else None
        finding = {
            "id": key,
            "sev": "info",  # черновик без подтверждения — см. правило sev выше
            "title": title,
            "what": what,
            "ev": ev,
            "impact": None,
            "where": t(target),
            "snip": t(r.get("change")),
            "kind": _infer_kind_v1(target),
            "pattern_id": pattern_id,
            "readiness": "hypothesis",  # рекомендации v1 — только черновики
            "decision": "not_requested",
            "execution": "not_applied",
            "effect": "not_measured",
            "source": "deep-v1",
            "lang": "ru",
            "sessions": [session_id],
            "cause": None,
            "alternative_causes": [],
            "preconditions": [],
            "exceptions": [],
            "verification": None,
            "rollback": None,
            "expected_effect": None,
            "v1_status": r.get("status"),
        }
        matched = [ep for ep in episodes if pattern_id and _pattern_match(ep["pattern_id"], pattern_id)]
        if matched:
            _attach_episodes(finding, matched, session_id)
        finding["lang"] = merge_lang(langs)
        findings.append(finding)
        seen[key] = finding
    return findings


_REVIEW_KIND = {"skill": "skill", "policy": "project_rule", "project_rule": "project_rule", "hook": "hook",
                "script": "script", "automation": "automation", "personalization": "personalization"}


def _first_sentence(text) -> str | None:
    text = _str(text)
    if not text:
        return None
    head = re.split(r"(?<=[.;:])\s", text, maxsplit=1)[0].rstrip(".;:")
    return _short(head, 120)


def _review_findings(review, session_id, existing, observations, checks, episodes, turn_classes):
    """Предложения тестового разбора (``review.json`` → ``proposals``).

    Если в этой сессии уже есть карточка Deep с тем же паттерном, предложение не создаёт
    дубль, а добавляет к ней свои основания. Иначе — отдельная карточка-гипотеза: основание
    одной сессии не делает её готовой к применению.
    """
    findings = []
    for p in (review or {}).get("proposals") or []:
        if not isinstance(p, dict):
            continue
        pattern_id = _str(p.get("pattern_id"))
        refs = [(sid or session_id, line) for sid, line in (normalize_ref(x) for x in p.get("evidence") or []) if line]
        same = next((f for f in existing if pattern_id and f.get("pattern_id") and _pattern_match(f["pattern_id"], pattern_id)
                     and session_id in f.get("sessions", [])), None)
        if same is not None:
            idx = {(e["sid"], e["line"]): e for e in same["ev"]}
            for sid, line in refs:
                _add_ev(same["ev"], idx, sid, line, "Тестовый разбор · основание предложения", "test-review")
            _sort_ev(same)
            continue
        should_be, was, cause = _str(p.get("should_be")), _str(p.get("was")), _str(p.get("cause"))
        if not should_be:
            continue
        effect = p.get("expected_effect") if isinstance(p.get("expected_effect"), dict) else {}
        effect_text = _str(effect.get("note"))
        if effect_text and effect.get("metric"):
            effect_text += f" (метрика {effect['metric']}, ожидается {'ниже' if effect.get('direction') == 'lower' else 'выше'})"
        ev, idx = [], {}
        for sid, line in refs:
            _add_ev(ev, idx, sid, line, "Тестовый разбор · основание предложения", "test-review")
        finding = {
            "id": "tr:" + text_key(json.dumps([session_id, pattern_id, should_be], ensure_ascii=False))[:20],
            "sev": "info",
            "title": _first_sentence(should_be),
            "what": " ".join(x for x in (f"Было: {_lower_first(was)}." if was else None,
                                          f"Вероятная причина: {_lower_first(cause)}." if cause else None) if x) or None,
            "ev": ev,
            "impact": None,
            "where": _str(p.get("target")),
            "snip": should_be,
            "kind": _REVIEW_KIND.get(p.get("class"), "diagnostic"),
            "pattern_id": pattern_id,
            "readiness": "hypothesis",
            "decision": "not_requested",
            "execution": "not_applied",
            "effect": "not_measured",
            "source": "test-review",
            "lang": "ru",
            "sessions": [session_id],
            "cause": cause,
            "alternative_causes": [],
            "preconditions": [],
            "exceptions": [],
            "verification": None,
            "rollback": None,
            "expected_effect": effect_text,
            "priority": p.get("priority"),
            "confidence": p.get("confidence"),
            "implementation_cost": p.get("implementation_cost"),
            "basis": p.get("basis"),
        }
        matched = [ep for ep in episodes if pattern_id and _pattern_match(ep["pattern_id"], pattern_id)]
        if matched:
            _attach_episodes(finding, matched, session_id)
        else:
            finding["ev"].sort(key=lambda e: (e["sid"] != session_id, e["sid"] or "", e["line"]))
        finding["impact"] = _episode_impact(matched, turn_classes)
        finding["sev"] = _severity(pattern_id, observations, checks, p.get("priority"), matched)
        findings.append(finding)
    return findings


def _outcome_tally(tasks) -> str | None:
    counts = {}
    for t in tasks:
        counts[t.get("outcome")] = counts.get(t.get("outcome"), 0) + 1
    parts = [f"{ru} {counts[en]}" for en, ru in _OUTCOME_RU if counts.get(en)]
    return ", ".join(parts) if parts else None


# ---------------------------------------------------------------- entry point

def _session_context(session_id: str, reports_dir: str, tests_dir: str, tr) -> dict:
    """Всё, что известно о сессии из разборов, кроме карточек «Что исправить»."""
    gaps: list[str] = []
    v2 = _find_v2(session_id, reports_dir, gaps)
    v1 = _read_json(os.path.join(reports_dir, "deep", f"{session_id}.json"), gaps, "Deep v1")
    if v1 is not None and not isinstance(v1, dict):
        gaps.append("Deep v1: неожиданная структура, пропущен")
        v1 = None
    coverage = _read_json(os.path.join(reports_dir, "coverage", f"{session_id}.json"), gaps, "Покрытие")
    review = _read_json(os.path.join(tests_dir, session_id, "review.json"), gaps, "Тестовый разбор")
    if review is not None and not isinstance(review, dict):
        review = None

    turn_classes = {}
    corrections = None
    if review is not None:
        for h in review.get("human_turns") or []:
            if isinstance(h, dict) and _int(h.get("turn_index")) is not None and isinstance(h.get("class"), str):
                turn_classes[h["turn_index"]] = h["class"]
        corrections = sum(1 for c in turn_classes.values() if c == "correction")

    tasks = _tasks_v2(v2, tr) if v2 else []
    tasks_by_id = {t["task_id"]: t for t in tasks if t.get("task_id")}

    checks_from = None
    if v2 and v2.get("checks"):
        checks, checks_from = _checks(v2["checks"], session_id, tr), "deep-v2"
    elif isinstance(coverage, dict) and coverage.get("checks"):
        checks, checks_from = _checks(coverage["checks"], session_id, tr), "coverage"
    else:
        checks = []

    # Итог сессии: в Deep v2 исход ставится по заданиям, общего итога нет — берём итог Deep v1
    # и явно пишем об этом; без v1 итог сессии не выводится.
    outcome = outcome_basis = model_opinion = task_text = None
    session_level_from = None
    v1_langs = []
    if v1:
        task_text, l1 = tr(v1.get("task"))
        outcome = v1.get("outcome") if isinstance(v1.get("outcome"), str) else None
        outcome_basis, l2 = tr(v1.get("outcome_basis"))
        model_opinion, l3 = tr(v1.get("model_opinion"))
        v1_langs = [l1, l2, l3]
        session_level_from = "deep-v1"
    title = task_text
    if title is None and review is not None:
        facets = review.get("facets") if isinstance(review.get("facets"), dict) else {}
        title, _ = tr(facets.get("task_statement"))
    if v2:
        tally = _outcome_tally(tasks)
        note = f"Deep v2 оценивает задания отдельно: {tally}." if tally else None
        if v1 and outcome is not None:
            prefix = "Итог сессии — из Deep v1."
            outcome_basis = " ".join(x for x in (outcome_basis, prefix, note) if x)
        else:
            outcome = None
            outcome_basis = " ".join(x for x in ("Deep v2 не выносит итог сессии.", note) if x)

    v1_observations = _observations(v1.get("observations"), session_id, tr, with_tasks=False) if v1 else []
    if v2:
        observations = _observations(v2.get("observations"), session_id, tr, with_tasks=True)
        unknowns, u_langs = tr.many(v2.get("unknowns"))
        deep = {
            "version": 2,
            "analysis_version": _str(v2.get("analysis_version")),
            "previous_report": _str(v2.get("previous_report")),
            "task": task_text, "outcome": outcome, "outcome_basis": outcome_basis, "model_opinion": model_opinion,
            "session_level_from": session_level_from,
            "observations": observations,
            "unknowns": unknowns,
        }
        deep["lang"] = merge_lang([*v1_langs, *u_langs, *(o["lang"] for o in observations)])
    elif v1:
        observations = v1_observations
        unknowns, u_langs = tr.many(v1.get("unknowns"))
        deep = {
            "version": 1,
            "task": task_text, "outcome": outcome, "outcome_basis": outcome_basis, "model_opinion": model_opinion,
            "session_level_from": session_level_from,
            "observations": observations,
            "unknowns": unknowns,
        }
        deep["lang"] = merge_lang([*v1_langs, *u_langs, *(o["lang"] for o in observations)])
    else:
        observations, deep = [], None

    return {
        "sid": session_id, "v2": v2, "v1": v1, "coverage": coverage, "review": review,
        "turn_classes": turn_classes, "corrections": corrections,
        "tasks": tasks, "tasks_by_id": tasks_by_id, "checks": checks, "checks_from": checks_from,
        "title": title, "outcome": outcome, "outcome_basis": outcome_basis, "deep": deep,
        "session_level_from": session_level_from,
        "observations": observations, "v1_observations": v1_observations,
        "episodes": _episodes(review, session_id),
        "gaps": gaps,
    }


def _session_findings(ctx, tr, *, registry=None) -> list:
    """Карточки одной сессии. ``registry`` — уже построенные карточки общего реестра:
    тогда кандидаты Deep v2 и черновики v1 этой сессии карточек не дают, а предложения
    тестового разбора прикрепляются к карточке реестра с тем же паттерном."""
    sid = ctx["sid"]
    if registry is not None:
        existing = registry
        findings = []
    else:
        findings, v2_lines = [], {}
        if ctx["v2"]:
            findings, v2_lines = _v2_findings(ctx, tr)
        if ctx["v1"]:
            findings += _v1_findings(ctx["v1"], sid, tr, ctx["v1_observations"], findings, v2_lines, ctx["episodes"])
        existing = findings
    if ctx["review"] is not None:
        findings += _review_findings(ctx["review"], sid, existing if registry is None else existing + findings,
                                     ctx["observations"], ctx["checks"], ctx["episodes"], ctx["turn_classes"])
    return findings


def _session_result(ctx, findings, tr, findings_from: str) -> dict:
    gaps = list(ctx["gaps"])
    if tr.missing:
        gaps.append(f"Без перевода осталось английских строк: {len(tr.missing)} (показаны как есть, lang: en)")
    outcome = ctx["outcome"]
    session_fields = {
        "title": ctx["title"],
        "outcome": outcome,
        "outcome_basis": ctx["outcome_basis"],
        "tasks": ctx["tasks"],
        "checks": ctx["checks"],
        "deep": ctx["deep"],
        "corrections": ctx["corrections"],
        "sources": {
            "deep": "v2" if ctx["v2"] else ("v1" if ctx["v1"] else "none"),
            "coverage": isinstance(ctx["coverage"], dict),
            "test_review": ctx["review"] is not None,
            "checks_from": ctx["checks_from"],
            "outcome_from": ctx["session_level_from"] if outcome is not None else None,
        },
    }
    if findings_from:  # только load_all: "registry" | "session"
        session_fields["sources"]["findings_from"] = findings_from
    return {
        "session_fields": session_fields,
        "findings": findings,
        "human_turn_classes": ctx["turn_classes"],
        "episodes": [dict(ep) for ep in ctx["episodes"]],
        "gaps": gaps,
    }


def _load(session_id: str, reports_dir: str, tests_dir: str, translations: dict | None = None):
    tr = _Translator(translations if translations is not None else load_translations(translation_path(reports_dir)))
    ctx = _session_context(session_id, reports_dir, tests_dir, tr)
    findings = _session_findings(ctx, tr)
    return _session_result(ctx, findings, tr, None), tr


def load_session(session_id: str, reports_dir: str, tests_dir: str) -> dict:
    """Выводы по одной сессии (без общего реестра предложений).

    → ``{"session_fields", "findings", "human_turn_classes", "episodes", "gaps"}``;
    ``gaps`` — что не прочитано или не переведено (для ``dataset.gaps``).
    """
    return _load(session_id, reports_dir, tests_dir)[0]


# ---------------------------------------------------------------- all sessions

_SEV_RANK = {"bad": 0, "warn": 1, "info": 2}
_READY_RANK = {"hypothesis": 0, "needs_spec": 1, "prepared": 2}


def _load_registry(reports_dir: str, gaps: list):
    """``<reports>/v2/proposals.json`` → список предложений или None (нет файла или не реестр)."""
    path = os.path.join(reports_dir, "v2", "proposals.json")
    data = _read_json(path, gaps, "Реестр предложений")
    if data is None:
        return None
    if not (isinstance(data, dict) and data.get("kind") == "proposal_registry" and data.get("schema_version") == 2
            and isinstance(data.get("proposals"), list)):
        gaps.append("Реестр предложений не соответствует схеме (kind/schema_version/proposals): карточки собраны по сессиям")
        return None
    return data["proposals"]


def _merge_card(into, other):
    """Одна карточка из двух с тем же id/group_key из разных сессий: объединяются сессии и
    основания; sev — худший, готовность — наименьшая, импакт — сумма коррекций."""
    for sid in other.get("sessions") or []:
        if sid not in into["sessions"]:
            into["sessions"].append(sid)
    idx = {(e["sid"], e["line"]): e for e in into["ev"]}
    for e in other.get("ev") or []:
        key = (e["sid"], e["line"])
        if key in idx:
            for src in e.get("src", "").split(", "):
                if src and src not in idx[key]["src"].split(", "):
                    idx[key]["src"] += ", " + src
            if "test-review" in e.get("src", "") and e.get("text"):
                idx[key]["text"] = e["text"]
        else:
            item = dict(e)
            idx[key] = item
            into["ev"].append(item)
    _sort_ev(into)
    if _SEV_RANK.get(other.get("sev"), 2) < _SEV_RANK.get(into.get("sev"), 2):
        into["sev"] = other["sev"]
    if _READY_RANK.get(other.get("readiness"), 0) < _READY_RANK.get(into.get("readiness"), 0):
        into["readiness"] = other["readiness"]
    if other.get("impact"):
        if into.get("impact"):
            n = _impact_count(into["impact"]) + _impact_count(other["impact"])
            into["impact"] = {"value": _plural_corrections(n),
                              "label": "в эпизодах " + "; ".join(x["label"].split(" ", 2)[-1] for x in (into["impact"], other["impact"]))}
        else:
            into["impact"] = dict(other["impact"])
    seen = {r.get("id") for r in into.get("replaces") or []}
    for r in other.get("replaces") or []:
        if r.get("id") not in seen:
            into.setdefault("replaces", []).append(r)
    into["lang"] = merge_lang([into.get("lang"), other.get("lang")])


def _merge_across(per_session: list) -> list:
    """Без реестра: карточки разных сессий с общим id или group_key — одна карточка."""
    out, by_key = [], {}
    for findings in per_session:
        for f in findings:
            keys = [k for k in (f.get("id"), f.get("group_key")) if k]
            same = next((by_key[k] for k in keys if k in by_key), None)
            if same is f:
                continue
            if same is None:
                out.append(f)
                for k in keys:
                    by_key[k] = f
                continue
            _merge_card(same, f)
            for k in keys:
                by_key.setdefault(k, same)
    return out


def load_all(session_ids, reports_dir: str, tests_dir: str, translations: dict | None = None) -> dict:
    """Выводы по набору сессий с общим реестром предложений.

    → ``{"sessions": {id: <как load_session>}, "findings": [...], "gaps": [...]}``.

    * Есть ``<reports>/v2/proposals.json`` (реестр Deep 2.0): карточки deep-v2 — только из
      реестра, по одной на предложение (``registry: true``), если хотя бы один источник —
      сессия из ``session_ids``; чужие id сессий остаются в ``sessions``/``ev``. Кандидаты
      Deep v2 отдельных сессий карточек не дают (реестр построен из них), черновики Deep v1
      не показываются (они пересмотрены в Deep 2.0). Предложения и эпизоды тестового
      разбора прикрепляются к карточке реестра с тем же паттерном в этой сессии, иначе
      предложение становится отдельной карточкой test-review.
    * Реестра нет: карточки по сессиям, как в ``load_session``; карточки с общим
      ``proposal_id``/``group_key`` из разных сессий объединяются.

    ``findings`` в словаре каждой сессии — те же объекты, что в общем списке (карточки, где
    есть эта сессия); складывать их с общим списком не нужно.
    """
    translations = translations if translations is not None else load_translations(translation_path(reports_dir))
    ids = [sid for sid in dict.fromkeys(session_ids or []) if isinstance(sid, str) and sid]
    gaps: list[str] = []
    registry = _load_registry(reports_dir, gaps)

    ctxs, trs = {}, {}
    for sid in ids:
        tr = _Translator(translations)
        ctxs[sid], trs[sid] = _session_context(sid, reports_dir, tests_dir, tr), tr

    if registry is not None:
        rtr = _Translator(translations)
        chosen = [p for p in registry if isinstance(p, dict)
                  and any(isinstance(s, dict) and s.get("session_id") in ctxs for s in p.get("sources") or [])]
        findings, _ = _proposal_cards(chosen, ctxs, rtr, None, registry=True)
        cards = list(findings)
        for sid in ids:
            findings += _session_findings(ctxs[sid], trs[sid], registry=cards + findings[len(cards):])
        if any(((ctxs[sid]["v1"] or {}).get("recommendations")) for sid in ids):
            gaps.append("Черновики рекомендаций Deep v1 не показаны: есть реестр предложений Deep 2.0, "
                        "куда они вошли после пересмотра.")
        if rtr.missing:
            gaps.append(f"Реестр предложений: без перевода осталось английских строк: {len(rtr.missing)} "
                        "(показаны как есть, lang: en)")
        findings_from = "registry"
    else:
        findings = _merge_across([_session_findings(ctxs[sid], trs[sid]) for sid in ids])
        findings_from = "session"

    sessions = {}
    for sid in ids:
        mine = [f for f in findings if sid in (f.get("sessions") or [])]
        sessions[sid] = _session_result(ctxs[sid], mine, trs[sid], findings_from)
    return {"sessions": sessions, "findings": findings, "gaps": gaps}
