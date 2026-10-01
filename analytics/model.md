# Логическая модель данных

Сущности и поля, на которые опираются правила ([catalogue.yaml](catalogue.yaml)) и метрики ([metrics.md](metrics.md)). Модель логическая: события лежат в ClickHouse (`otel-lab`, [§4](#4-где-это-в-clickhouse)); собирает их в эти сущности техническая часть; MCP — источник и хранилище: отдаёт сущности и принимает результаты разборов. Аналитическая часть читает ClickHouse напрямую для сверки полей и приёмки.

Источники — события хуков (`hottell`: `Body = agent.hook.<Событие>`, поля payload хука — атрибуты) и нативная телеметрия агентов. Имена полей взяты из документации агентов и кода `hottell`; **сверяются по ClickHouse на первых своих сессиях** (план, шаг A0), расхождения фиксируются здесь.

## 1. Сессия и её события

### Session

| Поле | Смысл | Codex | Claude Code |
|---|---|---|---|
| `session_id` | идентификатор сессии | хук `session_id`; нативно `conversation.id` | хук `session_id`; нативно `session.id` |
| `agent` | `codex` / `claude` | атрибут `agent` от `hottell` | то же |
| `participant` | человек | соответствие `host.name` → участник (техническая часть) | то же |
| `started_at`, `ended_at` | начало и конец | начало — `SessionStart` или первое событие; конец — `SessionEnd` или 20 минут без событий (у Codex `SessionEnd.reason` всегда `other`). После конца ядро разбирает сессию и появляется вопрос о результате | то же |
| `start_source` | `startup / resume / clear / compact / fork` | `SessionStart.source` | `SessionStart.source` |
| `end_reason` | причина завершения | `SessionEnd.reason` (сейчас всегда `other`) | `SessionEnd.reason` — сверить имя поля |
| `project` | проект: последний сегмент `cwd` | `cwd` | `cwd` |
| `models[]` | модели в сессии | хук `model`; нативно `model` | `SessionStart.model`; нативно `api_request.model` |
| `goal_category` | класс задачи | глубокий разбор (фасеты) | то же |
| `kind` | `work` / `analysis`; аналитические сессии в метрики не входят | `analysis` — в сессии активирован skill `session-retro`; запуски быстрого разбора не собираются | то же |
| `usage` | токены: `input, output, cache_read, cache_write, reasoning`; стоимость | нативно `codex.sse_event` (response.completed), метрика `codex.turn.token_usage` | нативно `claude_code.api_request`, метрики `token.usage`, `cost.usage` |
| `human_active_min` | активное время человека | нет | метрика `claude_code.active_time.total{type=user}` |

### HumanTurn — реплика человека

| Поле | Смысл | Источник |
|---|---|---|
| `turn_index` | порядковый номер реплики в сессии, первая — постановка | порядок `UserPromptSubmit` |
| `ts` | время | событие |
| `text` | текст — только при `send_prompts = true` или локально — у быстрого и глубокого разбора | `UserPromptSubmit.prompt` |
| `length` | длина | нативно `user_prompt.prompt_length` |
| `after_question` | предыдущий ход агента закончился вопросом человеку | §3.6 |
| `class` | `task / correction / clarification / goal_change / answer / new_task / ack` | быстрый разбор, §3.5; глубокий разбор и человек могут исправить |
| `instruction` | инструкция о способе работы — пересказ одной фразой | быстрый разбор; для D10 |

### AgentTurn — ход агента

От `UserPromptSubmit` до `Stop`. Поля: `turn_index`, `started_at`, `ended_at`, `final_message` (`Stop.last_assistant_message`), `ends_with_question` (§3.6), `claims_done` (быстрый разбор, D05), `summary` (быстрый разбор), `usage` (если агент отдаёт по ходу; иначе доля от сессии, помечается `estimate`).

### ToolCall — вызов инструмента

| Поле | Смысл | Codex | Claude Code |
|---|---|---|---|
| `call_id` | связь начала и результата | хук `tool_use_id`; нативно `call_id` — сверить совпадение | хук и нативно `tool_use_id` |
| `tool` | имя инструмента | `tool_name` | `tool_name` |
| `mcp_server` | для `mcp__<server>__<tool>` — `<server>` | из `tool_name`; нативно `mcp_server` | из `tool_name` |
| `input` | аргументы | `PreToolUse.tool_input` | то же |
| `input_exact_hash` | хэш точных аргументов — «тот же вызов» (D03) | §3.1 | §3.1 |
| `input_hash` | хэш нормализованных аргументов — «похожий вызов» (D22) | §3.1 | §3.1 |
| `started_at`, `ended_at` | начало и конец | `PreToolUse`, `PostToolUse` | `PreToolUse`, `PostToolUse` / `PostToolUseFailure` |
| `success` | успех | нативно `codex.tool_result.success` | `PostToolUseFailure` = неуспех; нативно `claude_code.tool_result.success` |
| `error_text` | текст ошибки, обрезанный | `tool_response` / нативно `output` | `tool_response` / нативно `error` |
| `error_class` | класс ошибки | §3.2 | §3.2 |
| `output_len` | размер результата | нативно `output_length` | нативно `tool_result_size_bytes` |
| `is_edit` | вызов меняет файлы | §3.3 | §3.3 |
| `is_wait` | ожидание или опрос | §3.4 | §3.4 |

### Прочие события

| Сущность | Поля | Codex | Claude Code |
|---|---|---|---|
| `Permission` | `call_id`, `requested_at`, `decided_at`, `decision`, `source` (человек / настройки / hook) | хук `PermissionRequest`; нативно `codex.tool_decision` | хук `PermissionRequest` — добавить; нативно `claude_code.tool_decision` |
| `Interrupt` | `ts` | хук `Interrupt` | нативно `tool_decision.source = user_abort`; текст `[Request interrupted by user]` |
| `Compaction` | `ts`, `trigger`, `pre_tokens`, `post_tokens` | хуки `PreCompact / PostCompact` | хук `PreCompact`; нативно `claude_code.compaction` |
| `SkillUse` | `skill`, `ts`, `trigger` (человек / агент) | метрика `codex.skill.injected`, если она реально присутствует; `$skill` в промпте — только намерение человека, не доказательство использования | вызов инструмента `Skill`; нативно `claude_code.skill_activated`, если присутствует |
| `Subagent` | `agent_id`, `type`, `started_at`, `ended_at` | хуки `SubagentStart / SubagentStop` | хук `SubagentStop`; `SubagentStart` — добавить |
| `ApiError` | `ts`, `status`, `attempt` | нативно `codex.api_request` | нативно `claude_code.api_error` |

### ConfigItem — элемент настроек участника

Для D19 и D26. Источник — снимок настроек на `SessionStart` (техническая часть) или чтение на машине участника skill'ом. Текущий `hottell.skill_snapshot` записывает только локальные `SKILL.md`, исключает plugin skills и не доказывает, что агенту был доступен или что он вызвал skill. В локальном OTel на 30.09.2026 метрика `codex.skill.injected` не наблюдалась; отсутствие метрики нельзя считать отсутствием использования.

| Поле | Смысл |
|---|---|
| `kind` | `agents_md / claude_md / memory / personalization / skill / mcp_server` |
| `scope` | `global / project` и проект |
| `path_class` | вид пути без личных данных: `~/.codex/AGENTS.md`, `<project>/AGENTS.md`, `~/.claude/skills/<name>` |
| `hash`, `size`, `modified_at` | для свежести и изменений |
| `models_since_change[]` | модели и версии агента, появившиеся в сессиях после `modified_at` |
| `description` | для skill — `description` из SKILL.md |
| `rules[]` | для инструкций — список правил (строк-императивов) с номерами; извлекает skill |

## 2. Результаты анализа

Хранит техническая часть (MCP); пишут ядро, быстрый и глубокий разбор, человек. Хранятся дольше сырья: сырьё на стенде живёт 10 дней, а эти сущности нужны для окон в 30 дней и для журнала. В пилоте, пока хранилища нет, результаты лежат локально в `local-data/`.

| Сущность | Поля |
|---|---|
| `Episode` | `episode_id`, `pattern_id`, `session_ids[]`, `span {from_ts, to_ts, turn_from, turn_to}`, `evidence[]` (ids вызовов, реплик, событий), `cost {tokens, minutes, estimate}`, `severity`, `status: suspected / confirmed / dismissed`, `detector_version`, `created_at` |
| `Review` | `episode_id`, `by: human / skill`, `decision: confirm / dismiss / known`, `reason`, `at` |
| `TurnReview` | `session_id`, `turn_index`, поля по схеме `turn_review` ([quick-review.md](quick-review.md)), `by: quick / deep / human`, `model`, `tokens`, `at`; исправление глубокого разбора или человека — новая запись, главнее |
| `Facets` | по схеме `facets` в [skill/schemas.json](skill/schemas.json) |
| `Verdict` | `session_id`, `verdict: verified / partial / failed / unknown`, `basis[]` (проверки и разметка, на которых основан), `model_opinion`, `agent_claim`, `orange_flag {open, question, answer, answered_at}` |
| `Label` | разметка человека: `session_id`, `goal`, `done_criteria[]`, `outcome: 0–3`, `rework_min` (нет ответа — `unknown`, не 0), `what_went_wrong`, `task_ref` — ручная связь сессии с задачей (одна задача — несколько сессий, одна сессия — несколько задач), `share: none / metrics / metrics_facets / excerpts` — уровень для командного среза, `send: yes / no` — сессия выбрана на отправку |
| `Proposal` | по схеме `proposal`, включая `basis: single_session / repeated`; статусы `proposed / accepted / rejected / implemented / preliminary / verified / no_effect / worse` |
| `JournalEntry` | `proposal_id`, `implemented_at`, `target`, `usage {signal, count, since}`, `effect {metric, before, after, n_before, n_after, method: replay / before_after}`, `status` |

## 3. Правила нормализации

Определения, от которых зависят детекторы. Реализация — техническая часть.

### 3.1 Хэши аргументов `input_exact_hash` и `input_hash`

Канонический JSON аргументов: ключи отсортированы; строки обрезаны по краям, пробелы схлопнуты; абсолютные пути внутри `cwd` → относительные; для shell-команд убирается префикс `cd <путь> &&`.

- `input_exact_hash` = хэш `tool` + канонического JSON. Одинаковый — **тот же вызов** над тем же объектом; по нему D03 ищет повтор.
- `input_hash` = то же, но UUID, ISO-даты и время, hex длиной ≥16, числа длиной ≥6 заменены плейсхолдерами `<uuid>`, `<ts>`, `<hex>`, `<n>`. Одинаковый — **похожий вызов**, возможно над разными объектами (заявки, операции); по нему D22 ищет повторяющиеся последовательности шагов. Для поиска повтора он не годится.

### 3.2 Класс ошибки `error_class`

Первое совпадение сверху вниз, без учёта регистра, по `error_text`:

| Класс | Признаки | Среда |
|---|---|---|
| `auth` | `401`, `403`, `unauthorized`, `forbidden`, `AUTH_REQUIRED`, `invalid (api )?key`, `invalid token`, `token expired` | да |
| `rate_limit` | `429`, `rate limit`, `too many requests`, `quota` | да |
| `network` | `ECONNREFUSED`, `ETIMEDOUT`, `ECONNRESET`, `could not resolve host`, `getaddrinfo`, `connection (refused|reset)` | да |
| `permission` | `EACCES`, `permission denied`, `operation not permitted`, `sandbox` + `denied` | да |
| `dependency` | `command not found`, `ModuleNotFoundError`, `no module named`, `cannot find (module|package)` | да |
| `not_found` | `ENOENT`, `no such file`, `404`, `not found` | да |
| `timeout` | `timed out`, `timeout`, `deadline exceeded` | нет |
| `test_failure` | `FAILED`, `AssertionError`, `\d+ failed`, `Tests:.*failed` | нет |
| `usage` | `SyntaxError`, `usage:`, `invalid (option|argument)`, `unknown (option|flag)` | нет |
| `other` | остальное | нет |

Сигнатура ошибки = `tool` + `error_class` + первые 200 символов `error_text` после замены чисел, путей и идентификаторов плейсхолдерами.

### 3.3 Изменение файлов `is_edit`

Успешный вызов `Edit`, `Write`, `MultiEdit`, `NotebookEdit`, `apply_patch`, а также shell-команда с записью в файл (`>`, `>>`, `tee`, `sed -i`, `mv`, `cp`, `rm`) или `git commit`.

### 3.4 Ожидание `is_wait`

Shell-команда или MCP-вызов, который опрашивает состояние: `sleep`, `watch`, `wait`, `status`, `gh run (view|watch)`, `kubectl (get|rollout status)`, `docker (ps|logs)`, `curl` к адресам `health|status|ready`, а также вызов с тем же `input_exact_hash`, между повторами которого паузы растут не меньше чем в 1.5 раза (backoff).

### 3.5 Класс реплики человека

Определяет быстрый разбор ([quick-review.md](quick-review.md)) по реплике, постановке и предыдущему ходу агента; короткие «ок», «да», «продолжай» — правилом, без модели. Глубокий разбор перепроверяет при уверенности ниже 0.6; человек главнее.

| Класс | Когда | Вмешательство |
|---|---|---|
| `correction` | агент неверно понял то, что было доступно: в постановке, в файлах, в прошлых репликах | да |
| `clarification` | человек добавляет данные, которых не было в постановке | да |
| `answer` | ответ на вопрос агента | да |
| `goal_change` | человек меняет цель после увиденного результата | да |
| `new_task` | новая задача в той же сессии | нет |
| `ack` | «да», «ок», «продолжай» без новых данных | нет |

### 3.6 Ход агента заканчивается вопросом

Структурно: `final_message` оканчивается на `?` или содержит обращение за решением («подтвердите», «какой вариант», «уточните») в последнем абзаце. Подтверждает быстрый разбор (`ends_with_question`).

### 3.7 Распознанная проверка

Вызов инструмента или результат, совпадающий с проверкой из [criteria.md](criteria.md) для класса задачи: команда проверки с кодом 0 и признаком успеха, созданный непустой артефакт, ответ внешней системы со статусом успеха.

### 3.8 Вмешательство человека

Реплика классов `correction`, `clarification`, `answer`, `goal_change`; решение человека по разрешению; прерывание.

## 4. Где это в ClickHouse

Хранилище сбора — ClickHouse в `otel-lab`, база `otel`; таблицы создаёт экспортёр Collector. Аналитическая часть читает его пользователем `reader` (только `SELECT`): сверка полей, приёмка детекторов, разбор эпизодов. Сборка сущностей §1 и хранение результатов §2 — техническая часть.

| Что | Где |
|---|---|
| События хуков | `otel.otel_logs`, `ServiceName = 'agent-hooks'`, `Body = 'agent.hook.<Событие>'` |
| Импортированная история (`sessions_ship`) | `otel.otel_logs`, `ServiceName = 'agent-backfill'`; `Timestamp` — время загрузки, время события — `LogAttributes['event.time']`; `session.id`, `event.seq`, `event.kind`, `source = backfill` |
| Поле payload хука, например `session_id`, `tool_name`, `tool_use_id`, `prompt` | `LogAttributes['<поле>']`; объекты — JSON-строкой, поле обрезано до 64 KB |
| Агент | `LogAttributes['agent']` = `claude` / `codex` |
| Машина и среда | `ResourceAttributes['host.name']`, `ResourceAttributes['deployment.environment']` |
| Нативные события агентов (`claude_code.*`, `codex.*`) | `otel.otel_logs`, `ServiceName` = `claude-code` / `codex`; имя события — в `Body` или `LogAttributes['event.name']`; поля — `LogAttributes`. Имена сервисов и событий сверить в A0 |
| Метрики (`token.usage`, `cost.usage`, `active_time.total`, `codex.turn.token_usage`, `codex.skill.injected`) | `otel.otel_metrics_sum`, `otel.otel_metrics_gauge`, `otel.otel_metrics_histogram`: `MetricName`, `Attributes`, `Value` |
| Трейсы | `otel.otel_traces` |
| Время события | `Timestamp` |

Collector маскирует до записи значения, похожие на ключи и токены; тексты промптов, аргументы и выводы инструментов лежат как пришли. Результаты запросов по своим сессиям — только в `local-data/`.

Пример — вызовы инструментов одной сессии из хуков:

```sql
SELECT Timestamp, Body,
       LogAttributes['tool_use_id'] AS call_id,
       LogAttributes['tool_name']   AS tool
FROM otel.otel_logs
WHERE ServiceName = 'agent-hooks'
  AND Body IN ('agent.hook.PreToolUse', 'agent.hook.PostToolUse', 'agent.hook.PostToolUseFailure')
  AND LogAttributes['session_id'] = {sid:String}
ORDER BY Timestamp;
```
