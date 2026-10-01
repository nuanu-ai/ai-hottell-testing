# Контракт данных hottell UI v3

Сборщик (`ui/builder/build.py`) читает приватные локальные источники и пишет
`<out>/dataset.json` и `<out>/sessions/<session_id>.json`. Сервер (`ui/main.go`)
отдаёт их как есть. Интерфейс (`ui/static/index.html`) ничего не выдумывает:
отсутствующее значение — `null`, и интерфейс показывает «—» или «нет данных».

`<out>` по умолчанию — `local-data/ui/` (игнорируется Git). Сырые тексты
остаются на этой машине; сервер слушает только `127.0.0.1`.

## Источники

| Источник | Путь | Что берём |
|---|---|---|
| Сырой rollout Codex | `~/.codex/sessions/**/rollout-*-<id>.jsonl` | ходы, реплики, вызовы, ошибки, токены, модель, сжатия, ожидания, skills |
| Deep v2 | `local-data/reports/rebuild/<id>/deep.v2.json` (или `reports/v2/deep/<id>.json`) | задания, 13 проверок, наблюдения, `proposal_candidates` |
| Deep v1 | `local-data/reports/deep/<id>.json` | задача, исход, наблюдения, рекомендации, неизвестное |
| Покрытие | `local-data/reports/coverage/<id>.json` | 13 проверок, если нет v2 |
| Тестовый разбор | `local-data/tests/<id>/review.json`, `simulated-telemetry/summary.json` | классы реплик человека (коррекции), эпизоды D01/D12/D05 |
| Каталог проверок | `ui/builder/catalogue.json` | названия и способ подсчёта 13 проверок |

## Ссылки на события

Каждое событие и доказательство несёт `line` — номер строки (с 1) в сыром rollout.
`local_transcript:L1273`, `L1273`, `#1273` (source_ordinal = line − 1) нормализуются
в `line: 1273`. Интерфейс по клику открывает ленту сессии на этой строке.

## dataset.json

```jsonc
{
  "schema_version": 1,
  "generated_at": "2026-09-30T12:00:00Z",
  "host": "alva-mac",
  "window": {"from": "ISO", "to": "ISO"},
  "pricing": {                                   // оценка, не счёт
    "basis": "api_list_price_estimate",
    "note": "Оценка по прайсу API. Фактически — подписка Codex, счёт по токенам не выставлялся.",
    "models": {"gpt-6-astra": {"input": 1.25, "cached": 0.125, "output": 10.0}}  // $ за 1 млн токенов
  },
  "sessions": [Session],
  "tools": [{"name": "exec", "kind": "builtin|mcp|nested", "server": null, "calls": 0, "errors": 0, "unknown": 0, "p50_s": null, "p95_s": null}],
  "mcp": [{"server": "cua_repl", "calls": 0, "errors": 0, "unknown": 0, "avg_s": null}],
  "commands": [{"cmd": "xcodebuild test", "project": "bakery", "runs": 0, "failed": 0, "unknown": 0, "p95_s": null, "total_min": 0}],
  "permissions": [{"label": "политика never · без запроса", "count": 0}, {"label": "агент спросил вас", "count": 0}],
  "friction": [Friction],
  "skills": Skills,
  "findings": [Finding],
  "done": [],                                    // применённые правки с эффектом; пока пусто
  "checks_catalog": [{"id": "D01", "name": "…", "how": "…"}],
  "gaps": ["что не собрано и почему"]
}
```

### Session

```jsonc
{
  "id": "00000000-0000-4000-8000-00000000000a", "short": "00000000…000a",   // первые 8 и последние 4 знака id
  "agent": "codex", "source_path": "…", "source_sha256": "…", "records": 3305,
  "project": "bakery", "cwd": "…", "branch": null,
  "start": "ISO", "end": "ISO",
  "wall_min": 1603.0,          // от первого до последнего события
  "active_min": 120.2,         // сумма длительностей ходов агента
  "user_min": 14.0,            // сумма пауз «ответ агента → ваша реплика», каждая ≤ 30 мин; null если нельзя посчитать
  "turns": 26, "prompts": 28, "corrections": 5,   // corrections: null если нет разметки
  "reqs": 436,                 // ответов модели (token_usage_record)
  "model": "gpt-6-astra",
  "tok": {"input": 0, "cached": 0, "output": 0, "reasoning": 0},
  "cache_hit": 0.95,
  "cost_usd": 12.3,            // оценка по pricing; null если модели нет в таблице
  "calls": 381, "errors": 29, "unknown_results": 0,
  "commits": 0, "prs": 0, "added": null, "removed": null,
  "first": "первая реплика человека, ≤ 200 символов",
  "title": "задача из Deep или первая реплика",
  "flags": ["compact", "wait", "coldcache"],
  "compactions": 6, "aborted": 1, "waits": 4, "wait_min": 12.5,
  "outcome": "partial|verified|failed|unknown", "outcome_basis": "…",
  "tasks": [{"task_id": "T01", "goal": "…", "outcome": "partial", "start_line": 9, "end_line": 369}],
  "checks": [{"id": "D01", "status": "suspected|confirmed|checked_clear|insufficient_data|not_checked|not_applicable", "summary": "…", "lines": [1219]}],
  "deep": {"version": 2, "task": "…", "outcome": "…", "outcome_basis": "…", "model_opinion": "…",
           "observations": [{"pattern": "…", "finding": "…", "status": "…", "lines": [9]}],
           "unknowns": ["…"]},
  "sources": {"transcript": "available", "hooks": "missing", "otel": "missing", "deep": "v2|v1|none", "coverage": true, "test_review": true},
  "daily": [{"date": "2026-09-28", "cost_usd": 1.23, "reqs": 40,
             "tok": {"input": 0, "cached": 0, "output": 0, "reasoning": 0},
             "agent_min": 12.5, "user_min": 3.0, "calls": 50, "errors": 2}]   // по дням UTC
}
```

Агрегаты `tools[]`, `mcp[]`, `commands[]`, `permissions[]` несут `by_session`
(`{"<sid>": {"calls", "errors", "unknown"}}`, для команд `{"runs", "failed", "unknown"}`, для разрешений — число),
чтобы интерфейс пересчитывал их под фильтр; сумма по сессиям равна итогу строки. `unknown` — вызовы с неизвестным
исходом, по правилу своей строки: у инструментов и MCP — без записанного результата (у вызовов MCP версии :8800 —
без статуса завершения, одно правило для `tools[]` и `mcp[]`), у команд — без кода выхода и признака ошибки (в живой
версии — и с результатом без признака успеха или ошибки). p50/p95 остаются общими.

### sessions/<id>.json

```jsonc
{
  "id": "…",
  "events": [{
    "line": 13, "at": "ISO", "turn": 0,
    "k": "prompt|answer|tool|err|api|wait|compact|abort|skill|agent",
    "x": "короткий текст: реплика, команда, заголовок вызова",
    "note": "exit 1 · текст ошибки ≤ 160 символов" ,
    "side": "1.4 с | $0.02 | вы",
    "tool": "exec"
  }],
  "cum": [[0, 0], [12.5, 0.8]],    // минуты от начала → $ нарастающим итогом (оценка)
  "ctx": [[0.1, 36.7]],            // минуты → вход запроса, тыс. токенов
  "tools": {"exec": 293, "js": 69},
  "turns": [{"turn": 0, "turn_id": "…", "start": "ISO", "end": "ISO", "dur_ms": 677970,
             "state": "task_complete|turn_aborted|open", "prompt_line": 10, "prompt": "…", "answer": "…",
             "tok": {"input": 0, "cached": 0, "output": 0}}]
}
```

### Friction

Ключи и правила подсчёта (по событиям, без модели):

| key | Название | Как считается | sev |
|---|---|---|---|
| retry | Повтор с той же ошибкой | один и тот же вызов (одинаковый вход) ≥ 3 раз в окне 10 вызовов, все с ошибкой, без правок между | bad |
| thrash | Цикл правка → тест | ≥ 3 провала тестовой команды подряд с правками между ними | bad |
| mcpfail | Сбой MCP | ≥ 3 ошибок одного MCP-сервера в сессии | bad |
| coldcache | Холодный кэш | ваша реплика после паузы > 5 мин, первый запрос хода читает из кэша < 50 % входа | warn |
| compact | Сжатие контекста | событие `compacted` посреди работы | warn |
| reread | Перечитывание файлов | один файл прочитан ≥ 4 раз за сессию | warn |
| wait | Агент ждал вас | вызов `request_user_input*`; ожидание до вашего ответа | info |
| abort | Прерванный ход | `turn_aborted` | info |
| correction | Ваша коррекция | реплика размечена как correction в review.json | warn |

```jsonc
{"key": "compact", "name": "…", "how": "…", "sev": "warn",
 "sessions": ["<id>"], "by_session": {"<id>": 13}, "count": 13, "cost_usd": 1.2,
 "evidence": [{"sid": "<id>", "line": 386, "at": "ISO", "text": "…"}]}
```

`by_session` — `{"<sid>": эпизодов}` (обе версии); интерфейс считает эпизоды по нему (при `count: null` — «—»).
`evidence` — до 60 самых свежих доказательств всех эпизодов, в порядке времени (обе версии); эпизод перечитывания
или сбоя MCP несёт до 8 последних своих событий.

### Skills

```jsonc
{
  "available": 42,                         // skills в <skills_instructions>
  "rows": [{"name": "…", "source": "plugin или каталог", "activations": 2, "sessions": ["<id>"], "by_session": {"<id>": 2},
            "first_line": 120, "to_code_min": null, "corrections_after": 1, "subagents": 0,
            "size_ktok": 4.6, "state": "used|noisy|slow|unused|conflict",
            "evidence": [{"sid": "<id>", "line": 120, "at": "ISO", "text": "cat …/SKILL.md"}]}],
  "gantt": {"sid": "<id>", "title": "…", "from": "ISO", "to": "ISO",
            "rows": [{"name": "…", "segs": [{"from": "ISO", "to": "ISO", "c": "skill|review|subagent|noskill|mcp", "label": "…"}]}],
            "marks": [{"at": "ISO", "text": "«…»", "line": 1218}]}
}
```

Активация skill — чтение его `SKILL.md` вызовом инструмента. Отрезок skill на диаграмме — от
активации до конца хода. Субагенты — `spawn_agent`/`followup_task`. MCP — вызовы с `namespace mcp__*`.
`rows[].by_session` — активации по сессиям (сумма равна `activations`); интерфейс считает по нему активации
в таблице и в итоге для выбранных сессий. Без поля (старые датасеты) — `activations` за весь датасет.

### Finding

Карточка «Что исправить». Источник — только готовые разборы; ничего не додумывается.

```jsonc
{
  "id": "p2:1bd6e891d80da2b423e5",
  "sev": "bad|warn|info",
  "title": "что сделать, одной строкой (рус.)",
  "what": "что наблюдалось и вероятная причина (рус.)",
  "ev": [{"sid": "<id>", "line": 1219, "at": "ISO|null", "text": "что видно в этой строке"}],
  "impact": {"value": "2 коррекции", "label": "на задачу"},      // null, если нечем обосновать
  "where": "точный объект изменения",
  "snip": "текст правки",
  "kind": "personalization|project_rule|skill|hook|script|automation|diagnostic|habit",
  "pattern_id": "D12",
  "readiness": "hypothesis|needs_spec|prepared",
  "decision": "not_requested", "execution": "not_applied", "effect": "not_measured",
  "source": "deep-v2|deep-v1|test-review|detector",
  "lang": "ru|ru-translated",
  "sessions": ["<id>"],
  "cause": null, "alternative_causes": [], "preconditions": [], "exceptions": [],
  "verification": null, "rollback": null, "expected_effect": null
}
```

## Дополнения сборщика

Сверх схемы выше (интерфейс их понимает или спокойно игнорирует):

- `tools[].kind: "nested"` — вызовы внутри скриптов `exec`: shell-команды, `apply_patch`, MCP, поиск;
  `mcp[]` несёт `direct` и `nested`.
- `permissions[]` — ещё строка о песочнице.
- У проверок и наблюдений рядом с `lines` есть `at`, у заданий — `start_at`/`end_at`,
  в `sessions/<id>.json` — `episodes` с `at`.
- В карточках Deep 2.0 сохраняются исходные поля предложения (`change_type`, `priority`, `scope`,
  `selection_reason`, `readiness_reason`, `existing_rule`, `target`, `replaces`), у доказательств — `src`.
- Источник `test-review` — предложения `review.json`; если в сессии уже есть карточка Deep с тем же
  паттерном, предложение только добавляет к ней основания.
- Имя проекта — папка `cwd`; для локальной копии ChatGPT-проекта (`…/g-p-<hash>`) — имя из её `AGENTS.md`.

## Живая версия (`live.py`)

Тот же формат с `"variant": "live"` и `"title"`. Источник — только ClickHouse (`agent-hooks` и нативный OTel),
без журналов и Deep. `line` — порядковый номер события в сессии (1..N). `sources.hooks`: `recorded` или
`partial` — записан только хвост: первый `SessionStart` с `source` `startup`, `clear` или `resume` — `resume`
(`clear` в Claude Code начинает новую сессию; `compact` не в счёт; `resume` после `startup` или `clear` — возврат
к записанной сессии), или первое событие хуков позже времени создания из uuid7 больше чем на 1 ч. `sources.otel`: `recorded` или `unlinked`, `transcript: not_used`.
Карточки — только детекторы (`source: "detector"`). `pricing.basis: "otel_reported"` — стоимость из OTel Claude Code.
Доказательства карточек (`Finding.ev`) — самые свежие, в порядке времени: до 8, у карточек об OTel и Bash Codex — до
5 сессий. Эпизод в карточке — его последнее событие, сессия без токенов в карточке об OTel — её последнее событие.

Поля сверх общей схемы:

- `Session.kind` — `system` (явная служебная задача Codex: подсказки Desktop; проверка подсказок Codex — реплика
  «You are an expert at upholding safety and compliance standards for Codex ambient suggestions…»; память;
  папка `~/.codex/memories`) | `automation` (реплика `<heartbeat>`) | `user` (остальное, в том числе короткие
  сессии и сессии без реплик); `Session.kind_reason` — сработавшее правило: `подсказки Codex Desktop`,
  `проверка подсказок Codex`, `память Codex`, `папка ~/.codex/memories`, `реплика <heartbeat>` или `null`.
  Интерфейс по умолчанию скрывает только `system`.
- `Session.outcome_known` — вызовы с известным исходом (успех или ошибка); долю ошибок интерфейс считает среди них.
- `Session.waits` — вопросы вам (`request_user_input`, `request_user_input_async`, `AskUserQuestion`; вопрос, записанный
  без PreToolUse, не учитывается) и окна разрешения Claude, где решали вы. Обычный (не async) вопрос длится до своего
  результата или до вашей следующей реплики — что раньше; async — до вашей следующей реплики. Простой начинается,
  когда закончились вызовы основного агента, уже шедшие в момент вопроса (пока выполнялся другой вызов — не простой);
  если конец такого вызова неизвестен (нет PostToolUse, а у Claude — и длительности в tool_result) и он из того же
  хода, что и вопрос, простой не доказан и вопрос — не остановка; вызов прошлого хода без PostToolUse — потерянный
  результат, он не мешает. Остановка — простой не короче 5 с, внутри которого основной агент не начал ни одного
  вызова; вопрос без конца — остановка, если после него основной агент ничего не начинал. Не остановки: вопрос,
  вернувший ошибку (он не задан), и ответ быстрее 5 с, в том числе нулевой длины. Окно разрешения Claude — остановка
  неизвестной длины.
  `wait_min` — сумма измеренных остановок: от конца уже шедших вызовов до ответа, интервалы объединены; `null` —
  если остановки были, но ни одна не измерена; `0` — если остановок не было. Эпизоды `wait` — только остановки
  на вопросе.
- `Friction.by_session` — `{"<sid>": эпизодов}`; интерфейс считает эпизоды по нему (при `count: null` — «—»).
  `Friction.cost_usd` — стоимость самих эпизодов; интерфейс показывает её, когда выбраны все сессии сигнала.
- Колонка «Стоимость эпизодов» (обе версии): сумма есть только у сигналов с атрибутируемой стоимостью (холодный
  кэш) и только когда все сессии сигнала в выборке; у остальных «—». Так же — цена в карточке темы.
- `reread`: одно место файла ≥ 3 раз подряд одним агентом (субагенты — отдельно) с одинаковым хэшем записанного
  ответа (`resp_hash`, cityHash64 в ClickHouse), без сжатия контекста и правки этого файла между чтениями; чтения без
  результата, с пустым записанным ответом или с ошибкой (исход error или ошибка cat/sed/head… в первой строке
  ответа), команды на несколько файлов и хвост (`tail`) не считаются.
  Эпизод несёт `path`, `extra`, `bytes`. Карточка — от 3 эпизодов; файлы группируются по полному пути и
  показываются как `папка/имя`.
- `Finding.scope` — `work` | `collection` (здоровье сбора данных: показывается свёрнутым, считается по всем сессиям,
  включая `system`, `readiness: needs_spec`; переключатель «Без служебных» на него не действует, период, агент и
  проект пересчитывают эффект). Карточки `work` — только по сессиям `kind != system`, все
  `readiness: hypothesis` с `preconditions`, в том числе общие «Разобрать эпизоды…». Интерфейс отличает только
  значение `collection`: свободный `scope` карточек Deep 2.0 показывается как работа. `impact_by_session` и
  `impact_unit` — вклад сессий для пересчёта эффекта под фильтр; доказательства (`ev`) интерфейс оставляет только
  выбранных сессий.
- Карточка «нет PostToolUse» (`collection`) — сессии Codex, начатые при работающих хуках. Не считаются вызовы без
  PreToolUse, вызовы моложе 2 мин на момент сборки и вызовы открытого последнего хода, пока сессия идёт (последнее
  событие моложе 1 ч); через час тишины они снова считаются. Вызовы в последние 2 мин своей сессии считаются и
  называются отдельно. Момент сборки — `--until`, если он задан, иначе текущее время: сборка на фиксированном окне
  воспроизводима.
- `skills.available` — объединение снимка и открытых skills; при `snapshot_complete = false` — «известных».
  Имя skill — `name` из фронтматтера `SKILL.md` (только блок между первыми строками `---`), иначе папка, для общих
  имён (`skill`, `skills`) — папка выше.
## Журнал коуча (`/api/coach/journal`)

Журнал hottell-coach — `local-data/coach/journal.jsonl` (флаг `-journal`), одна JSON-запись на строку;
его дописывает `coach_journal` локального MCP `hottell-local`, сервер дашборда только читает. Формат записи
задаёт `analytics/coach/SKILL.md`, раздел «7. Журнал».

```json
{"entries": [{"id": "…", "topic_key": "…", "decision": "applied", "…": "…",
              "result": "repeated", "observations": 4, "repeats": 1, "checked_at": "2026-10-08T09:00:00Z", "checks": 2}],
 "broken_lines": 0}
```

- `entries` — решения (`applied`, `declined`, `not_justified`, `test`) в порядке записи, с полями как в файле.
  Записи проверок (`check_of`) отдельно не отдаются: последняя проверка сворачивается в своё решение —
  `result`, `observations`, `repeats` (не у `repeated` — `null`), `checked_at` (её `at`) и `checks` (сколько
  проверок было). У решения без проверок `result` и `observations` — `null`, как в файле, а `repeats`,
  `checked_at` и `checks` нет. Проверка решения, которого в файле выше нет, пропускается.
- `broken_lines` — сколько строк не разобралось как JSON-объект (в том числе `null`); они пропускаются.
- Последняя строка без `\n` — запись, которая ещё пишется (`coach_journal` дописывает строку вместе с `\n`
  одной записью): она пропускается и битой не считается.
- Нет файла — `{"entries": [], "broken_lines": 0}`; файл не читается — `500` с `{"error": "…"}`.

## /api/status

Статус отправки в сервис команды. Сервер раз в минуту выполняет `hottell status --json`
(бинарь — флаг `-hottell`) и отдаёт последний итог. Только с loopback, иначе 403
`{"error": "…"}`. Ответ не кэшируется.

```jsonc
// до первой проверки или при -hottell ""
{"state": "unknown"}

// после проверки
{
  "checked_at": "2026-10-01T07:00:00Z",          // UTC
  "state": "ok|problem|not_configured",
  "reason": "бинаря hottell нет",                // только у not_configured
  "service_origin": "https://hottell.example",   // origin mcp.url; только у ok и problem
  "status": {…}                                  // вывод hottell status --json как есть; только у ok и problem
}
```

- `ok` — в JSON `ok: true` (код выхода 0); `problem` — `ok: false` (код выхода 1 — не сбой запуска);
  что не так — `status.problems[]`, по строке на проблему.
- `not_configured` — бинаря нет, не ответил за 20 с, упал до вывода, вывод не JSON (старый прототип)
  или в JSON нет `mcp.url`. `reason`:
  - «бинаря hottell нет»;
  - «hottell status не запустился: …» — другая ошибка запуска;
  - «hottell status не ответил за 20 с» — тайм-аут;
  - «hottell status завершился с ошибкой: <первая строка stderr>» — код 1 и пустой stdout (новый
    hottell упал до отчёта, например без `$HOME`); строка — до 200 символов, длиннее — обрезана с «…»;
    без stderr — «exit status 1»;
  - «hottell status --json не дал JSON — бинарь не той версии» — остальной вывод не JSON (старый
    прототип печатает использование в stderr и выходит с кодом 2);
  - «MCP hottell не подключён» — в JSON нет `mcp.url`.
- `status` — формат `statusReport` из `temp/cmd/hottell/status.go`: `ok`, `version`, `mcp` (`agent`,
  `config`, `url`, `last_success`, `error`, `message`), `token` (`present`, `ingest_url`, `last_sent`,
  `last_error`, `unauthorized`), `settings` (`cached`, `version`, `applied`), `agents`, `queue`
  (`queued`, …), `daemon`, `problems`. Ключа MCP и токена коллектора в нём нет.
- `service_origin` — куда ведут ссылки на `/telemetry` и `/connect` сервиса.


## /api/pulse

Пульс шапки «Живых данных». Есть только у сервера с флагом `-clickhouse`; без него — 404
`{"error": "…"}`, в `/api/meta` — `"pulse": false`. Только с loopback, иначе 403. Ответ общий
для всех вкладок и кэшируется на 5 секунд.

```jsonc
{
  "at": "2026-10-01T14:58:30.123+08:00",          // когда собран ответ (время сервера с поясом)
  "bars": [                                        // события хуков (ServiceName = agent-hooks) за 10 минут
    {"t": "2026-10-01T06:58:10Z", "agent": "codex", "n": 7}   // начало 10-секундного интервала, UTC
  ],
  "active": [                                      // сессии с событиями за 2 минуты, свежие сверху, до 20
    {"sid": "01a0f57b-…", "agent": "codex", "project": "ai-hottell",
     "first": "Почини сборку",                     // первая реплика из dataset.json; нет сессии в датасете — поля нет
     "last_at": "2026-10-01T06:58:25Z", "per_min": 12}        // событий за последнюю минуту
  ],
  "error": "ClickHouse недоступен: …"              // только при ошибке; тогда bars и active пустые
}
```

- `project` — из датасета, иначе папка непустого `cwd` событий сессии; без `cwd` — пустая строка (интерфейс
  пишет «без проекта»).
- Интерфейс раскладывает `bars` в 60 столбиков по 10 секунд (высота — `min(16, round(n/12·16))` px,
  Claude Code и Codex — стеком) и считает тишиной 12 пустых столбиков подряд (2 минуты).
