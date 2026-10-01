# hottell-coach, исправления живого дашборда, статус отправки, интерфейс v5.1 — план реализации

> **Для исполнителя (Claude):** обязательный навык — superpowers:executing-plans (или superpowers:subagent-driven-development в этой же сессии). Внутри PR задачи идут по порядку: почти все трогают общие файлы. Тесты пишутся первыми и должны упасть по ожидаемой причине.

**Спека:** `docs/specs/2026-10-01-hottell-coach.spec.md` (версия от 14:40, база `camp` от `dcfbb04`). Три пункта — три PR в `camp`, строго 1 → 2 → 3: пункты 1 и 2 правят `ui/static/index.html`.

**Цель.** (1) Дашборд не выдаёт готовые советы, а приводит человека в сессию Codex или Claude Code, где skill `hottell-coach` по данным локального MCP `hottell-local` разбирает с ним одну тему и после явного «да» вносит одно изменение с записью в журнал. (2) Живой дашборд перестаёт врать о полноте данных, коротких ID, длительности и времени запусков по расписанию. (3) Сервер дашборда знает состояние отправки в сервис команды. (4) Интерфейс дашборда — по пакету дизайна v5.1: цикл улучшений, темы, журнал коуча, строка отправки, пульс.

**Архитектура.** Методология — в `analytics/coach/SKILL.md`. Данные и журнал — в новом бинаре `hottell` (`temp/cmd/hottell`, модуль `git.alva.dev/alva/harness-telemetry`): команда `hottell mcp-local` запускает stdio-сервер `hottell-local` с инструментами `sessions_list`, `session_read`, `session_stats` (перенос из старого `hottell/`, плюс окно `from`/`to` и полнота выборки), `findings` и `coach_journal`. Логика — в чистых пакетах `internal/hottell/local/sessions` и `internal/hottell/local/coach`, MCP — только в `internal/hottell/local/mcpserver`. `hottell install` регистрирует `hottell-local` в конфигах обоих агентов, `hottell uninstall` снимает. Сервер дашборда (`ui/main.go`) отдаёт журнал (`/api/coach/journal`), раз в минуту — `hottell status --json` (`/api/status`) и пульс из ClickHouse (`/api/pulse`). Весь интерфейс (`ui/static/index.html`) меняется одним PR 4 по пакету дизайна v5.1, после данных. Старый бинарь `hottell/` не меняется.

**Стек.** Go 1.27.1 в `temp/` (go-sdk v1.8.0, zstd; golangci-lint и go-arch-lint), Go 1.25 stdlib в `ui/`, Python 3 stdlib + unittest в `ui/builder/`, одна HTML-страница с ванильным JS. Новых зависимостей модулей нет.

---

## Изменения после ревью (2026-10-01, второй агент)

| # | Замечание | Проверка | Что изменено |
|---|---|---|---|
| 1 | MCP запланирован в старом бинаре, а спека требует `temp/cmd/hottell`, `mcp-local` и имя `hottell-local` | Верно: спеку обновили в 14:40 (раздел «Данные: локальный MCP `hottell-local`», «Изменения в проекте») | Часть A переписана: пакеты в `temp/internal/hottell/local/`, команда `hottell mcp-local`, регистрация в `install`/`uninstall`, компоненты go-arch-lint (T1–T8). Старый `hottell/` не трогается |
| 2 | PR 3 делает отменённый пакет для хаба; нужен `/api/status` | Верно: спека, раздел 3 и «Не делать» | PR 3 заменён: `GET /api/status` по `hottell status --json` раз в минуту (T27–T30); строка в шапке — в интерфейсе v5.1 (T34, T39) |
| 3 | Находка возвращается за период по одному пересечению сессии с окном, хотя все доказательства старые | Верно: в прошлой версии `brief()` брал сессию, если она пересекалась с окном, даже без доказательств в нём | В период попадает только находка с доказательством, время которого внутри окна. Находки, у доказательств которых времени нет, — отдельным списком `undated` (T4) |
| 4 | После `not_enough_data` изменение больше не проверяется | Верно: методология брала только записи без `result` | Проверок может быть несколько, действует последняя; запись с `not_enough_data` проверяется снова, когда после прошлой проверки появились новые сессии агента (T5, T9) |
| 5 | Счётчики `session_read` неверны при пагинации | Верно: цикл обрывался на заполненной странице, `matched` и `out_of_period` считались только до неё | Счётчики считаются по всей сессии, страница набирается отдельно; тест на 251 событии (T3) |

Не из замечаний, но из новой спеки: вид и расположение элементов интерфейса задаёт `docs/specs/2026-10-01-ux.spec.md`, а точнее — пакет дизайна v5.1 (следующий раздел и PR 4).

## Изменения по прототипу интерфейса v5.1 (2026-10-01)

Пакет `design_handoff_hottell_dashboard_v5.1` (README — вид и поведение, финальные; HANDOFF — порядок: #14 → данные `/api/coach/journal` и `/api/status` → интерфейс и `/api/pulse`).

| Что в прототипе | Что меняется в плане |
|---|---|
| Весь интерфейс — одно задание, после данных | Задачи с правкой `index.html` переехали в новый PR 4 (T31–T39): T13, T14, T15 из PR 1; JS-часть T20, T21–T24 из PR 2; T29 из PR 3. В PR 1–3 остались данные и сборщик. Пункты 1 и 2 спеки больше не правят `index.html` по очереди — его правит только PR 4 |
| `GET /api/pulse` (ClickHouse, раз в 5 с, кэш 5 с, только loopback) | Новая задача T32 |
| «Исправлено»: `repeated` → «повторилось на N из M» | В журнал добавлено поле `repeats` у проверки `repeated` (T5, T9, T12; решение 18) |
| «Обсудить эту сессию» в ленте → `сессия <полный id>` | Новый вход SKILL.md (T9) |
| Период в реплике — по фильтру («период 14 дней», «весь период») | Решение 8 изменено; SKILL.md понимает «весь период» (T9) |
| Команда для терминала в двойных кавычках: `codex "$hottell-coach …"` | Не принимается: в zsh и bash `$hottell` подставится пустым. В PR 4 — одинарные кавычки (решение 1), владельцу — открытый вопрос |
| Служебные сессии: подсказки Codex «You are an expert at upholding safety and compliance standards for Codex ambient suggestions» | Новое правило в `live.py` — T21 PR 2 (вместо стартовой вкладки, ушедшей в PR 4) |
| Тема связывается с сигналом трения по совпадению названия | В PR 4 — по `pattern_id` (T34) |
| Стоимость темы и «Трения» — `friction.cost_usd`, только если все сессии сигнала в выборке, иначе «—» | Правка сборщика T24 (стоимость в карточке, `cost_by_session`) не нужна; приёмка пункта 2 про «Холодный кэш» — в T39 |
| «Исправлено» раскрывается в «Основания · Изменение · Проверка · Откат»; в демо-записях прототипа есть поле `basis` | Отдельного поля нет: «Основания» строятся из `topic` и `evidence` (решение 17) |
| Строка отправки при проблеме — «Отправка: ошибка · <проблема> · Подключение →» | Текст из README, в PR 4 (T34) |

## Что проверено при подготовке (2026-10-01)

| Факт | Где видно | Что следует для плана |
|---|---|---|
| PR #14 (`claude/live-honest-fix`) ещё открыт; спека требует базу «`camp` после его слияния» | `gh pr list` | T0: без слияния #14 не начинать |
| Номера строк спеки в пункте 2 не совпадают с веткой #14: `partial` — `live.py:815`, `"short"` — `live.py:1071`, `defaultView` — `index.html:750`, `short()` — `index.html:438` | `grep` по ветке | Места указаны по именам функций |
| Модуль `temp/` требует Go 1.27.1, локально 1.25.6 с `GOTOOLCHAIN=auto`; `golangci-lint`, `go-arch-lint`, `task` не установлены | `temp/go.mod`, `go version`, `which` | T0: загрузка тулчейна и установка линтеров — с согласия владельца |
| Линтеры `temp/`: запрещены глобальные переменные (`gochecknoglobals`), тесты — во внешнем пакете (`testpackage`, кроме `_internal_test.go`) и с `t.Parallel()` | `temp/.golangci.yml` | Регулярки создаются в конструкторах, каталоги передаются конфигом (никакого `t.Setenv`), тесты — `package x_test` |
| go-sdk разрешён только серверу сервиса и MCP-клиенту бинаря, zstd — только читателю транскриптов Codex | `temp/.go-arch-lint.yml` | T1: компоненты `hottelllocal` (zstd) и `hottellmcplocal` (go-sdk) |
| Codex переносит rollout в `archived_sessions/` и сжимает в `.jsonl.zst`; старый бинарь видит только `sessions/**/*.jsonl` | `temp/internal/hottell/transcript/codex/codex.go`, `hottell/sessions.go` | Перенос сессий читает оба каталога и `.zst` (T2) |
| `hottell install` нового бинаря останавливается, если в конфигах агентов нет удалённого `hottell` по HTTP (`mcpconfig.FindIn`); старые хуки `hottell -agent …` по тому же пути он считает чужими и снимает только с `--replace-foreign-hooks` | `temp/cmd/hottell/install.go` | На этой машине сейчас прототип (stdio `hottell`, хуки `-agent`) — для приёмки до перехода `hottell-local` регистрируется вручную (T8, путь Б) |
| Набор резервных копий `install` — `~/.claude/settings.json`, `~/.codex/config.toml`, `~/.codex/hooks.json`; `~/.claude.json` в нём нет | `temp/cmd/hottell/daemon.go` `agentBackups` | `~/.claude.json` правится точечно (одна запись), откат — `hottell uninstall` (решение 12) |
| Для правок есть готовые помощники: `claude.editSection` (JSON с сохранением порядка и атомарной записью), `codex.scan`/`readConfig`/`writeConfig`/`appendBlock` (TOML построчно) | `agentconfig/claude/file.go`, `agentconfig/codex/otel*.go` | Регистрация `hottell-local` строится на них (T7) |
| `hottell status --json`: `ok`, `mcp.url`, `token.last_sent`, `token.last_error`, `settings.version`, `queue.queued`, `problems`; при проблеме — код 1 и тот же JSON | `temp/cmd/hottell/status.go` | PR 3 (T28) |
| go-sdk v1.8.0: есть `mcp.IOTransport{Reader, Writer}`; jsonschema-go даёт указателям `null`, `map[string]any` — объект с любыми полями | кэш модулей | `mcp-local` работает поверх переданных потоков; вход `coach_journal` — `map` со строгим разбором |
| В доказательствах дашборда `line` — строка ленты дашборда (живой) или rollout (разобранный), а не `seq` | `ui/CONTRACT.md`, `live.py` `_events` | `findings` отдаёт `at` и `line_of`; skill находит `seq` через `session_read` с окном `at ± 2 с` |
| В реестре `proposals.json` у предложений `action` вместо заголовка, доказательства `L1157` без времени | `local-data/reports/v2/proposals.json` | Такие находки — в `undated` |
| Часовой пояс машины — WITA (+08:00) | `date +%Z` | В тестах окна — только RFC3339 с `Z` |
| `kpi()` показывает `note` только без сравнения с прошлым периодом | `index.html` `function kpi` | Интерфейс v5.1 переделывает KPI (T35) |
| Карточка «холодного кэша» показывает число эпизодов, «Трение» теряет стоимость под фильтром | `live.py` `work_findings`, `aggregate_friction`; `renderFriction` | Интерфейс v5.1 берёт стоимость из `friction.cost_usd` при полном охвате — правка сборщика не нужна (T24, T37) |
| `local-data/` в `.gitignore` | `git check-ignore` | Журнал и протоколы приёмки в git не попадают |

## Решения плана

1. **Команда для терминала — в одинарных кавычках.** `codex "$hottell-coach …"` в zsh и bash подставит пустую `$hottell` (так в прототипе v5.1). Кнопка показывает `codex '$hottell-coach …'` и `claude '/hottell-coach …'`; одинарные кавычки внутри — `'\''`.
2. **Итог проверки — отдельная запись журнала** с `check_of`. Проверок у изменения может быть несколько; `read` сворачивает в исходную запись последнюю (`result`, `observations`, `repeats`, `checked_at`, `checks` — сколько их было). `not_enough_data` не окончателен: skill проверяет такую запись снова, когда после `checked_at` появились сессии того же агента.
3. **`test` проверяется так же, как `applied`** — это тоже внесённое изменение; у него обязательны `check_after` и проверка.
4. **Полнота выборки.** `session_read` считает `matched_events` и `out_of_period` по всей сессии, независимо от страницы. `session_stats` получает `offset` и поле `coverage` (найдено, прочитано, в окне, отброшено, `next_offset`): иначе полноту нельзя назвать числом, а чтение всех сессий одним вызовом упирается в таймаут MCP у Codex.
5. **Без `since` период `session_stats` отбирает файлы по mtime ≥ `from`**: файл, изменённый до начала окна, событий окна не содержит.
6. **Маскирование в журнале на сервере — секреты и домашний каталог** (правила `redact()` из `ui/builder/telemetry.py`, `/Users/<имя>` → `~`). Пути клиентов и имена третьих лиц — правило skill.
7. **Тема «обзор»** у кнопок в начале «Что исправить»; skill понимает «обзор» и пустую тему одинаково.
8. **Период в реплике — по фильтру дашборда:** «период 7 дней» по умолчанию, «период 14 дней», «весь период» (прототип v5.1).
9. **Период находок — по времени доказательств.** Находка входит в период, только если у неё есть доказательство со временем внутри окна. Если ни у одного доказательства нет времени, находка идёт в отдельный список `undated` (когда её сессии пересекаются с окном): skill не цитирует её как основание периода, пока не найдёт событие окна через `session_read`.
10. **Раскладка кода:** `internal/hottell/local/sessions` (транскрипты, окно, чтение, агрегаты), `internal/hottell/local/coach` (находки, журнал, маскирование), `internal/hottell/local/mcpserver` (MCP), `cmd/hottell/mcplocal.go` (команда). Имя сервера — константа `mcpserver.Name`.
11. **Сессии Codex — из `sessions/` и `archived_sessions/`, включая `.jsonl.zst`**, как их читает сам новый бинарь; копия одного потока в двух местах — одна сессия (берётся более свежая).
12. **`~/.claude.json` не добавляется в набор резервных копий:** Claude Code постоянно его переписывает, и восстановление целиком откатило бы чужое состояние. `install` меняет в нём только `mcpServers.hottell-local`, `uninstall` снимает эту запись.
13. **Ошибка регистрации `hottell-local` не останавливает `install`:** отправка работает без коуча. Шаг печатается как сбой, код выхода 1.
14. **Статус отправки:** путь бинаря — флаг `-hottell` сервера дашборда (по умолчанию `~/.local/bin/hottell`); вывод не в JSON (так отвечает старый прототип) — «Отправка не настроена».
15. **Интерфейс — только PR 4,** по пакету v5.1; PR 1–3 не трогают `index.html`.
16. **Пульс:** первая реплика и проект идущей сессии — из `dataset.json` (сборщик их уже замаскировал и обрезал), из ClickHouse — только счёт и время; без `-clickhouse` пульса нет (:8800).
17. **«Основания» в «Исправлено»** — `topic` и ссылки `evidence`, отдельного поля в журнале нет.
18. **`repeats` у проверки `repeated`** — на скольких из `observations` задач сигнал вернулся (1…`observations`); у других итогов поля нет. Нужно для «повторилось на N из M».

## Открытые вопросы владельцу

| Вопрос | По умолчанию в плане |
|---|---|
| Когда эта машина переходит на сервис команды: запущенный сервис, ключ со страницы «Подключение», замена stdio-записи `hottell` прототипа на HTTP и `hottell install --replace-foreign-hooks` | До перехода `hottell-local` для приёмки регистрируется вручную из сборки ветки (T8, путь Б); прототип и его хуки не трогаются |
| Codex: срабатывает ли `$hottell-coach` в аргументе `codex '<реплика>'` | Проверяется в T10; если нет — реплика `Используй skill hottell-coach: <тема>, период 7 дней` |
| Приёмка T11 требует ответов человека о причине | Владелец участвует в пяти сессиях приёмки лично |
| Прототип v5.1 даёт команду для терминала в двойных кавычках | В PR 4 — одинарные (решение 1); если нужен вид как в прототипе — только с экранированием `\$` |

## Порядок и ветки

| PR | Ветка | Задачи | Условие начала |
|---|---|---|---|
| 1 | `claude/hottell-coach` | T0–T17 | #14 слит в `camp` |
| 2 | `claude/live-dashboard-fixes` | T18–T26 | PR 1 слит |
| 3 | `claude/send-status` | T27–T30 | PR 2 слит |
| 4 | `claude/dashboard-v5.1` | T31–T39 | PR 1–3 слиты |

Проверки модулей:
- `temp/`: `cd temp && go vet ./... && go test -race ./... && golangci-lint run && go-arch-lint check`
- `ui/`: `cd ui && go vet ./... && go test -count=1 ./...`
- сборщик: `cd ui/builder && python3 -m unittest -v`
- skill: `python3 -m unittest analytics.test_coach_skill -v` (из корня)

Коммиты заканчиваются строкой `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`, описания PR — строкой `🤖 Generated with [Claude Code](https://claude.com/claude-code)`.

---

# PR 1 — hottell-coach

## T0: Ветка и инструменты

**Шаг 1.** #14 слит:

```bash
gh pr view 14 --repo nuanu-ai/ai-hottell --json state,mergeCommit
```

Ожидается `"state":"MERGED"`. Иначе — остановиться и спросить владельца.

**Шаг 2.** Копия от свежего `camp` (навык superpowers:using-git-worktrees):

```bash
cd ~/Life/projects/ai-hottell
git fetch origin
git worktree add .claude/worktrees/hottell-coach -b claude/hottell-coach origin/camp
cd .claude/worktrees/hottell-coach
git log --oneline -1 -- temp   # dcfbb04 или новее
```

**Шаг 3.** Спека и план — неотслеживаемые в основной копии:

```bash
cp ~/Life/projects/ai-hottell/docs/specs/2026-10-01-hottell-coach.spec.md docs/specs/
cp ~/Life/projects/ai-hottell/docs/specs/2026-10-01-hottell-coach.plan.md docs/specs/
git add docs/specs/2026-10-01-hottell-coach.*.md
git commit -m "Спека и план: hottell-coach, исправления живого дашборда, статус отправки"
```

**Шаг 4. Инструменты `temp/`** — спросить владельца: «Загрузить Go 1.27.1 (тулчейн подтянется сам при первой сборке `temp/`) и поставить `golangci-lint` и `go-arch-lint` (`brew install golangci-lint`, `go install github.com/fe3dback/go-arch-lint@latest`)?». После «да»:

```bash
cd temp && go version   # go1.27.1 — загружен по GOTOOLCHAIN=auto
golangci-lint version && go-arch-lint version
```

Без линтеров задачи части A проверяются только `go vet` и тестами — сказать об этом в PR.

**Шаг 5.** Базовые проверки зелёные до правок:

```bash
(cd temp && go test -race ./... && golangci-lint run && go-arch-lint check) && (cd ui && go test -count=1 ./...) && (cd ui/builder && python3 -m unittest -q)
```

---

## Часть A — локальный MCP `hottell-local` в новом бинаре

## T1: Компоненты go-arch-lint

**Файл:** `temp/.go-arch-lint.yml`.

**Шаг 1.** В `vendors` обновить комментарии:

```yaml
  # The MCP SDK stays in the MCP server of the service, the hottell binary's MCP client and
  # its local MCP server hottell-local.
  go-sdk: { in: [github.com/modelcontextprotocol/go-sdk, github.com/modelcontextprotocol/go-sdk/**] }
  …
  # zstd unpacks the compressed Codex rollouts in the hottell binary's Codex transcript reader
  # and in the sessions hottell-local reads.
  zstd: { in: [github.com/klauspost/compress/zstd, github.com/klauspost/compress/zstd/**] }
```

**Шаг 2.** В `components` после `hottellmcpclient`:

```yaml
  # hottell-local reads this Mac's transcripts, the dashboard's findings and the coach's
  # journal; the more specific glob lets its session reader unpack Codex rollouts with zstd.
  hottelllocal: { in: internal/hottell/local/** }
  # The more specific glob lets the local MCP server use go-sdk.
  hottellmcplocal: { in: internal/hottell/local/mcpserver/** }
```

**Шаг 3.** В `deps`:

```yaml
  hottelllocal:
    mayDependOn: [hottelllocal, pkg]
    canUse: [zstd]
  hottellmcplocal:
    mayDependOn: [hottellmcplocal, hottelllocal, pkg]
    canUse: [go-sdk]
```

и в `hottellcmd.mayDependOn` добавить `hottelllocal, hottellmcplocal`.

**Шаг 4.** `cd temp && go-arch-lint check` — зелёный (пакетов ещё нет, `ignoreNotFoundComponents: true`).

**Шаг 5.** `git commit -am "arch-lint: компоненты локального MCP hottell-local"`

## T2: Пакет `local/sessions` — перенос чтения сессий

**Файлы:**
- Создать: `temp/internal/hottell/local/sessions/doc.go`, `transcript.go`, `list.go`, `text.go`
- Тесты: `temp/internal/hottell/local/sessions/transcript_test.go`, `list_test.go` (пакет `sessions_test`)

**Шаг 1. Перенос.** Скопировать код старого бинаря с переименованиями; логика разбора не меняется, JSON-теги — тоже (на них опирается SKILL.md):

| Было (`hottell/`) | Стало (`local/sessions/`) | Изменения |
|---|---|---|
| `transcript.go`: `tokenUsage`, `sessEvent`, `transcript`, `parseClaude`, `parseCodex`, `codexItem`, `codexUsage`, `blockText`, `jsonString`, `num`, `str0`, `firstNonEmpty`, `eachJSONLine`, `parseTS` | `transcript.go`: `TokenUsage` (метод `Add`), `Event`, `Transcript`, `ParseTS`; остальное — неэкспортируемое как было | `parseTranscriptFile(si)` → `Parse(si Info) (*Transcript, error)`, открывает файл через `open(si)` (шаг 3) |
| `sessions.go`: `sessionInfo`, `sessionFilter`, `parseWhen`, `listSessions`, `findSession`, `listClaudeSessions`, `listCodexSessions`, `statSession`, `codexFirstCwd`, `encodeClaudeProject`, `projectMatches` | `list.go`: `Info`, `Filter`, `ParseWhen`, `List`, `Find`; остальное — неэкспортируемое | Каталоги — из `Roots`, не из `expandHome`; `now` — параметр; `Filter.Offset`; Codex — шаг 2 |
| `otlp.go`: `truncate` | `text.go`: `Truncate` | — |

Комментарии в коде `temp/` — по-английски, как вокруг. В `doc.go`:

```go
// Package sessions reads the transcripts of Claude Code and Codex on this Mac into one
// stream of events per session for hottell-local: the inventory, one session page by
// page within a time window, and aggregates of a session or of a period. Nothing it
// reads leaves the machine.
package sessions
```

Новые типы в `list.go`:

```go
// Roots are the directories the transcripts are under.
type Roots struct {
	// ClaudeProjects is <claude config dir>/projects.
	ClaudeProjects string
	// CodexHome is ~/.codex or CODEX_HOME: rollouts are under sessions/ and archived_sessions/.
	CodexHome string
}
```

`Info` получает поле `Compressed bool \`json:"compressed,omitempty"\``. `Filter` — поле:

```go
	Offset int `json:"offset,omitempty" jsonschema:"how many sessions of the list to skip (pages); 0 by default"`
```

Сигнатуры: `List(r Roots, f Filter, now time.Time) ([]Info, int, error)` (после сортировки — `out = out[min(f.Offset, len(out)):]`, затем `limit`; `total` — до `offset`), `Find(r Roots, agent, id string, now time.Time) (Info, error)` (путь к файлу принимается и с `.jsonl.zst`).

**Шаг 2. Codex: оба каталога и сжатые rollout** — заменить `listCodexSessions` на:

```go
// codexRollout reports whether name is a Codex rollout, rollout-<time>-<thread>.jsonl or
// the same compressed to .jsonl.zst, with its thread id and start.
func codexRollout(name string) (id string, started time.Time, zst, ok bool) {
	base, zst := strings.CutSuffix(name, ".zst")
	base, ok = strings.CutSuffix(base, ".jsonl")
	if !ok {
		return "", time.Time{}, false, false
	}
	rest, ok := strings.CutPrefix(base, "rollout-")
	if !ok || len(rest) <= 20 {
		return "", time.Time{}, false, false
	}
	if t, err := time.ParseInLocation("2006-01-02T15-04-05", rest[:19], time.Local); err == nil {
		started = t
	}
	return strings.TrimPrefix(rest[19:], "-"), started, zst, true
}

// listCodex lists the rollouts under sessions/ and archived_sessions/. Codex moves a rollout
// to the archive and compresses it; a thread found twice is the copy changed last.
func listCodex(home string) []Info {
	byID := map[string]Info{}
	for _, dir := range []string{"sessions", "archived_sessions"} {
		_ = filepath.WalkDir(filepath.Join(home, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil //nolint:nilerr // a missing or unreadable directory has no rollouts
			}
			id, started, zst, ok := codexRollout(d.Name())
			if !ok {
				return nil
			}
			si, ok := statSession("codex", path, "")
			if !ok {
				return nil
			}
			si.ID, si.Started, si.Compressed = id, started, zst
			si.Project = codexFirstCwd(si)
			if prev, dup := byID[id]; !dup || si.Modified.After(prev.Modified) {
				byID[id] = si
			}
			return nil
		})
	}
	out := make([]Info, 0, len(byID))
	for _, si := range byID {
		out = append(out, si)
	}
	return out
}
```

`codexFirstCwd(si Info)` читает первую строку через `open(si)`.

**Шаг 3. Открытие с распаковкой** — в `transcript.go`:

```go
// open returns the lines of the transcript, unpacking a compressed Codex rollout.
func open(si Info) (io.ReadCloser, error) {
	f, err := os.Open(si.Path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", si.Path, err)
	}
	if !si.Compressed {
		return f, nil
	}
	z, err := zstd.NewReader(f)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("unpack %s: %w", si.Path, err)
	}
	return zstdFile{Decoder: z, f: f}, nil
}

// zstdFile closes both the decoder and the file under it.
type zstdFile struct {
	*zstd.Decoder
	f *os.File
}

func (z zstdFile) Close() error {
	z.Decoder.Close()
	return z.f.Close()
}
```

**Шаг 4. Тесты.** Перенести из `hottell/sessions_test.go` фикстуры `claudeFixture`, `codexFixture`, `codexOldFixture` и тесты `TestSessionsList`, `TestParseClaude`, `TestParseCodex`, `TestRealSessions` в `list_test.go`/`transcript_test.go` (пакет `sessions_test`, каждый тест — `t.Parallel()`). `fakeHome` заменить на хелпер `writeRoots(t *testing.T) sessions.Roots`, который пишет те же фикстуры в `t.TempDir()` (`<tmp>/.claude/projects/…`, `<tmp>/.codex/sessions/…`) и возвращает каталоги; им же пользуются тесты T3; разбор — через `sessions.Parse(sessions.Info{Agent: …, Path: …})`. Новый тест:

```go
func TestCodexArchivedAndCompressed(t *testing.T) {
	t.Parallel()
	roots := writeRoots(t) // фикстуры как в TestSessionsList
	dir := filepath.Join(roots.CodexHome, "archived_sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	enc, _ := zstd.NewWriter(nil)
	packed := enc.EncodeAll([]byte(codexFixture), nil)
	name := "rollout-2026-09-20T10-00-00-019e-arch.jsonl.zst"
	if err := os.WriteFile(filepath.Join(dir, name), packed, 0o600); err != nil {
		t.Fatal(err)
	}
	// тот же поток ещё и несжатым в sessions/ — одна сессия, а не две
	old := filepath.Join(roots.CodexHome, "sessions", "2026", "09", "20", "rollout-2026-09-20T10-00-00-019e-arch.jsonl")
	os.MkdirAll(filepath.Dir(old), 0o700)
	os.WriteFile(old, []byte(codexFixture), 0o600)
	past := time.Now().Add(-time.Hour)
	os.Chtimes(old, past, past)

	list, _, err := sessions.List(roots, sessions.Filter{Agent: "codex", Limit: 100}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var arch []sessions.Info
	for _, si := range list {
		if si.ID == "019e-arch" {
			arch = append(arch, si)
		}
	}
	if len(arch) != 1 || !arch[0].Compressed || arch[0].Project != "/Users/a/3d" {
		t.Fatalf("архивный сжатый rollout: %+v", arch)
	}
	tr, err := sessions.Parse(arch[0])
	if err != nil || len(tr.Events) == 0 {
		t.Fatalf("разбор .zst: %v %d", err, len(tr.Events))
	}
}
```

**Шаг 5.**

```bash
cd temp && go test -race ./internal/hottell/local/... && golangci-lint run ./internal/hottell/local/... && go-arch-lint check
```

**Шаг 6.** `git add temp/internal/hottell/local/sessions && git commit -m "hottell: чтение сессий для hottell-local — перенос из прототипа, архивные и сжатые rollout Codex"`

## T3: Окно `from`/`to`, страницы и полнота выборки

**Файлы:**
- Создать: `temp/internal/hottell/local/sessions/period.go`, `read.go`, `stats.go`
- Тесты: `period_test.go`, `read_test.go`, `stats_test.go` (пакет `sessions_test`)

**Шаг 1. Падающие тесты.** `period_test.go`:

```go
package sessions_test

import (
	"testing"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/local/sessions"
)

func TestParsePeriod(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	p, err := sessions.ParsePeriod("168h", "", now)
	if err != nil || !p.From.Equal(now.Add(-168*time.Hour)) || !p.To.IsZero() {
		t.Fatalf("168h: %+v %v", p, err)
	}
	p, err = sessions.ParsePeriod("", "2026-09-23", now) // дата в to — включительно, в местном времени
	want, _ := time.ParseInLocation("2006-01-02", "2026-09-24", time.Local)
	if err != nil || !p.To.Equal(want) {
		t.Fatalf("to: %+v %v", p, err)
	}
	if _, err := sessions.ParsePeriod("2026-09-02T00:00:00Z", "2026-09-01T00:00:00Z", now); err == nil {
		t.Fatal("from после to — ошибка")
	}
	if p, _ := sessions.ParsePeriod("", "", now); p.Set() || !p.Has(time.Time{}) {
		t.Fatal("пустое окно пропускает всё")
	}
	p, _ = sessions.ParsePeriod("2026-09-01T00:00:00Z", "2026-09-02T00:00:00Z", now)
	in, edge := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	if !p.Has(in) || p.Has(edge) || p.Has(time.Time{}) {
		t.Fatal("окно [from, to): from внутри, to снаружи, без времени — снаружи")
	}
	if !p.Overlaps(in.Add(-time.Hour), in.Add(time.Hour)) || p.Overlaps(edge, edge.Add(time.Hour)) {
		t.Fatal("пересечение отрезка с окном")
	}
}
```

`read_test.go` — счётчики по всей сессии при страницах (замечание 5) и граница периода:

```go
// longClaude — сессия Claude Code: одна реплика из июня и 250 — 30 сентября.
func longClaude(t *testing.T) sessions.Roots {
	t.Helper()
	roots := sessions.Roots{ClaudeProjects: filepath.Join(t.TempDir(), "projects"), CodexHome: t.TempDir()}
	var b strings.Builder
	line := func(text string, at time.Time) {
		fmt.Fprintf(&b, `{"type":"user","message":{"role":"user","content":%q},"timestamp":%q,"sessionId":"long"}`+"\n",
			text, at.Format(time.RFC3339Nano))
	}
	line("старая реплика", time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC))
	base := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	for i := range 250 {
		line(fmt.Sprintf("реплика %d", i), base.Add(time.Duration(i)*time.Minute))
	}
	p := filepath.Join(roots.ClaudeProjects, "-w-long", "long.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return roots
}

func TestReadCountersCoverTheWholeSession(t *testing.T) {
	t.Parallel()
	roots, now := longClaude(t), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	in := sessions.ReadIn{ID: "long", Agent: "claude", From: "2026-09-29T00:00:00Z"}
	first, err := sessions.Read(roots, in, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.Total != 251 || first.Matched != 250 || first.OutOfPeriod != 1 || len(first.Events) != 100 || first.Next != 101 {
		t.Fatalf("страница 1: total=%d matched=%d out=%d n=%d next=%d", first.Total, first.Matched, first.OutOfPeriod, len(first.Events), first.Next)
	}
	in.Offset = 201
	last, err := sessions.Read(roots, in, now)
	if err != nil {
		t.Fatal(err)
	}
	if last.Matched != 250 || last.OutOfPeriod != 1 || len(last.Events) != 50 || last.Next != 0 {
		t.Fatalf("последняя страница: matched=%d out=%d n=%d next=%d", last.Matched, last.OutOfPeriod, len(last.Events), last.Next)
	}
	for _, ev := range append(first.Events, last.Events...) {
		if ev.TS.Before(time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)) {
			t.Fatalf("событие до окна: %+v", ev)
		}
	}
}
```

`stats_test.go` — агрегат периода по фикстурам T2 (файлы свежие, события из августа и сентября):

```go
func TestStatsPeriodCoverageAndPages(t *testing.T) {
	t.Parallel()
	roots, now := writeRoots(t), time.Now()
	st, _, err := sessions.StatsOf(roots, sessions.StatsIn{From: "2026-09-23T00:00:00Z", To: "2026-09-24T00:00:00Z"}, now)
	if err != nil {
		t.Fatal(err)
	}
	c := st.Coverage
	// по mtime подходят 3 файла, событий окна — только у 019e-codex-1
	if c == nil || c.SessionsFound != 3 || c.SessionsRead != 3 || c.SessionsInPeriod != 1 || c.EventsOutOfPeriod == 0 || c.NextOffset != 0 {
		t.Fatalf("coverage: %+v", c)
	}
	if st.Sessions != 1 || len(st.PerSession) != 1 || st.PerSession[0].ID != "019e-codex-1" || c.EventsInPeriod != st.Events {
		t.Fatalf("stats: %+v", st.PerSession)
	}
	p1, note, _ := sessions.StatsOf(roots, sessions.StatsIn{Filter: sessions.Filter{Limit: 1}}, now)
	if p1.Coverage.SessionsRead != 1 || p1.Coverage.NextOffset != 1 || !strings.Contains(note, "offset=1") {
		t.Fatalf("страница 1: %+v %q", p1.Coverage, note)
	}
	p3, _, _ := sessions.StatsOf(roots, sessions.StatsIn{Filter: sessions.Filter{Limit: 1, Offset: 2}}, now)
	if p3.Coverage.SessionsRead != 1 || p3.Coverage.NextOffset != 0 {
		t.Fatalf("последняя страница: %+v", p3.Coverage)
	}
}
```

Если в `TestSessionsList` фикстура содержит иное число сессий без субагентов, чем 3, — поправить ожидание под неё, а не код.

**Шаг 2.** `cd temp && go test -race ./internal/hottell/local/sessions/` — падает: `undefined: sessions.ParsePeriod`, `sessions.Read`, `sessions.StatsOf`.

**Шаг 3. `period.go`:**

```go
package sessions

import (
	"errors"
	"fmt"
	"time"
)

// Period is a window over the time of the events themselves, [From, To). since and until
// of List look at the file's mtime instead, and a fresh file can hold month-old replies:
// Codex appends a resumed session to its old rollout.
type Period struct {
	From time.Time
	To   time.Time
}

// ParsePeriod reads from and to like since and until: YYYY-MM-DD, RFC3339 or a duration
// back from now (168h). A date in to is inclusive.
func ParsePeriod(from, to string, now time.Time) (Period, error) {
	f, err := ParseWhen(from, now)
	if err != nil {
		return Period{}, fmt.Errorf("from: %w", err)
	}
	t, err := ParseWhen(to, now)
	if err != nil {
		return Period{}, fmt.Errorf("to: %w", err)
	}
	if to != "" && len(to) == len("2006-01-02") {
		t = t.Add(24 * time.Hour)
	}
	if !f.IsZero() && !t.IsZero() && !f.Before(t) {
		return Period{}, errors.New("from must be earlier than to")
	}
	return Period{From: f, To: t}, nil
}

// Set reports whether the window bounds anything.
func (p Period) Set() bool { return !p.From.IsZero() || !p.To.IsZero() }

// Has reports whether an event at ts is inside the window. With a window set, an event
// without a time is outside: nothing proves it belongs to the period.
func (p Period) Has(ts time.Time) bool {
	if !p.Set() {
		return true
	}
	if ts.IsZero() {
		return false
	}
	return (p.From.IsZero() || !ts.Before(p.From)) && (p.To.IsZero() || ts.Before(p.To))
}

// Overlaps reports whether [a, b] meets the window; unknown ends meet a set window never.
func (p Period) Overlaps(a, b time.Time) bool {
	if !p.Set() {
		return true
	}
	if a.IsZero() || b.IsZero() {
		return false
	}
	return (p.From.IsZero() || !b.Before(p.From)) && (p.To.IsZero() || a.Before(p.To))
}

// inPeriod returns the events inside the window and how many it dropped.
func inPeriod(evs []Event, p Period) ([]Event, int) {
	if !p.Set() {
		return evs, 0
	}
	out := make([]Event, 0, len(evs))
	for _, ev := range evs {
		if p.Has(ev.TS) {
			out = append(out, ev)
		}
	}
	return out, len(evs) - len(out)
}
```

**Шаг 4. `read.go`:**

```go
package sessions

import "time"

// ReadIn selects one session's events, page by page.
type ReadIn struct {
	Agent   string   `json:"agent,omitempty" jsonschema:"claude or codex; helps when the id is ambiguous"`
	ID      string   `json:"id" jsonschema:"session id, a unique prefix of it, or the path of the transcript"`
	Offset  int      `json:"offset,omitempty" jsonschema:"first event (seq) of the page; 0 by default"`
	Limit   int      `json:"limit,omitempty" jsonschema:"events per page; 100 by default"`
	Kinds   []string `json:"kinds,omitempty" jsonschema:"only these kinds: user_message, assistant_message, reasoning, tool_call, tool_result, system, attachment, token_usage, turn_start, turn_end, compact"`
	MaxText int      `json:"max_text,omitempty" jsonschema:"cut texts to this many bytes; 0 means 2000, -1 means no cut"`
	From    string   `json:"from,omitempty" jsonschema:"only events at or after: YYYY-MM-DD, RFC3339 or a duration back (168h)"`
	To      string   `json:"to,omitempty" jsonschema:"only events before: YYYY-MM-DD (inclusive) or RFC3339"`
}

// ReadOut is one page. The counters cover the whole session, not the page.
type ReadOut struct {
	Session     Info        `json:"session"`
	Meta        *Transcript `json:"meta"`
	Total       int         `json:"total_events"`
	Matched     int         `json:"matched_events"` // of the kinds asked, inside the window
	OutOfPeriod int         `json:"out_of_period"`  // of the kinds asked, outside the window
	Next        int         `json:"next_offset,omitempty"`
	Events      []Event     `json:"events"`
}

// Read returns one page of a session's events within the window.
func Read(r Roots, in ReadIn, now time.Time) (ReadOut, error) {
	per, err := ParsePeriod(in.From, in.To, now)
	if err != nil {
		return ReadOut{}, err
	}
	si, err := Find(r, in.Agent, in.ID, now)
	if err != nil {
		return ReadOut{}, err
	}
	tr, err := Parse(si)
	if err != nil {
		return ReadOut{}, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 100
	}
	maxText := in.MaxText
	if maxText == 0 {
		maxText = 2000
	}
	want := map[string]bool{}
	for _, k := range in.Kinds {
		want[k] = true
	}
	out := ReadOut{Session: si, Meta: tr, Total: len(tr.Events), Events: []Event{}}
	for _, ev := range tr.Events {
		if len(want) > 0 && !want[ev.Kind] {
			continue
		}
		if !per.Has(ev.TS) {
			out.OutOfPeriod++
			continue
		}
		out.Matched++
		if ev.Seq < in.Offset {
			continue
		}
		if len(out.Events) == limit { // the page is full; keep counting
			if out.Next == 0 {
				out.Next = ev.Seq
			}
			continue
		}
		if maxText > 0 {
			ev.Text = Truncate(ev.Text, maxText)
		}
		out.Events = append(out.Events, ev)
	}
	return out, nil
}
```

**Шаг 5. `stats.go`.** Перенести из `hottell/session_tools.go` `toolStat`, `sessionStats`, `sessionBrief`, `newStats`, `addTranscript`, `finishTools` (как `ToolStat`, `Stats`, `Brief`, неэкспортируемые функции) и добавить:

```go
// StatsIn asks for one session (ID) or for a period (the filter and the window).
type StatsIn struct {
	Filter
	ID   string `json:"id,omitempty" jsonschema:"one session; without id the aggregate of the period"`
	From string `json:"from,omitempty" jsonschema:"count only events at or after: YYYY-MM-DD, RFC3339 or a duration back (168h); without since the files are those changed at or after from"`
	To   string `json:"to,omitempty" jsonschema:"count only events before: YYYY-MM-DD (inclusive) or RFC3339"`
}

// Coverage tells how complete the aggregate is.
type Coverage struct {
	SessionsFound     int `json:"sessions_found"`     // files matching agent, since/until, project
	SessionsRead      int `json:"sessions_read"`      // read on this page (offset, limit)
	SessionsInPeriod  int `json:"sessions_in_period"` // of those, with events inside from/to
	EventsInPeriod    int `json:"events_in_period"`
	EventsOutOfPeriod int `json:"events_out_of_period"`
	Unreadable        int `json:"unreadable,omitempty"`
	NextOffset        int `json:"next_offset,omitempty"` // 0 when every found session was read
}
```

`Stats` получает последним полем `Coverage *Coverage \`json:"coverage,omitempty"\``. Функция:

```go
// StatsOf aggregates one session or a period. note says how to read the next page, if any.
func StatsOf(r Roots, in StatsIn, now time.Time) (*Stats, string, error) {
	per, err := ParsePeriod(in.From, in.To, now)
	if err != nil {
		return nil, "", err
	}
	st, tools := newStats(), map[string]*ToolStat{}
	if in.ID != "" {
		si, err := Find(r, in.Agent, in.ID, now)
		if err != nil {
			return nil, "", err
		}
		tr, err := Parse(si)
		if err != nil {
			return nil, "", err
		}
		var skipped int
		tr.Events, skipped = inPeriod(tr.Events, per)
		if per.Set() {
			st.Coverage = &Coverage{SessionsFound: 1, SessionsRead: 1, EventsInPeriod: len(tr.Events), EventsOutOfPeriod: skipped}
			if len(tr.Events) > 0 {
				st.Coverage.SessionsInPeriod = 1
			}
		}
		st.addTranscript(tr, tools)
		finishTools(st, tools)
		return st, "", nil
	}
	f := in.Filter
	if f.Limit <= 0 {
		f.Limit = 20
	}
	if f.Since == "" && in.From != "" {
		f.Since = in.From // a file changed before the window opened has no events in it
	}
	list, total, err := List(r, f, now)
	if err != nil {
		return nil, "", err
	}
	cov := &Coverage{SessionsFound: total, SessionsRead: len(list)}
	if f.Offset+len(list) < total {
		cov.NextOffset = f.Offset + len(list)
	}
	for _, si := range list {
		tr, err := Parse(si)
		if err != nil {
			cov.Unreadable++
			continue
		}
		var skipped int
		tr.Events, skipped = inPeriod(tr.Events, per)
		cov.EventsOutOfPeriod += skipped
		if per.Set() && len(tr.Events) == 0 {
			continue
		}
		cov.SessionsInPeriod++
		cov.EventsInPeriod += len(tr.Events)
		st.PerSession = append(st.PerSession, st.addTranscript(tr, tools))
	}
	st.Coverage = cov
	finishTools(st, tools)
	note := ""
	if cov.NextOffset > 0 {
		note = fmt.Sprintf("%d of %d matching sessions counted; next page: offset=%d", len(list), total, cov.NextOffset)
	}
	return st, note, nil
}
```

**Шаг 6.** Тесты, линтер, архитектура — зелёные (команда из T2, шаг 5).

**Шаг 7.** `git add temp/internal/hottell/local/sessions && git commit -m "hottell-local: окно from/to по времени событий, счётчики по всей сессии, полнота выборки"`

## T4: `local/coach` — находки дашборда за период

**Файлы:**
- Создать: `temp/internal/hottell/local/coach/doc.go`, `findings.go`
- Тест: `temp/internal/hottell/local/coach/findings_test.go` (пакет `coach_test`)

**Шаг 1. Падающий тест** `findings_test.go`:

```go
package coach_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/local/coach"
)

const (
	sidNew  = "019f0000-0000-7000-8000-00000000000a" // живой, в периоде
	sidOld  = "019f0000-0000-7000-8000-00000000000b" // живой, до периода
	sidAuto = "019f0000-0000-7000-8000-00000000000c" // запуск по расписанию
	sidLong = "019f0000-0000-7000-8000-00000000000e" // начата в июне, продолжена в сентябре
	sidRev  = "019f0000-0000-7000-8000-00000000000d" // разобранный
)

type m = map[string]any

func ev(sid string, line int, at, text string) m { return m{"sid": sid, "line": line, "at": at, "text": text} }

// dashboardData — каталог данных дашборда: оба набора и реестр предложений.
func dashboardData(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	put := func(rel string, v any) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(v)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	put("ui-live/dataset.json", m{
		"variant": "live", "generated_at": "2026-10-01T06:00:00Z",
		"window": m{"from": "2026-06-01T00:00:00Z", "to": "2026-10-01T06:00:00Z"},
		"sessions": []m{
			{"id": sidNew, "kind": "user", "start": "2026-09-30T08:00:00Z", "end": "2026-09-30T09:00:00Z"},
			{"id": sidOld, "kind": "user", "start": "2026-09-21T08:00:00Z", "end": "2026-09-21T09:00:00Z"},
			{"id": sidAuto, "kind": "automation", "start": "2026-09-30T10:00:00Z", "end": "2026-09-30T17:00:00Z"},
			{"id": sidLong, "kind": "user", "start": "2026-06-10T08:00:00Z", "end": "2026-09-30T12:00:00Z"},
		},
		"findings": []m{
			{"id": "live:new", "title": "Разобрать эпизоды «повтор вызова»", "kind": "habit", "pattern_id": "retry", "sev": "warn",
				"readiness": "hypothesis", "sessions": []string{sidNew},
				"ev": []m{ev(sidNew, 10, "2026-09-30T08:10:00Z", "1/4"), ev(sidNew, 11, "2026-09-30T08:11:00Z", "2/4"),
					ev(sidNew, 12, "2026-09-30T08:12:00Z", "3/4"), ev(sidNew, 13, "2026-09-30T08:13:00Z", "4/4")}},
			{"id": "live:old", "title": "Старое", "kind": "diagnostic", "sessions": []string{sidOld},
				"ev": []m{ev(sidOld, 3, "2026-09-21T08:05:00Z", "старое")}},
			// замечание 3: сессия пересекается с окном, но единственное доказательство — июньское
			{"id": "live:june", "title": "Июньское", "kind": "habit", "sessions": []string{sidLong},
				"ev": []m{ev(sidLong, 5, "2026-06-10T08:30:00Z", "июнь")}},
			{"id": "live:health", "title": "Нет PostToolUse", "kind": "diagnostic", "scope": "collection",
				"sessions": []string{sidNew}, "ev": []m{}},
		},
		"friction": []m{{"key": "coldcache", "name": "Холодный кэш", "sev": "warn", "sessions": []string{sidNew},
			"evidence": []m{ev(sidNew, 20, "2026-09-30T08:30:00Z", "пауза 12 мин")}}},
		"skills": m{"rows": []m{
			{"name": "playwright", "state": "used", "activations": 2, "sessions": []string{sidNew},
				"evidence": []m{ev(sidNew, 30, "2026-09-30T08:40:00Z", "skill")}},
			{"name": "pdf", "state": "used", "activations": 1, "sessions": []string{sidLong},
				"evidence": []m{ev(sidLong, 7, "2026-06-10T09:00:00Z", "skill")}},
		}},
	})
	put("ui/dataset.json", m{
		"generated_at": "2026-09-30T15:00:00Z",
		"sessions":     []m{{"id": sidRev, "start": "2026-09-29T10:00:00Z", "end": "2026-09-30T11:00:00Z"}},
		"findings": []m{{"id": "p2:a", "title": "План назван готовым до ревью", "kind": "skill", "pattern_id": "D05", "sev": "bad",
			"readiness": "needs_spec", "decision": "not_requested", "execution": "not_applied", "scope": "Сверка плана",
			"sessions": []string{sidRev}, "ev": []m{ev(sidRev, 1157, "2026-09-30T10:35:41.515Z", "план готов")}}},
	})
	put("reports/v2/proposals.json", m{"kind": "proposal_registry", "schema_version": 2, "proposals": []m{
		{"proposal_id": "p2:a", "pattern_id": "D05", "change_type": "skill", "action": "Сверить план",
			"decision": m{"status": "approved"}, "execution": m{"status": "not_applied"}, "readiness": m{"status": "needs_specification"},
			"sources": []m{{"session_id": sidRev, "evidence": []string{"L1157"}}}},
		{"proposal_id": "p2:b", "pattern_id": "D12", "change_type": "workflow", "action": "Записывать критерий готовности",
			"decision": m{"status": "not_requested"}, "execution": m{"status": "not_applied"}, "readiness": m{"status": "hypothesis"},
			"sources": []m{{"session_id": sidRev, "evidence": []string{"L42", "L43"}}}},
	}})
	return dir
}

var now = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC) //nolint:gochecknoglobals // fixed clock of the tests

func byID(fs []coach.FindingBrief) map[string]coach.FindingBrief {
	out := map[string]coach.FindingBrief{}
	for _, f := range fs {
		out[f.ID] = f
	}
	return out
}

func TestFindingsNeedGroundsInsideTheWindow(t *testing.T) {
	t.Parallel()
	out, err := coach.Findings(dashboardData(t), coach.FindingsIn{Since: "2026-09-29T00:00:00Z", Until: "2026-10-02T00:00:00Z"}, now)
	if err != nil {
		t.Fatal(err)
	}
	in, undated := byID(out.Findings), byID(out.Undated)
	for _, id := range []string{"live:old", "live:june"} {
		if _, ok := in[id]; ok {
			t.Errorf("%s: доказательств в окне нет, а находка попала в период", id)
		}
		if _, ok := undated[id]; ok {
			t.Errorf("%s: время доказательств известно — это не undated", id)
		}
	}
	nw := in["live:new"]
	if nw.Source != "live" || len(nw.Evidence) != 3 || nw.EvidenceTotal != 4 || nw.Evidence[0].LineOf != "live_timeline" {
		t.Fatalf("live:new: %+v", nw)
	}
	if fr := in["friction:coldcache"]; fr.Kind != "friction" || len(fr.Evidence) != 1 {
		t.Fatalf("friction: %+v", fr)
	}
	a := in["p2:a"]
	if a.Source != "reviewed" || a.Decision != "approved" || a.Evidence[0].LineOf != "rollout" || a.Evidence[0].Line != 1157 {
		t.Fatalf("p2:a: статусы — из реестра: %+v", a)
	}
	b := undated["p2:b"] // только в реестре, у доказательств нет времени
	if b.Title != "Записывать критерий готовности" || len(b.Evidence) != 2 || b.Evidence[0].Line != 42 || len(b.Sessions) != 1 {
		t.Fatalf("p2:b: %+v", b)
	}
	if h, ok := undated["live:health"]; !ok || !h.Collection {
		t.Fatalf("находка без доказательств — в undated, с пометкой collection: %+v", h)
	}
	if len(out.Skills) != 1 || out.Skills[0].Name != "playwright" || out.Skills[0].EvidenceInPeriod != 1 {
		t.Fatalf("skills — по доказательствам окна: %+v", out.Skills)
	}
	if len(out.ServiceSessions) != 1 || out.ServiceSessions[0].ID != sidAuto {
		t.Fatalf("service: %+v", out.ServiceSessions)
	}
	for _, s := range out.Sources {
		if s.State != "ok" {
			t.Fatalf("%s: %s %s", s.Name, s.State, s.Error)
		}
	}
}

func TestFindingsWithoutWindowByIDAndSource(t *testing.T) {
	t.Parallel()
	dir := dashboardData(t)
	all, _ := coach.Findings(dir, coach.FindingsIn{}, now)
	if _, ok := byID(all.Findings)["live:june"]; !ok || len(all.Undated) != 0 {
		t.Fatalf("без окна — все находки, undated пуст: %d %d", len(all.Findings), len(all.Undated))
	}
	one, _ := coach.Findings(dir, coach.FindingsIn{ID: "friction:coldcache"}, now)
	if len(one.Findings) != 1 || one.Findings[0].ID != "friction:coldcache" {
		t.Fatalf("по id: %+v", one.Findings)
	}
	live, _ := coach.Findings(dir, coach.FindingsIn{Source: "live"}, now)
	if len(live.Sources) != 1 || live.Sources[0].Name != "live" {
		t.Fatalf("source=live: %+v", live.Sources)
	}
	if _, err := coach.Findings(dir, coach.FindingsIn{Source: "both"}, now); err == nil {
		t.Fatal("неверный source — ошибка")
	}
}

func TestFindingsMissingFiles(t *testing.T) {
	t.Parallel()
	out, err := coach.Findings(t.TempDir(), coach.FindingsIn{}, now)
	if err != nil || len(out.Findings) != 0 || len(out.Sources) != 3 {
		t.Fatalf("без файлов: %+v %v", out, err)
	}
	for _, s := range out.Sources {
		if s.State != "missing" {
			t.Fatalf("%s: %s", s.Name, s.State)
		}
	}
}
```

**Шаг 2.** `cd temp && go test -race ./internal/hottell/local/coach/` — падает: `undefined: coach.Findings`.

**Шаг 3. `doc.go`:**

```go
// Package coach holds what hottell-local gives the hottell-coach skill besides the
// sessions: the findings of the dashboard's datasets within a period, and the coach's
// journal of decisions.
package coach
```

**Шаг 4. `findings.go`:**

```go
package coach

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/local/sessions"
)

// maxEvidence is how many pieces of evidence a finding carries at most.
const maxEvidence = 3

// What is read: only the fields of the datasets the findings need (ui/CONTRACT.md).

type dsEvidence struct {
	SID  string `json:"sid"`
	Line int    `json:"line"`
	At   string `json:"at"`
	Text string `json:"text"`
}

type dsFinding struct {
	ID        string       `json:"id"`
	Title     string       `json:"title"`
	Kind      string       `json:"kind"`
	PatternID string       `json:"pattern_id"`
	Sev       string       `json:"sev"`
	Readiness string       `json:"readiness"`
	Decision  string       `json:"decision"`
	Execution string       `json:"execution"`
	Scope     string       `json:"scope"` // "collection" in the live dataset: health of the data collection
	Sessions  []string     `json:"sessions"`
	Ev        []dsEvidence `json:"ev"`
}

type dsFriction struct {
	Key      string       `json:"key"`
	Name     string       `json:"name"`
	Sev      string       `json:"sev"`
	Sessions []string     `json:"sessions"`
	Evidence []dsEvidence `json:"evidence"`
}

type dsSkill struct {
	Name        string       `json:"name"`
	State       string       `json:"state"`
	Activations int          `json:"activations"`
	Evidence    []dsEvidence `json:"evidence"`
}

type dsSession struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Start string `json:"start"`
	End   string `json:"end"`
}

type dashDataset struct {
	GeneratedAt string `json:"generated_at"`
	Window      struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"window"`
	Findings []dsFinding  `json:"findings"`
	Friction []dsFriction `json:"friction"`
	Skills   struct {
		Rows []dsSkill `json:"rows"`
	} `json:"skills"`
	Sessions []dsSession `json:"sessions"`
}

type regStatus struct {
	Status string `json:"status"`
}

type regProposal struct {
	ProposalID string    `json:"proposal_id"`
	PatternID  string    `json:"pattern_id"`
	ChangeType string    `json:"change_type"`
	Action     string    `json:"action"`
	Decision   regStatus `json:"decision"`
	Execution  regStatus `json:"execution"`
	Readiness  regStatus `json:"readiness"`
	Sources    []struct {
		SessionID string   `json:"session_id"`
		Evidence  []string `json:"evidence"` // L1157: rollout lines, without a time
	} `json:"sources"`
}

type proposalRegistry struct {
	Proposals []regProposal `json:"proposals"`
}

// finding is a registry proposal missing from the reviewed dataset, shaped as its findings.
func (p regProposal) finding() dsFinding {
	f := dsFinding{ID: p.ProposalID, Title: p.Action, Kind: p.ChangeType, PatternID: p.PatternID,
		Readiness: p.Readiness.Status, Decision: p.Decision.Status, Execution: p.Execution.Status}
	for _, s := range p.Sources {
		f.Sessions = append(f.Sessions, s.SessionID)
		for _, l := range s.Evidence {
			if n, err := strconv.Atoi(strings.TrimPrefix(l, "L")); err == nil {
				f.Ev = append(f.Ev, dsEvidence{SID: s.SessionID, Line: n})
			}
		}
	}
	return f
}

// What is returned.

// FindingRef is one piece of evidence of a finding.
type FindingRef struct {
	Session string `json:"session"`
	At      string `json:"at,omitempty"`
	Line    int    `json:"line,omitempty"`
	// LineOf says what Line counts: live_timeline is a line of the live dashboard's timeline,
	// rollout a line of the rollout. Neither is the seq of session_read.
	LineOf string `json:"line_of"`
	Text   string `json:"text,omitempty"`
}

// FindingBrief is a finding in short.
type FindingBrief struct {
	ID        string `json:"id"`
	Source    string `json:"source"` // live or reviewed
	Title     string `json:"title"`
	Kind      string `json:"kind"`
	Pattern   string `json:"pattern,omitempty"`
	Severity  string `json:"severity,omitempty"`
	Readiness string `json:"readiness,omitempty"`
	Decision  string `json:"decision,omitempty"`
	Execution string `json:"execution,omitempty"`
	// Collection marks the health of the data collection, not the person's work.
	Collection bool         `json:"collection,omitempty"`
	Sessions   []string     `json:"sessions"`
	Evidence   []FindingRef `json:"evidence"`
	// EvidenceTotal is how much evidence was placed, before the cut to maxEvidence.
	EvidenceTotal int `json:"evidence_total"`
}

// ServiceSession is a session that is not the person's work: system or automation.
type ServiceSession struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
}

// SourceState says whether a dataset was read: ok, missing or broken.
type SourceState struct {
	Name        string `json:"name"` // live, reviewed or registry
	Path        string `json:"path"`
	State       string `json:"state"`
	GeneratedAt string `json:"generated_at,omitempty"`
	WindowFrom  string `json:"window_from,omitempty"`
	WindowTo    string `json:"window_to,omitempty"`
	Error       string `json:"error,omitempty"`
}

// FindingsIn selects the findings.
type FindingsIn struct {
	Since  string `json:"since,omitempty" jsonschema:"start of the period over the time of the evidence: YYYY-MM-DD, RFC3339 or a duration back (168h)"`
	Until  string `json:"until,omitempty" jsonschema:"end of the period: YYYY-MM-DD (inclusive) or RFC3339"`
	Source string `json:"source,omitempty" jsonschema:"live, reviewed or all (all by default)"`
	ID     string `json:"id,omitempty" jsonschema:"one finding: live:…, p2:… or friction:<key>"`
}

// FindingsOut is the findings of the period and what they were read from.
type FindingsOut struct {
	DataDir  string         `json:"data_dir"`
	Sources  []SourceState  `json:"sources"`
	Findings []FindingBrief `json:"findings"`
	// Undated are findings none of whose evidence has a time, so the period cannot hold
	// them; listed when one of their sessions meets the period.
	Undated         []FindingBrief   `json:"undated"`
	Skills          []SkillBrief     `json:"skills,omitempty"`
	ServiceSessions []ServiceSession `json:"service_sessions,omitempty"`
}

// SkillBrief is a skill used inside the period.
type SkillBrief struct {
	Name             string `json:"name"`
	State            string `json:"state,omitempty"`
	Activations      int    `json:"activations"`        // over the dataset's window, not the period
	EvidenceInPeriod int    `json:"evidence_in_period"` // activations seen inside the period
	Sessions         int    `json:"sessions"`           // sessions with such evidence
}

type span struct{ from, to time.Time }

// placement is where a finding goes for the period.
type placement int

const (
	placeOut placement = iota
	placeIn
	placeUndated
)

// Findings reads the live dataset (dataDir/ui-live), the reviewed one (dataDir/ui) and the
// proposal registry (dataDir/reports/v2) and returns the findings of the period. A missing
// or broken file is a source state, not an error.
func Findings(dataDir string, in FindingsIn, now time.Time) (FindingsOut, error) {
	src := in.Source
	if src == "" {
		src = "all"
	}
	if src != "live" && src != "reviewed" && src != "all" {
		return FindingsOut{}, fmt.Errorf("source: live, reviewed or all, not %q", in.Source)
	}
	per, err := sessions.ParsePeriod(in.Since, in.Until, now)
	if err != nil {
		return FindingsOut{}, err
	}
	out := FindingsOut{DataDir: dataDir, Sources: []SourceState{}, Findings: []FindingBrief{}, Undated: []FindingBrief{}}
	add := func(f dsFinding, source, lineOf string, spans map[string]span) {
		if in.ID != "" && f.ID != in.ID {
			return
		}
		switch b, where := brief(f, source, lineOf, spans, per); where {
		case placeIn:
			out.Findings = append(out.Findings, b)
		case placeUndated:
			out.Undated = append(out.Undated, b)
		case placeOut:
		}
	}
	if src != "reviewed" {
		ds, st := loadDataset("live", filepath.Join(dataDir, "ui-live", "dataset.json"))
		out.Sources = append(out.Sources, st)
		spans := sessionSpans(ds)
		for _, f := range ds.Findings {
			add(f, "live", "live_timeline", spans)
		}
		for _, fr := range ds.Friction {
			add(dsFinding{ID: "friction:" + fr.Key, Title: fr.Name, Kind: "friction", PatternID: fr.Key, Sev: fr.Sev,
				Readiness: "hypothesis", Sessions: fr.Sessions, Ev: fr.Evidence}, "live", "live_timeline", spans)
		}
		for _, r := range ds.Skills.Rows {
			n, sids := 0, map[string]bool{}
			for _, e := range r.Evidence {
				if per.Has(parseTime(e.At)) {
					n++
					sids[e.SID] = true
				}
			}
			if r.Activations > 0 && (n > 0 || !per.Set()) {
				out.Skills = append(out.Skills, SkillBrief{Name: r.Name, State: r.State, Activations: r.Activations,
					EvidenceInPeriod: n, Sessions: len(sids)})
			}
		}
		for _, s := range ds.Sessions {
			if (s.Kind == "system" || s.Kind == "automation") && per.Overlaps(parseTime(s.Start), parseTime(s.End)) {
				out.ServiceSessions = append(out.ServiceSessions, ServiceSession{ID: s.ID, Kind: s.Kind})
			}
		}
	}
	if src != "live" {
		ds, st := loadDataset("reviewed", filepath.Join(dataDir, "ui", "dataset.json"))
		out.Sources = append(out.Sources, st)
		var reg proposalRegistry
		out.Sources = append(out.Sources, readJSONSource("registry", filepath.Join(dataDir, "reports", "v2", "proposals.json"), &reg))
		spans := sessionSpans(ds)
		byID := map[string]regProposal{}
		for _, p := range reg.Proposals {
			byID[p.ProposalID] = p
		}
		seen := map[string]bool{}
		for _, f := range ds.Findings {
			if p, ok := byID[f.ID]; ok { // the registry keeps the decision and execution statuses
				f.Decision, f.Execution = p.Decision.Status, p.Execution.Status
			}
			seen[f.ID] = true
			add(f, "reviewed", "rollout", spans)
		}
		for _, p := range reg.Proposals {
			if !seen[p.ProposalID] {
				add(p.finding(), "reviewed", "rollout", spans)
			}
		}
	}
	return out, nil
}

// brief places a finding: in the period only with evidence timed inside the window; apart,
// in Undated, when none of its evidence has a time and one of its sessions meets the
// window. A session that merely spans the window proves nothing: its evidence may be
// months old.
func brief(f dsFinding, source, lineOf string, spans map[string]span, per sessions.Period) (FindingBrief, placement) {
	b := FindingBrief{ID: f.ID, Source: source, Title: f.Title, Kind: f.Kind, Pattern: f.PatternID, Severity: f.Sev,
		Readiness: f.Readiness, Decision: f.Decision, Execution: f.Execution, Collection: f.Scope == "collection",
		Sessions: []string{}, Evidence: []FindingRef{}}
	seen := map[string]bool{}
	addSession := func(sid string) {
		if !seen[sid] {
			seen[sid] = true
			b.Sessions = append(b.Sessions, sid)
		}
	}
	addEvidence := func(e dsEvidence) {
		addSession(e.SID)
		b.EvidenceTotal++
		if len(b.Evidence) < maxEvidence {
			b.Evidence = append(b.Evidence, FindingRef{Session: e.SID, At: e.At, Line: e.Line, LineOf: lineOf, Text: clipRunes(e.Text, 160)})
		}
	}
	addAll := func() {
		for _, e := range f.Ev {
			addEvidence(e)
		}
		for _, sid := range f.Sessions {
			addSession(sid)
		}
	}
	if !per.Set() {
		addAll()
		return b, placeIn
	}
	dated := false
	for _, e := range f.Ev {
		t := parseTime(e.At)
		dated = dated || !t.IsZero()
		if per.Has(t) { // false for an evidence without a time
			addEvidence(e)
		}
	}
	switch {
	case b.EvidenceTotal > 0:
		return b, placeIn
	case dated:
		return b, placeOut
	}
	for _, sid := range f.Sessions {
		if sp := spans[sid]; per.Overlaps(sp.from, sp.to) {
			addAll()
			return b, placeUndated
		}
	}
	return b, placeOut
}

func sessionSpans(ds dashDataset) map[string]span {
	m := make(map[string]span, len(ds.Sessions))
	for _, s := range ds.Sessions {
		m[s.ID] = span{parseTime(s.Start), parseTime(s.End)}
	}
	return m
}

// readJSONSource reads the file of a source into v; a missing file is the state missing.
func readJSONSource(name, path string, v any) SourceState {
	st := SourceState{Name: name, Path: path, State: "ok"}
	b, err := os.ReadFile(path) //nolint:gosec // a file of the dashboard's own data directory
	switch {
	case errors.Is(err, fs.ErrNotExist):
		st.State = "missing"
	case err != nil:
		st.State, st.Error = "broken", err.Error()
	default:
		if err := json.Unmarshal(b, v); err != nil {
			st.State, st.Error = "broken", err.Error()
		}
	}
	return st
}

func loadDataset(name, path string) (dashDataset, SourceState) {
	var ds dashDataset
	st := readJSONSource(name, path, &ds)
	if st.State == "ok" {
		st.GeneratedAt, st.WindowFrom, st.WindowTo = ds.GeneratedAt, ds.Window.From, ds.Window.To
	}
	return ds, st
}

// parseTime reads an RFC3339 time of the datasets; anything else is no time.
func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// clipRunes cuts by characters, not bytes: the texts are in Cyrillic.
func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
```

Каталог данных — параметр `dataDir`; переменных окружения пакет не читает.

**Шаг 5.** Тесты, `golangci-lint run ./internal/hottell/local/...`, `go-arch-lint check` — зелёные.

**Шаг 6.** `git add temp/internal/hottell/local/coach && git commit -m "hottell-local: находки дашборда за период — только с доказательствами внутри окна, без времени — отдельно"`

## T5: `local/coach` — журнал коуча и маскирование

**Файлы:**
- Создать: `temp/internal/hottell/local/coach/journal.go`, `mask.go`
- Тесты: `journal_test.go` (пакет `coach_test`), `mask_internal_test.go` (пакет `coach`)

**Шаг 1. Падающие тесты.** `mask_internal_test.go`:

```go
package coach

import (
	"strings"
	"testing"
)

func TestMask(t *testing.T) {
	t.Parallel()
	mask := newMasker()
	for in, want := range map[string]string{
		"api_key=sk-abcdefghijklmnop":         "api_key=[скрыто]",
		"Authorization: Bearer abcdef123456":  "Authorization: [скрыто]",
		"curl -H 'x' bearer abcdef123456":     "curl -H 'x' Bearer [скрыто]",
		"ключ ghp_" + strings.Repeat("a", 24): "ключ [скрыто]",
		"/Users/alex/work/x":                  "~/work/x",
		"обычный текст без секретов":          "обычный текст без секретов",
	} {
		if got := mask.text(in); got != want {
			t.Errorf("mask(%q) = %q, ждали %q", in, got, want)
		}
	}
}
```

`journal_test.go` (пакет `coach_test`) — повторная проверка после `not_enough_data` (замечание 4); остальные тесты журнала — в шаге 4а:

```go
func TestJournalRecheckAfterNotEnoughData(t *testing.T) {
	t.Parallel()
	j := coach.NewJournal(filepath.Join(t.TempDir(), "coach", "journal.jsonl"))
	e, err := j.Append(appliedEntry(), now)
	if err != nil {
		t.Fatal(err)
	}
	id := e["id"].(string)
	check := func(result string, obs any, at time.Time) {
		t.Helper()
		if _, err := j.Append(m{"check_of": id, "topic_key": "scope-creep-engineering", "agent": "codex",
			"result": result, "observations": obs}, at); err != nil {
			t.Fatalf("%s: %v", result, err)
		}
	}
	check("not_enough_data", nil, now.Add(7*24*time.Hour))
	got, _ := j.Read(id, "")
	if got[0]["result"] != "not_enough_data" || got[0]["checks"] != float64(1) {
		t.Fatalf("первая проверка: %+v", got[0])
	}
	check("not_repeated", 4, now.Add(14*24*time.Hour)) // появились сопоставимые задачи — проверка снова
	got, _ = j.Read(id, "")
	if len(got) != 1 || got[0]["result"] != "not_repeated" || got[0]["observations"] != float64(4) || got[0]["checks"] != float64(2) {
		t.Fatalf("действует последняя проверка: %+v", got[0])
	}
}
```

Значения из `Read` проходят через JSON, поэтому числа — `float64`.

**Шаг 2.** Тесты падают: `undefined: newMasker`, `coach.NewJournal`.

**Шаг 3. `mask.go`** — без глобальных переменных:

```go
package coach

import "regexp"

// maskText replaces a secret in the journal.
const maskText = "[скрыто]"

// masker hides secrets and the home directory with the rules of redact() in
// ui/builder/telemetry.py, without its "looks like a key" guess. Client paths and names of
// third parties cannot be recognised this way; the hottell-coach skill masks them.
type masker struct {
	pem, kv, bearer, sk, vendor, home *regexp.Regexp
}

func newMasker() masker {
	return masker{
		pem:    regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`),
		kv:     regexp.MustCompile(`(?i)\b(token|secret|password|passwd|api[_-]?key|authorization)(\s*[:=]\s*)(?:(?:bearer|basic|token)\s+)?("[^"]*"|'[^']*'|\S+)`),
		bearer: regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{6,}`),
		sk:     regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{10,}`),
		vendor: regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[abprs]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16})\b`),
		home:   regexp.MustCompile(`/Users/[^/\s"']+`),
	}
}

func (k masker) text(s string) string {
	s = k.pem.ReplaceAllString(s, maskText)
	s = k.kv.ReplaceAllString(s, "${1}${2}"+maskText)
	s = k.bearer.ReplaceAllString(s, "Bearer "+maskText)
	s = k.sk.ReplaceAllString(s, maskText)
	s = k.vendor.ReplaceAllString(s, maskText)
	return k.home.ReplaceAllString(s, "~")
}
```

**Шаг 4. `journal.go`:**

```go
package coach

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// maxQuoteRunes is how long a quote in the journal may be, after masking.
const maxQuoteRunes = 200

type journalEvidence struct {
	Session string `json:"session"`
	At      string `json:"at,omitempty"`
	Seq     *int   `json:"seq,omitempty"`
	Quote   string `json:"quote"`
}

// journalEntry is one line of the journal: a decision on a topic, or a check of a change
// (CheckOf). The nulls of the spec's record stay nulls: layer, result, observations.
type journalEntry struct {
	ID           string            `json:"id"`
	CheckOf      string            `json:"check_of,omitempty"`
	TopicKey     string            `json:"topic_key"`
	At           string            `json:"at"`
	Agent        string            `json:"agent"`
	Topic        string            `json:"topic,omitempty"`
	Findings     []string          `json:"findings,omitempty"`
	Evidence     []journalEvidence `json:"evidence,omitempty"`
	Decision     string            `json:"decision,omitempty"`
	Layer        *string           `json:"layer"`
	Target       string            `json:"target,omitempty"`
	BeforeSHA256 string            `json:"before_sha256,omitempty"`
	AfterSHA256  string            `json:"after_sha256,omitempty"`
	Change       string            `json:"change,omitempty"`
	Rollback     string            `json:"rollback,omitempty"`
	Check        string            `json:"check,omitempty"`
	CheckAfter   string            `json:"check_after,omitempty"`
	Result       *string           `json:"result"`
	Observations *int              `json:"observations"`
	// Repeats is, for repeated, on how many of the observed tasks the signal came back.
	Repeats *int `json:"repeats,omitempty"`
}

// Journal is the coach's journal: one record per JSON line, appended only.
type Journal struct {
	path string
	mask masker
}

// NewJournal opens the journal at path; the file appears with the first record (0600, its
// directory 0700).
func NewJournal(path string) *Journal { return &Journal{path: path, mask: newMasker()} }

// Path is where the journal is.
func (j *Journal) Path() string { return j.path }

// Read returns the decisions in the order written, each with its latest check folded in
// (result, observations, checked_at, checks); id and topicKey narrow them when set.
func (j *Journal) Read(id, topicKey string) ([]map[string]any, error) {
	all, err := readJournal(j.path)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, rec := range fold(all) {
		if (id != "" && rec["id"] != id) || (topicKey != "" && rec["topic_key"] != topicKey) {
			continue
		}
		out = append(out, rec)
	}
	return roundTrip(out)
}

// Append checks one record, a decision or a check (check_of), masks it and appends it under
// a lock: two agents may write at once. It returns the record as written.
func (j *Journal) Append(raw map[string]any, now time.Time) (map[string]any, error) {
	e, err := decodeEntry(raw)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(j.path), 0o700); err != nil {
		return nil, fmt.Errorf("create the journal's directory: %w", err)
	}
	lock, err := os.OpenFile(j.path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the journal's lock: %w", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return nil, fmt.Errorf("lock the journal: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) //nolint:errcheck // closing the file unlocks it too
	existing, err := readJournal(j.path)
	if err != nil {
		return nil, err
	}
	if e.ID == "" {
		e.ID = newJournalID(now)
	}
	if e.At == "" {
		e.At = now.UTC().Format(time.RFC3339)
	}
	for _, p := range []*string{&e.Topic, &e.Change, &e.Rollback, &e.Check, &e.Target} {
		*p = j.mask.text(*p)
	}
	for i := range e.Evidence {
		e.Evidence[i].Quote = j.mask.text(e.Evidence[i].Quote)
	}
	if err := validateEntry(e, existing); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(e); err != nil { // Encode ends the line itself
		return nil, fmt.Errorf("encode the record: %w", err)
	}
	f, err := os.OpenFile(j.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the journal: %w", err)
	}
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		return nil, fmt.Errorf("chmod the journal: %w", err)
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		return nil, fmt.Errorf("append to the journal: %w", err)
	}
	if err := f.Sync(); err != nil {
		return nil, fmt.Errorf("sync the journal: %w", err)
	}
	recs, err := roundTrip([]map[string]any{entryMap(e)})
	if err != nil {
		return nil, err
	}
	return recs[0], nil
}

// decodeEntry reads a record of append; an unknown field is an error, so a typo is not lost.
func decodeEntry(raw map[string]any) (journalEntry, error) {
	var e journalEntry
	if len(raw) == 0 {
		return e, errors.New("append needs an entry")
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return e, fmt.Errorf("entry: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		return e, fmt.Errorf("entry: %w", err)
	}
	return e, nil
}

func readJournal(path string) ([]journalEntry, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the journal of the dashboard's data directory
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the journal: %w", err)
	}
	var out []journalEntry
	for i, line := range bytes.Split(b, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e journalEntry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("%s: line %d: %w", path, i+1, err)
		}
		out = append(out, e)
	}
	return out, nil
}

// fold returns the decisions in the order written with their latest check folded in; a
// change may be checked again and again (not_enough_data is not final).
func fold(all []journalEntry) []map[string]any {
	out := []map[string]any{}
	idx := map[string]map[string]any{}
	for _, e := range all {
		rec := entryMap(e)
		if e.CheckOf == "" {
			idx[e.ID] = rec
			out = append(out, rec)
			continue
		}
		if o := idx[e.CheckOf]; o != nil {
			n, _ := o["checks"].(int)
			o["result"], o["observations"], o["repeats"], o["checked_at"], o["checks"] = rec["result"], rec["observations"], rec["repeats"], e.At, n+1
		}
	}
	return out
}

func entryMap(e journalEntry) map[string]any {
	b, _ := json.Marshal(e)
	var rec map[string]any
	_ = json.Unmarshal(b, &rec)
	return rec
}

// roundTrip gives the records the JSON types a client sees: numbers are float64.
func roundTrip(recs []map[string]any) ([]map[string]any, error) {
	b, err := json.Marshal(recs)
	if err != nil {
		return nil, fmt.Errorf("encode the records: %w", err)
	}
	var out []map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("decode the records: %w", err)
	}
	return out, nil
}

func newJournalID(now time.Time) string {
	const abc = "0123456789abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = abc[int(b[i])%len(abc)]
	}
	return now.UTC().Format("20060102T150405") + "-" + string(b)
}

func validateEntry(e journalEntry, existing []journalEntry) error {
	byID := map[string]journalEntry{}
	for _, x := range existing {
		byID[x.ID] = x
	}
	if _, dup := byID[e.ID]; dup {
		return fmt.Errorf("id %q is already in the journal", e.ID)
	}
	if _, err := time.Parse(time.RFC3339, e.At); err != nil {
		return fmt.Errorf("at must be RFC3339: %w", err)
	}
	if e.Agent != "codex" && e.Agent != "claude" {
		return fmt.Errorf("agent: codex or claude, not %q", e.Agent)
	}
	if !validTopicKey(e.TopicKey) {
		return fmt.Errorf("topic_key: lowercase latin letters, digits and hyphens, 2–64, not %q", e.TopicKey)
	}
	for i, ev := range e.Evidence {
		if ev.Session == "" || strings.TrimSpace(ev.Quote) == "" {
			return fmt.Errorf("evidence[%d] needs session and quote", i)
		}
		if n := utf8.RuneCountInString(ev.Quote); n > maxQuoteRunes {
			return fmt.Errorf("evidence[%d].quote: %d characters after masking, at most %d", i, n, maxQuoteRunes)
		}
		if ev.At != "" {
			if _, err := time.Parse(time.RFC3339, ev.At); err != nil {
				return fmt.Errorf("evidence[%d].at must be RFC3339: %w", i, err)
			}
		}
	}
	for _, h := range []string{e.BeforeSHA256, e.AfterSHA256} {
		if h != "" && !validSHA256(h) {
			return errors.New("before_sha256 and after_sha256 are 64 lowercase hex digits")
		}
	}
	if e.CheckOf != "" {
		return validateCheck(e, byID)
	}
	return validateDecision(e)
}

func validateDecision(e journalEntry) error {
	switch e.Decision {
	case "applied", "declined", "not_justified", "test":
	default:
		return fmt.Errorf("decision: applied, declined, not_justified or test, not %q", e.Decision)
	}
	if strings.TrimSpace(e.Topic) == "" {
		return errors.New("topic: the topic in one sentence")
	}
	if len(e.Evidence) == 0 {
		return errors.New("evidence: no quotes from the sessions, no topic")
	}
	if e.Result != nil || e.Observations != nil || e.Repeats != nil {
		return errors.New("result, observations and repeats come from a check (check_of), not from a decision")
	}
	if e.Layer != nil {
		switch *e.Layer {
		case "experience", "instructions", "skill", "technical":
		default:
			return fmt.Errorf("layer: experience, instructions, skill or technical, not %q", *e.Layer)
		}
	}
	if e.Decision != "applied" && e.Decision != "test" {
		return nil
	}
	if e.Layer == nil {
		return errors.New("layer is required for applied and test")
	}
	if *e.Layer != "experience" && e.Target == "" {
		return errors.New("target: the file or setting changed")
	}
	if e.Change == "" || e.Rollback == "" || e.Check == "" {
		return errors.New("change, rollback and check are required for applied and test")
	}
	if _, err := time.Parse("2006-01-02", e.CheckAfter); err != nil {
		return fmt.Errorf("check_after: YYYY-MM-DD, when to check: %w", err)
	}
	return nil
}

func validateCheck(e journalEntry, byID map[string]journalEntry) error {
	orig, ok := byID[e.CheckOf]
	if !ok || orig.CheckOf != "" {
		return fmt.Errorf("check_of: no decision %q in the journal", e.CheckOf)
	}
	if orig.Decision != "applied" && orig.Decision != "test" {
		return fmt.Errorf("check_of: only applied and test are checked, %q is %s", e.CheckOf, orig.Decision)
	}
	if e.TopicKey != orig.TopicKey {
		return errors.New("a check has the topic_key of its decision")
	}
	if e.Decision != "" || e.Layer != nil {
		return errors.New("a check has no decision and no layer")
	}
	if e.Result == nil {
		return errors.New("result: repeated, not_repeated or not_enough_data")
	}
	switch *e.Result {
	case "not_enough_data":
	case "repeated", "not_repeated":
		if e.Observations == nil || *e.Observations < 1 {
			return errors.New("observations: how many comparable tasks after the change were seen, at least 1")
		}
	default:
		return fmt.Errorf("result: repeated, not_repeated or not_enough_data, not %q", *e.Result)
	}
	switch {
	case *e.Result == "repeated" && (e.Repeats == nil || *e.Repeats < 1 || *e.Repeats > *e.Observations):
		return errors.New("repeats: on how many of the observed tasks the signal came back, 1 to observations")
	case *e.Result != "repeated" && e.Repeats != nil:
		return errors.New("repeats is only for repeated")
	}
	if *e.Result == "repeated" && len(e.Evidence) == 0 {
		return errors.New("evidence: repeated needs a quote of the repeat")
	}
	return nil
}

// validTopicKey: lowercase latin letters, digits and hyphens, 2–64, not starting with a hyphen.
func validTopicKey(s string) bool {
	if len(s) < 2 || len(s) > 64 || s[0] == '-' {
		return false
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

// validSHA256 is 64 lowercase hex digits.
func validSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
```

**Шаг 4а. Остальные тесты журнала** в `journal_test.go` (пакет `coach_test`, рядом с `TestJournalRecheckAfterNotEnoughData`):

```go
func appliedEntry() m {
	return m{
		"topic_key": "scope-creep-engineering", "agent": "codex", "topic": "Агент раздувает реализацию",
		"findings": []string{"live:abc"},
		"evidence": []m{{"session": "019f-a", "at": "2026-09-30T08:14:02Z", "seq": 412, "quote": "сделай только это, без лишнего"}},
		"decision": "applied", "layer": "instructions", "target": "~/.codex/AGENTS.md",
		"before_sha256": strings.Repeat("a", 64), "after_sha256": strings.Repeat("b", 64),
		"change": "Уточнено правило о минимальной реализации", "rollback": "Вернуть прежнюю строку 12",
		"check": "Задачи на правку кода; сигнал — просьба не раздувать", "check_after": "2026-10-08",
	}
}

func journalAt(t *testing.T) (*coach.Journal, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "coach", "journal.jsonl")
	return coach.NewJournal(path), path
}

func TestJournalAppendAndRead(t *testing.T) {
	t.Parallel()
	j, path := journalAt(t)
	rec, err := j.Append(appliedEntry(), now)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := rec["id"].(string)
	if at, _ := rec["at"].(string); id == "" || at == "" {
		t.Fatalf("id и at ставит журнал: %+v", rec)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("права журнала: %v %v", st, err)
	}
	b, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 1 || !json.Valid([]byte(lines[0])) {
		t.Fatalf("одна запись — одна строка JSON: %q", b)
	}
	var raw m
	_ = json.Unmarshal([]byte(lines[0]), &raw)
	if v, ok := raw["result"]; !ok || v != nil {
		t.Fatalf("у решения result: null, как в схеме: %v", raw)
	}
	declined := appliedEntry()
	declined["topic_key"], declined["decision"], declined["layer"] = "long-sessions", "declined", nil
	if _, err := j.Append(declined, now); err != nil {
		t.Fatal(err)
	}
	if all, _ := j.Read("", ""); len(all) != 2 {
		t.Fatalf("read: %d", len(all))
	}
	if one, _ := j.Read(id, ""); len(one) != 1 || one[0]["id"] != id {
		t.Fatalf("read id: %+v", one)
	}
	if topic, _ := j.Read("", "long-sessions"); len(topic) != 1 || topic[0]["decision"] != "declined" {
		t.Fatalf("read topic_key: %+v", topic)
	}
}

func TestJournalRejects(t *testing.T) {
	t.Parallel()
	j, path := journalAt(t)
	long := appliedEntry()
	long["evidence"] = []m{{"session": "s", "quote": strings.Repeat("я", 201)}}
	noEvidence := appliedEntry()
	delete(noEvidence, "evidence")
	badDecision := appliedEntry()
	badDecision["decision"] = "maybe"
	noCheckAfter := appliedEntry()
	delete(noCheckAfter, "check_after")
	unknown := appliedEntry()
	unknown["checked_at"] = "2026-10-01T00:00:00Z"
	withResult := appliedEntry()
	withResult["result"] = "repeated"
	orphan := m{"check_of": "nope", "topic_key": "x-y", "agent": "codex", "result": "not_enough_data"}
	for name, e := range map[string]m{"цитата > 200": long, "без цитат": noEvidence, "decision": badDecision,
		"без check_after": noCheckAfter, "неизвестное поле": unknown, "result у решения": withResult, "проверка без решения": orphan} {
		if _, err := j.Append(e, now); err == nil {
			t.Errorf("%s: ждали отказ", name)
		}
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("после отказов журнал не появляется")
	}
	ok := m{"id": "x-1", "topic_key": "a-b", "agent": "claude", "topic": "т", "decision": "not_justified", "layer": nil,
		"evidence": []m{{"session": "s", "quote": "q"}}}
	if _, err := j.Append(ok, now); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Append(ok, now); err == nil || !strings.Contains(err.Error(), "x-1") {
		t.Fatalf("повтор id: %v", err)
	}
}

func TestJournalMasks(t *testing.T) {
	t.Parallel()
	j, path := journalAt(t)
	e := appliedEntry()
	e["evidence"] = []m{{"session": "s1", "quote": "api_key=sk-abcdefghijklmnop в /Users/alex/clients"}}
	e["change"] = "В конфиг добавлен Bearer abcdefghijkl"
	if _, err := j.Append(e, now); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	for _, leak := range []string{"sk-abcdefghijklmnop", "abcdefghijkl", "/Users/alex"} {
		if strings.Contains(string(b), leak) {
			t.Fatalf("в журнале осталось %q: %s", leak, b)
		}
	}
}

func TestJournalReadMissing(t *testing.T) {
	t.Parallel()
	j, _ := journalAt(t)
	if all, err := j.Read("", ""); err != nil || all == nil || len(all) != 0 {
		t.Fatalf("нет файла — пустой журнал: %v %v", all, err)
	}
}
```

```go
func TestJournalRepeatsCount(t *testing.T) {
	t.Parallel()
	j, _ := journalAt(t)
	e, _ := j.Append(appliedEntry(), now)
	id := e["id"].(string)
	check := func(extra m) error {
		rec := m{"check_of": id, "topic_key": "scope-creep-engineering", "agent": "codex", "result": "repeated",
			"observations": 3, "evidence": []m{{"session": "s2", "quote": "опять раздул"}}}
		for k, v := range extra {
			rec[k] = v
		}
		_, err := j.Append(rec, now)
		return err
	}
	if check(nil) == nil || check(m{"repeats": 4}) == nil {
		t.Fatal("repeated без repeats или с repeats больше observations — отказ")
	}
	if err := check(m{"repeats": 1}); err != nil {
		t.Fatal(err)
	}
	got, _ := j.Read(id, "")
	if got[0]["repeats"] != float64(1) || got[0]["observations"] != float64(3) {
		t.Fatalf("повторилось на 1 из 3: %+v", got[0])
	}
}
```

Тип `m` и `now` — из `findings_test.go` того же пакета.

**Шаг 5.** Тесты, линтер, архитектура — зелёные.

**Шаг 6.** `git add temp/internal/hottell/local/coach && git commit -m "hottell-local: журнал коуча — JSONL 0600, повторные проверки, маскирование секретов"`

## T6: Сервер `hottell-local` и команда `hottell mcp-local`

**Файлы:**
- Создать: `temp/internal/hottell/local/mcpserver/mcpserver.go`, тест `mcpserver_test.go` (пакет `mcpserver_test`)
- Создать: `temp/cmd/hottell/mcplocal.go`, тест `mcplocal_internal_test.go`
- Изменить: `temp/cmd/hottell/main.go` (doc-комментарий, `usage`, `case "mcp-local"`)

**Шаг 1. Падающий тест** `mcpserver_test.go`:

```go
package mcpserver_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/local/mcpserver"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/local/sessions"
)

func connect(t *testing.T, cfg mcpserver.Config) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	if _, err := mcpserver.New(cfg).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func config(t *testing.T) mcpserver.Config {
	t.Helper()
	home := t.TempDir()
	return mcpserver.Config{
		Roots:   sessions.Roots{ClaudeProjects: filepath.Join(home, ".claude", "projects"), CodexHome: filepath.Join(home, ".codex")},
		DataDir: filepath.Join(home, "data"), Version: "test",
		Now:     func() time.Time { return time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC) },
	}
}

func TestTools(t *testing.T) {
	t.Parallel()
	cs := connect(t, config(t))
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"coach_journal", "findings", "session_read", "session_stats", "sessions_list"}) {
		t.Fatalf("tools: %v", names)
	}
}

func TestJournalAndFindingsOverMCP(t *testing.T) {
	t.Parallel()
	cs := connect(t, config(t))
	ctx := context.Background()
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("%s: %v %+v", name, err, res)
		}
		var out map[string]any
		raw, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(raw, &out)
		return out
	}
	if f := call("findings", map[string]any{"since": "168h"}); len(f["sources"].([]any)) != 3 {
		t.Fatalf("findings: %+v", f)
	}
	entry := map[string]any{"topic_key": "a-b", "agent": "claude", "topic": "т", "decision": "declined", "layer": nil,
		"evidence": []map[string]any{{"session": "s", "quote": "q"}}}
	call("coach_journal", map[string]any{"op": "append", "entry": entry})
	if r := call("coach_journal", map[string]any{"op": "read"}); r["count"] != float64(1) {
		t.Fatalf("journal: %+v", r)
	}
}
```

`mcplocal_internal_test.go`:

```go
package main

import (
	"io"
	"path/filepath"
	"testing"
)

func TestMCPLocalConfig(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	env := map[string]string{"HOME": home}
	cfg, err := mcpLocalConfigFrom(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Roots.ClaudeProjects != filepath.Join(home, ".claude", "projects") || cfg.Roots.CodexHome != filepath.Join(home, ".codex") ||
		cfg.DataDir != filepath.Join(home, "Life", "projects", "ai-hottell", "local-data") {
		t.Fatalf("по умолчанию: %+v", cfg)
	}
	env["HOTTELL_DATA_DIR"] = "/x/data"
	env["CLAUDE_CONFIG_DIR"] = filepath.Join(home, "cc")
	cfg, _ = mcpLocalConfigFrom(func(k string) string { return env[k] })
	if cfg.DataDir != "/x/data" || cfg.Roots.ClaudeProjects != filepath.Join(home, "cc", "projects") {
		t.Fatalf("из окружения: %+v", cfg)
	}
}

func TestRunMCPLocalUsage(t *testing.T) {
	t.Parallel()
	if code := run([]string{"mcp-local", "extra"}, nil, io.Discard, io.Discard, func(string) string { return "" }); code != exitUsage {
		t.Fatalf("лишний аргумент: %d", code)
	}
}
```

Если `daemonConfigFrom` читает переменные, которых нет в `env` теста (например, требует состояние), — взять из неё только определение `home`, `claudeDir`, `codexHome` в общий хелпер и вызвать его из обеих.

**Шаг 2.** Тесты падают: нет пакета `mcpserver`, нет `mcpLocalConfigFrom`.

**Шаг 3. `mcpserver.go`:**

```go
// Package mcpserver is hottell-local: the MCP server the agents start from the hottell
// binary over stdio. It gives the hottell-coach skill this Mac's sessions, the dashboard's
// findings and the coach's journal; nothing it reads leaves the machine. The service's
// server is hottell; this one never talks to it.
package mcpserver

import (
	"context"
	"io"
	"path/filepath"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/local/coach"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/local/sessions"
)

// Name is the server's name in the agents' configs.
const Name = "hottell-local"

// Config is what the server reads.
type Config struct {
	Roots sessions.Roots
	// DataDir is the dashboard's local-data: ui-live/, ui/, reports/v2/, coach/.
	DataDir string
	Version string
	Now     func() time.Time
}

// New returns the server with its tools.
func New(cfg Config) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: Name, Version: cfg.Version}, nil)
	h := handlers{cfg: cfg, journal: coach.NewJournal(filepath.Join(cfg.DataDir, "coach", "journal.jsonl"))}
	mcp.AddTool(s, &mcp.Tool{Name: "sessions_list",
		Description: "Inventory of Claude Code and Codex transcripts on this Mac: id, agent, project (cwd), size, times. since/until look at the file's mtime, not at the events; offset/limit page the list."},
		h.list)
	mcp.AddTool(s, &mcp.Tool{Name: "session_read",
		Description: "One session page by page: user and agent turns, reasoning, tool calls and results, tokens. from/to keep only events of that time; matched_events and out_of_period count the whole session, next_offset is the next page (0 — none)."},
		h.read)
	mcp.AddTool(s, &mcp.Tool{Name: "session_stats",
		Description: "Aggregates of one session (id) or of a period: events by kind, tools and their errors, tokens by model, turns, duration. from/to count only events of that time; coverage says how many sessions and events were seen of those found, coverage.next_offset is the next page."},
		h.stats)
	mcp.AddTool(s, &mcp.Tool{Name: "findings",
		Description: "Findings of the hottell dashboard for a period: the live dataset (findings, friction, skills, service sessions) and the reviewed one (Deep 2.0 and the proposal registry). A finding counts for the period only with evidence timed inside it; findings whose evidence has no time are listed apart in undated. Evidence line is a dashboard or rollout line, not a seq."},
		h.findings)
	mcp.AddTool(s, &mcp.Tool{Name: "coach_journal",
		Description: "Journal of hottell-coach. op=read — all decisions or one by id/topic_key, each with its latest check (result, observations, checked_at, checks). op=append — one record: a decision on a topic (applied, declined, not_justified, test) or a check of a change (check_of). Secrets and the home directory are masked; quotes are at most 200 characters."},
		h.journal)
	return s
}

// Run serves one agent over in and out until it disconnects.
func Run(ctx context.Context, cfg Config, in io.Reader, out io.Writer) error {
	return New(cfg).Run(ctx, &mcp.IOTransport{Reader: io.NopCloser(in), Writer: nopWriteCloser{out}})
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

type handlers struct {
	cfg     Config
	journal *coach.Journal
}

type listOut struct {
	Total    int             `json:"total"`
	Returned int             `json:"returned"`
	Sessions []sessions.Info `json:"sessions"`
}

func (h handlers) list(_ context.Context, _ *mcp.CallToolRequest, in sessions.Filter) (*mcp.CallToolResult, listOut, error) {
	list, total, err := sessions.List(h.cfg.Roots, in, h.cfg.Now())
	if err != nil {
		return nil, listOut{}, err
	}
	if list == nil {
		list = []sessions.Info{}
	}
	return nil, listOut{Total: total, Returned: len(list), Sessions: list}, nil
}

func (h handlers) read(_ context.Context, _ *mcp.CallToolRequest, in sessions.ReadIn) (*mcp.CallToolResult, sessions.ReadOut, error) {
	out, err := sessions.Read(h.cfg.Roots, in, h.cfg.Now())
	return nil, out, err
}

func (h handlers) stats(_ context.Context, _ *mcp.CallToolRequest, in sessions.StatsIn) (*mcp.CallToolResult, *sessions.Stats, error) {
	st, note, err := sessions.StatsOf(h.cfg.Roots, in, h.cfg.Now())
	if err != nil || note == "" {
		return nil, st, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: note}}}, st, nil
}

func (h handlers) findings(_ context.Context, _ *mcp.CallToolRequest, in coach.FindingsIn) (*mcp.CallToolResult, coach.FindingsOut, error) {
	out, err := coach.Findings(h.cfg.DataDir, in, h.cfg.Now())
	return nil, out, err
}

// journalIn is a map, not a struct: a record holds nulls (layer, result) and the journal
// itself checks every field strictly.
type journalIn struct {
	Op       string         `json:"op" jsonschema:"read or append"`
	ID       string         `json:"id,omitempty" jsonschema:"read: one decision"`
	TopicKey string         `json:"topic_key,omitempty" jsonschema:"read: the decisions of one topic"`
	Entry    map[string]any `json:"entry,omitempty" jsonschema:"append: one record — a decision or a check (check_of); fields as analytics/coach/SKILL.md, section Журнал, lists them"`
}

type journalOut struct {
	Path    string           `json:"path"`
	Count   int              `json:"count"`
	Entries []map[string]any `json:"entries"`
}

func (h handlers) journal(_ context.Context, _ *mcp.CallToolRequest, in journalIn) (*mcp.CallToolResult, journalOut, error) {
	out := journalOut{Path: h.journal.Path(), Entries: []map[string]any{}}
	switch in.Op {
	case "read":
		entries, err := h.journal.Read(in.ID, in.TopicKey)
		if err != nil {
			return nil, out, err
		}
		out.Entries = entries
	case "append":
		rec, err := h.journal.Append(in.Entry, h.cfg.Now())
		if err != nil {
			return nil, out, err
		}
		out.Entries = append(out.Entries, rec)
	default:
		return nil, out, fmt.Errorf("op: read or append, not %q", in.Op)
	}
	out.Count = len(out.Entries)
	return nil, out, nil
}
```

(добавить `"fmt"` в импорты).

**Шаг 4. `temp/cmd/hottell/mcplocal.go`:**

```go
package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/local/mcpserver"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/local/sessions"
)

// dataDirEnv moves the dashboard's local-data that hottell-local reads.
const dataDirEnv = "HOTTELL_DATA_DIR"

// runMCPLocal serves hottell-local, the coach's local MCP server, over stdin and stdout.
// The agents start it from their configs; install puts it there.
func runMCPLocal(args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, usage)
		return exitUsage
	}
	cfg, err := mcpLocalConfigFrom(getenv)
	if err != nil {
		fmt.Fprintf(stderr, "hottell mcp-local: %v\n", err)
		return exitFailure
	}
	if err := mcpserver.Run(context.Background(), cfg, stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "hottell mcp-local: %v\n", err)
		return exitFailure
	}
	return exitOK
}

// mcpLocalConfigFrom finds the transcripts where install finds the agents' configs and the
// dashboard's data in HOTTELL_DATA_DIR or ~/Life/projects/ai-hottell/local-data.
func mcpLocalConfigFrom(getenv func(string) string) (mcpserver.Config, error) {
	d, err := daemonConfigFrom(getenv)
	if err != nil {
		return mcpserver.Config{}, err
	}
	data := getenv(dataDirEnv)
	if data == "" {
		data = filepath.Join(d.home, "Life", "projects", "ai-hottell", "local-data")
	}
	return mcpserver.Config{
		Roots:   sessions.Roots{ClaudeProjects: filepath.Join(d.claudeDir, "projects"), CodexHome: d.codexHome},
		DataDir: data, Version: currentVersion(), Now: time.Now,
	}, nil
}
```

`main.go`: в doc-комментарий — строку `//	hottell mcp-local              serve hottell-local, the coach's local MCP server, over stdio`; в `usage` — `mcp-local`; в `switch` — `case "mcp-local": return runMCPLocal(args[1:], stdin, stdout, stderr, getenv)`.

**Шаг 5.** Все проверки `temp/` зелёные. Ручная проверка stdio:

```bash
cd temp && go build -o /tmp/hottell-next ./cmd/hottell
printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}' \
  '{"jsonrpc":"2.0","method":"notifications/initialized"}' '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
  | (cat; sleep 1) | /tmp/hottell-next mcp-local | grep -o '"name":"[a-z_]*"' | sort -u
```

Ожидаются пять имён инструментов.

**Шаг 6.** `git add temp/internal/hottell/local/mcpserver temp/cmd/hottell && git commit -m "hottell mcp-local: локальный MCP hottell-local — сессии, находки, журнал коуча"`

## T7: Регистрация `hottell-local` в `install` и снятие в `uninstall`

**Файлы:**
- Создать: `temp/internal/hottell/agentconfig/claude/mcplocal.go`, `temp/internal/hottell/agentconfig/codex/mcplocal.go` и тесты `mcplocal_test.go` в обоих (внешние пакеты)
- Изменить: `temp/cmd/hottell/install.go`, `temp/cmd/hottell/uninstall.go`, тесты `install_internal_test.go`, `uninstall_internal_test.go`

**Шаг 1. Падающие тесты.** `agentconfig/claude/mcplocal_test.go`:

```go
package claude_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/agentconfig/claude"
)

func TestMCPLocal(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), ".claude.json")
	orig := `{"numStartups":3,"mcpServers":{"hottell":{"type":"http","url":"http://localhost:8080/mcp"}},"projects":{}}`
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := claude.EnsureMCPLocal(path, "hottell-local", "/u/.local/bin/hottell"); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		NumStartups int                       `json:"numStartups"`
		Servers     map[string]map[string]any `json:"mcpServers"`
	}
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	local := doc.Servers["hottell-local"]
	if doc.NumStartups != 3 || doc.Servers["hottell"]["type"] != "http" || local["type"] != "stdio" ||
		local["command"] != "/u/.local/bin/hottell" || local["args"].([]any)[0] != "mcp-local" {
		t.Fatalf("после Ensure: %s", b)
	}
	st1, _ := os.Stat(path)
	if err := claude.EnsureMCPLocal(path, "hottell-local", "/u/.local/bin/hottell"); err != nil {
		t.Fatal(err)
	}
	if st2, _ := os.Stat(path); !st2.ModTime().Equal(st1.ModTime()) {
		t.Fatal("повтор без изменений не пишет файл")
	}
	if err := claude.RemoveMCPLocal(path, "hottell-local"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	doc.Servers = nil
	_ = json.Unmarshal(b, &doc)
	if _, ok := doc.Servers["hottell-local"]; ok || doc.Servers["hottell"] == nil {
		t.Fatalf("снята только своя запись: %s", b)
	}
}
```

`agentconfig/codex/mcplocal_test.go`:

```go
package codex_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/agentconfig/codex"
)

const foreignTOML = `model = "gpt"

# сервис команды
[mcp_servers.hottell]
url = "http://localhost:8080/mcp"

[mcp_servers.gitea]
command = "/x/gitea-mcp"

[tui]
theme = "dark"
`

func TestMCPLocal(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := codex.ConfigFile(home)
	if err := os.WriteFile(path, []byte(foreignTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := codex.EnsureMCPLocal(home, "hottell-local", "/u/.local/bin/hottell"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	text := string(b)
	if !strings.HasPrefix(text, strings.TrimRight(foreignTOML, "\n")) ||
		!strings.Contains(text, "[mcp_servers.hottell-local]\ncommand = \"/u/.local/bin/hottell\"\nargs = [\"mcp-local\"]\n") {
		t.Fatalf("после Ensure:\n%s", text)
	}
	if err := codex.EnsureMCPLocal(home, "hottell-local", "/u/.local/bin/hottell"); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(path); string(again) != text {
		t.Fatal("повтор ничего не меняет")
	}
	if err := codex.RemoveMCPLocal(home, "hottell-local"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "hottell-local") || !strings.Contains(string(b), "# сервис команды\n[mcp_servers.hottell]") {
		t.Fatalf("снята только своя таблица, комментарий над чужой цел:\n%s", b)
	}
}

func TestMCPLocalRefusesInlineDefinition(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	os.WriteFile(codex.ConfigFile(home), []byte("[mcp_servers]\nhottell-local = { command = \"x\" }\n"), 0o600)
	if err := codex.EnsureMCPLocal(home, "hottell-local", "/u/.local/bin/hottell"); err == nil {
		t.Fatal("определение вне своей таблицы не правится, а отклоняется")
	}
}

func TestMCPLocalMissingConfig(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := codex.EnsureMCPLocal(home, "hottell-local", "/u/.local/bin/hottell"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(codex.ConfigFile(home)); !strings.Contains(string(b), "[mcp_servers.hottell-local]") {
		t.Fatalf("новый config.toml: %s", b)
	}
}
```

Если `readConfig` не создаёт отсутствующий файл, а возвращает ошибку, — повторить поведение `ApplyOTel` для отсутствующего файла.

**Шаг 2.** Падение: `undefined: claude.EnsureMCPLocal`, `codex.EnsureMCPLocal`.

**Шаг 3. `agentconfig/claude/mcplocal.go`:**

```go
package claude

import (
	"bytes"
	"encoding/json"
	"reflect"
)

// The local MCP server hottell-local lives in ~/.claude.json (CLAUDE_CONFIG_DIR/.claude.json)
// at the user scope, so every project sees it. Only its own entry of mcpServers is touched.

// EnsureMCPLocal writes mcpServers.<name> that starts binaryPath mcp-local over stdio.
func EnsureMCPLocal(claudeJSON, name, binaryPath string) error {
	want, err := marshal(map[string]any{"type": "stdio", "command": binaryPath, "args": []string{"mcp-local"}, "env": map[string]string{}})
	if err != nil {
		return err
	}
	return editSection(claudeJSON, "mcpServers", func(servers *object) (bool, error) {
		if cur, ok := servers.get(name); ok && sameJSON(cur, want) {
			return false, nil
		}
		servers.set(name, want)
		return true, nil
	})
}

// RemoveMCPLocal takes mcpServers.<name> out; the other servers stay.
func RemoveMCPLocal(claudeJSON, name string) error {
	return editSection(claudeJSON, "mcpServers", func(servers *object) (bool, error) {
		if _, ok := servers.get(name); !ok {
			return false, nil
		}
		servers.remove(name)
		return true, nil
	})
}

func sameJSON(a, b json.RawMessage) bool {
	if bytes.Equal(a, b) {
		return true
	}
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}
```

**Шаг 4. `agentconfig/codex/mcplocal.go`:**

```go
package codex

import (
	"fmt"
	"strings"
)

// The local MCP server hottell-local lives in config.toml as the table
// [mcp_servers.<name>]. Only that table is touched; a definition of the same server outside
// it (inline in [mcp_servers] or a root-level dotted key) is refused, not rewritten.

// EnsureMCPLocal writes [mcp_servers.<name>] that starts binaryPath mcp-local.
func EnsureMCPLocal(codexHome, name, binaryPath string) error {
	path := ConfigFile(codexHome)
	data, err := readConfig(path)
	if err != nil {
		return err
	}
	rest, own, err := cutServerTable(data, name)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	block := "[mcp_servers." + name + "]\ncommand = " + quote(binaryPath) + "\nargs = [\"mcp-local\"]\n"
	if strings.TrimSpace(own) == strings.TrimSpace(block) {
		return nil
	}
	return writeConfig(path, appendBlock(rest, block))
}

// RemoveMCPLocal takes [mcp_servers.<name>] out of config.toml.
func RemoveMCPLocal(codexHome, name string) error {
	path := ConfigFile(codexHome)
	data, err := readConfig(path)
	if err != nil {
		return err
	}
	rest, own, err := cutServerTable(data, name)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if own == "" {
		return nil
	}
	return writeConfig(path, rest)
}

// cutServerTable splits config.toml into [mcp_servers.<name>] with its sub-tables and the
// rest, keeping comments and blank lines with their neighbours the way cutOTel does.
func cutServerTable(data []byte, name string) (rest []byte, table string, err error) {
	stmts, err := scan(string(data))
	if err != nil {
		return nil, "", err
	}
	ours := func(path []string) bool { return len(path) >= 2 && path[0] == "mcp_servers" && path[1] == name }
	owned := make([]bool, len(stmts))
	var cur []string
	for i, s := range stmts {
		switch s.kind {
		case header:
			cur = s.path
			owned[i] = !s.array && ours(s.path)
		case pair:
			if !ours(cur) && ours(append(append([]string{}, cur...), s.path...)) {
				return nil, "", fmt.Errorf("%w: mcp_servers.%s is defined outside its own table", ErrMalformed, name)
			}
			owned[i] = ours(cur)
		case trivia:
		}
	}
	for i, s := range stmts {
		if s.kind == trivia {
			owned[i] = ownedNeighbour(stmts, owned, i, -1) && ownedNeighbour(stmts, owned, i, 1)
		}
	}
	var kept, cut strings.Builder
	removed := false
	for i, s := range stmts {
		if owned[i] {
			cut.WriteString(s.text)
			removed = true
			continue
		}
		if removed && isBlank(s) && strings.HasSuffix(kept.String(), "\n\n") {
			removed = false
			continue
		}
		removed = false
		kept.WriteString(s.text)
	}
	return []byte(kept.String()), cut.String(), nil
}
```

Если в пакете `codex` нет `ErrMalformed`, взять ошибку, которой `TestRefusesWhatItCannotEdit` проверяет отказ, или объявить `ErrMalformed` по образцу пакета `claude`. `quote` — из `otel.go`.

**Шаг 5. `install.go`** — после `result := applySettings(cfg.daemon, settings, r)`:

```go
	ensureMCPLocal(cfg.daemon, r)
```

и функции:

```go
// ensureMCPLocal registers hottell-local, the coach's local MCP server, in both agents. A
// failure is reported but does not stop install: sending works without the coach.
func ensureMCPLocal(d daemonConfig, r *report) {
	err := errors.Join(
		claude.EnsureMCPLocal(claudeJSONOf(d), mcpserver.Name, d.binary),
		codex.EnsureMCPLocal(d.codexHome, mcpserver.Name, d.binary),
	)
	if err != nil {
		r.failed("mcp-local", err)
		return
	}
	r.ok("mcp-local", mcpserver.Name+" in Claude Code and Codex")
}

// claudeJSONOf is ~/.claude.json or CLAUDE_CONFIG_DIR/.claude.json, as the MCP sources name it.
func claudeJSONOf(d daemonConfig) string {
	for _, s := range d.sources {
		if s.Agent == mcpconfig.Claude {
			return s.Path
		}
	}
	return filepath.Join(d.home, ".claude.json")
}
```

**Шаг 6. `uninstall.go`** — в шаги `r.step("claude", …)` и `r.step("codex", …)` добавить снятие и поправить подписи:

```go
	r.step("claude", "hooks, native OTel and "+mcpserver.Name+" removed",
		func() error { return claude.RemoveHooks(claudeSettings) },
		otel.Remove,
		func() error { return claude.RemoveMCPLocal(claudeJSONOf(d), mcpserver.Name) },
	)
	r.step("codex", "hooks, their trust, native OTel and "+mcpserver.Name+" removed",
		func() error { return codex.Uninstall(d.codexHome) },
		func() error { return codex.RemoveOTel(d.codexHome, d.paths) },
		func() error { return codex.RemoveMCPLocal(d.codexHome, mcpserver.Name) },
	)
```

**Шаг 7. Тесты команд.** В `TestInstall` (`install_internal_test.go`) после проверок итога: в `.claude.json` тестового дома есть `mcpServers.hottell-local` с `command` = путь установленного бинаря и `args` = `["mcp-local"]`; в `config.toml` — таблица `[mcp_servers.hottell-local]`; запись `hottell` сервиса не изменилась. В `TestInstallAgainReinstalls` — второй прогон не меняет эти записи. В `TestUninstallRestoresTheAgentsConfigs` — после `uninstall` записей `hottell-local` нет ни в одном конфиге, `hottell` сервиса на месте. Если `testdata/home` не содержит `.claude.json`, тест создаёт его из `testdata` с записью `hottell` по HTTP — так же, как тест готовит MCP-источник для `install`.

**Шаг 8.** Все проверки `temp/` зелёные.

**Шаг 9.** `git add temp && git commit -m "hottell install/uninstall: регистрация локального MCP hottell-local у обоих агентов"`

## T8: `hottell-local` на этой машине (с согласия владельца)

Сейчас здесь прототип: stdio-сервер `hottell` (`~/.local/bin/hottell mcp`) и хуки `hottell -agent …`. `hottell install` нового бинаря требует подключённого сервиса команды и заменит хуки. Поэтому два пути.

**Шаг 0.** Проверить состояние:

```bash
~/.local/bin/hottell 2>&1 | head -1          # прототип: «… -agent claude|codex | drain | mcp …»; новый: «usage: hottell install|…»
grep -c 'hottell -agent' ~/.claude/settings.json ~/.codex/hooks.json
claude mcp list 2>/dev/null | grep -i hottell
```

**Путь А — машина уже на сервисе команды** (новый бинарь стоит, `hottell status` зелёный). Спросить владельца: «Переустановить hottell сборкой ветки? `install` зарегистрирует `hottell-local` у обоих агентов; копии конфигов — в наборе резервных копий». После «да»:

```bash
cd temp && go build -o /tmp/hottell-next ./cmd/hottell && /tmp/hottell-next install
~/.local/bin/hottell status
```

В выводе `install` — строка `mcp-local … ok`.

**Путь Б — приёмка до перехода** (прототип не трогаем). Спросить владельца: «Собрать бинарь ветки в `local-data/bin/hottell-next` и зарегистрировать у Claude Code и Codex сервер `hottell-local` из него? Хуки и сервер `hottell` прототипа не меняются». После «да»:

```bash
cd temp && go build -o ~/Life/projects/ai-hottell/local-data/bin/hottell-next ./cmd/hottell
claude mcp add --scope user hottell-local -- "$HOME/Life/projects/ai-hottell/local-data/bin/hottell-next" mcp-local
codex mcp add hottell-local -- "$HOME/Life/projects/ai-hottell/local-data/bin/hottell-next" mcp-local
```

Если `codex mcp add` в установленной версии не умеет stdio через `--`, дописать в `~/.codex/config.toml` таблицу `[mcp_servers.hottell-local]` с `command` и `args = ["mcp-local"]` (предварительно скопировав файл в `local-data/backups/`).

**Шаг 1.** Новая сессия Claude Code: вызвать `findings` `{"since": "168h"}` у `hottell-local` — три источника `ok`; `session_stats` `{"from": "168h"}` — есть `coverage`. То же в новой сессии Codex.

**Шаг 2.** Обновить память проекта `hottell-local-install-state`: какой путь выбран, где бинарь, как снять (`claude mcp remove --scope user hottell-local`, `codex mcp remove hottell-local` для пути Б).

---

## Часть B — skill

## T9: `analytics/coach/SKILL.md` и проверка его структуры

**Файлы:** создать `analytics/coach/SKILL.md`, `analytics/test_coach_skill.py`.

**Шаг 1. Падающий тест** `analytics/test_coach_skill.py`:

```python
"""Структура analytics/coach/SKILL.md: frontmatter, правила дословно из спеки, тулы MCP.

Run: python3 -m unittest analytics.test_coach_skill -v   (из корня репозитория)
"""

import re
import unittest
from pathlib import Path

SKILL = Path(__file__).resolve().parent / "coach" / "SKILL.md"

# docs/specs/2026-10-01-hottell-coach.spec.md, «Правила (в SKILL.md дословно)»
RULES = [
    "Не дублировать. Если правило уже есть в настройках, но нарушается, уточнять существующее (файл и строка), а не добавлять второе.",
    "Сначала поправить существующий skill, потом создавать новый. Имя нового — по классу задач, а не по сегодняшней задаче.",
    "Не превращать в правило: сбои окружения (это починка), утверждения «инструмент X не работает», разовые ошибки, разовые задачи, способы, которые так и не сработали.",
    "Предпочтение, относящееся к задаче, у которой есть skill, — в этот skill; общее — в инструкции. Никогда в оба места.",
    "Без цитат из сессий темы нет.",
    "Без явного согласия человека ничего не менять.",
    "Секреты, пути клиентов и имена третьих лиц маскируются везде: в цитатах, в изменениях и в журнале. В журнал попадают ссылки на события и цитата не длиннее 200 символов.",
]


class CoachSkillTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.text = SKILL.read_text(encoding="utf-8")

    def test_frontmatter(self):
        m = re.match(r"^---\nname: hottell-coach\ndescription: (.+)\n---\n", self.text)
        self.assertIsNotNone(m, "frontmatter: name: hottell-coach и однострочный description")
        for trigger in ("$hottell-coach", "/hottell-coach", "дашборд"):
            self.assertIn(trigger, m.group(1))

    def test_rules_verbatim(self):
        for rule in RULES:
            self.assertIn(rule, self.text)

    def test_data_contract(self):
        for word in ("hottell-local", "coach_journal", "findings", "undated", "session_read", "session_stats", "`from`",
                     "next_offset", "coverage", "line_of", "check_of", "checked_at", "repeats", "весь период", "сессия <id>", "HOTTELL_DATA_DIR"):
            self.assertIn(word, self.text)

    def test_layers_and_outcomes(self):
        for word in ("experience", "instructions", "skill", "technical", "applied", "declined", "not_justified",
                     "test", "repeated", "not_repeated", "not_enough_data"):
            self.assertIn(f"`{word}`", self.text)


if __name__ == "__main__":
    unittest.main()
```

**Шаг 2.** `python3 -m unittest analytics.test_coach_skill -v` — падает: `FileNotFoundError`.

**Шаг 3. `analytics/coach/SKILL.md`** — текст целиком:

````markdown
---
name: hottell-coach
description: Разговор об улучшении работы с Codex или Claude Code по данным hottell — показать с цитатами, что мешает, вместе с человеком найти причину, выбрать один слой (опыт, инструкции, skill, техника), показать точное изменение, внести его только после явного «да» и записать решение в журнал. Используй, когда вызывают $hottell-coach или /hottell-coach, когда человек пришёл из дашборда hottell с id находки или просит разобрать, что мешает в работе с агентом за период.
---

# hottell-coach

Ты — коуч по работе человека с агентами Codex и Claude Code на этой машине. Данные даёт локальный MCP `hottell-local` (команда `hottell mcp-local` бинаря hottell; `hottell` — сервер сервиса команды, коучу он не нужен), методология — этот файл. Готовых советов не раздаёшь: показываешь, что повторяется, с цитатами; причину выясняешь вместе с человеком; вносишь одно изменение за раз и только с его согласия. Сигнал — повод разобраться, а не доказательство неэффективности человека.

## Вход

- `$hottell-coach <тема>, период N дней` в Codex или `/hottell-coach <тема>, период N дней` в Claude Code.
- Тема — id находки дашборда (`live:…`, `p2:…`, `friction:<ключ>`), `сессия <id>` или слова. «обзор» или пустая тема — начни с обзора.
- `сессия <id>` — человек пришёл из ленты сессии: прочитай её события периода (`session_read` с `from`), назови, что в ней повторяется из находок периода, и переходи к шагу 3 по выбранному.
- Период по умолчанию — 7 дней: `from` = `168h` (N дней — `N*24h`), `to` не задаёшь. «весь период» — без `from`. Одно окно на весь разговор.

## Данные

| Что | Вызов MCP `hottell-local` |
|---|---|
| Журнал прошлых решений | `coach_journal` `{"op": "read"}` |
| Находки дашборда за период | `findings` `{"since": "168h"}`; одна находка — `{"id": "<id>"}`. В `undated` — находки, у доказательств которых нет времени: это не основание периода, пока не найдено событие окна через `session_read` |
| События сессии за период | `session_read` `{"id", "from", "to", "kinds", "offset"}` — дочитывай страницы, пока `next_offset` не станет 0 |
| Агрегаты периода | `session_stats` `{"from", "to", "agent", "limit", "offset"}` — дочитывай, пока `coverage.next_offset` не станет 0 |
| Кандидаты в сессии | `sessions_list` `{"since"}` — `since` смотрит на время изменения файла, а не событий |
| Текущие настройки | читай с диска сам: `~/.codex/AGENTS.md`, `~/.claude/CLAUDE.md`, проектные `AGENTS.md`/`CLAUDE.md` (по `cwd` сессий), `~/.codex/skills/*/SKILL.md`, `~/.claude/skills/*/SKILL.md`, `~/.codex/config.toml` (`[mcp_servers]`), `~/.codex/hooks.json`, `~/.claude/settings.json` (`hooks`, `permissions`, `model`) |

Находки и журнал лежат в каталоге данных дашборда: `HOTTELL_DATA_DIR`, по умолчанию `~/Life/projects/ai-hottell/local-data`.

Как обращаться с данными:
- **Только события периода.** В `session_read` и `session_stats` всегда передавай `from` (и `to`, если окно закрыто). В недавно изменённом файле бывают реплики месячной давности — в разговор они не попадают.
- **Цитата** — id сессии, время и номер события `seq` из `session_read`. У доказательств `findings` есть `at` и `line`, но `line` — строка ленты дашборда (`line_of: live_timeline`) или rollout (`line_of: rollout`), а не `seq`. Чтобы процитировать, вызови `session_read` с `from`/`to` = `at` ± 2 секунды и возьми `seq` и текст события. Нет `at` (предложения реестра) — прочитай сессию за период и найди событие по содержанию; не нашёл — не цитируй.
- **Страницы.** Длинные сессии читай с `kinds` (например, `user_message`, `tool_result`) и `max_text`, чтобы страница помещалась.
- **Полнота выборки** — одной строкой в обзоре: «просмотрено S сессий и E событий периода из F найденных файлов; вне периода отброшено X» — по `coverage` из `session_stats` и `out_of_period` из `session_read`. Источник `findings` в состоянии `missing` или `broken` — скажи, каких находок нет.
- **Служебные сессии** (`service_sessions` из `findings`: `system`, `automation`) — не работа человека; выводов о нём по ним не делай. Эту сессию коуча не разбирай.

## Ход разговора

### 1. Начни с журнала

Прочитай журнал. Проверь записи с `decision` `applied` или `test`, у которых `check_after` уже наступил и:
- ещё нет `result`, или
- `result` = `not_enough_data`, и после `checked_at` у того же агента появились новые сессии.

Для каждой:
- найди после `at` записи сопоставимые задачи того же агента (`agent`): тот же класс задач и тот же сигнал, что в `check`. Окно — `from` = `at` записи;
- таких задач нет — `not_enough_data`;
- есть — `repeated` (сигнал повторился, приложи цитату; `repeats` — на скольких из них) или `not_repeated`; `observations` — сколько сопоставимых задач просмотрено;
- запиши итог: `coach_journal` `append` с `check_of` (формат ниже). Проверок может быть несколько, действует последняя;
- скажи человеку одной строкой. `not_repeated` значит «на N задачах повтора не было», а не «изменение доказанно помогло» — так и скажи. `not_enough_data` — «сопоставимых задач пока не было, проверю, когда появятся».

Если человек пришёл с темой, проверки всё равно первыми, но коротко.

### 2. Обзор

Собери `findings` и события периода. Покажи до 3 тем; по каждой:
- что повторяется — одной-двумя фразами;
- 2–3 цитаты с привязкой: id сессии, время, `seq`;
- цена: время человека (паузы, повторные объяснения), токены, повторные просьбы — только то, что видно в данных; чего не видно, так и назови.

Отдельной строкой — полнота выборки.

Не предлагай тему, по которой в журнале есть `declined` или `not_justified` (тот же `topic_key`), если после этого решения не появилось новых оснований — сигналов той же темы в сессиях позже его `at`. Новые основания есть — скажи: «в прошлый раз (дата) вы отказались; с тех пор повторилось N раз».

Спроси, с какой темы начать. Если человек пришёл с темой, сразу переходи к шагу 3 по ней.

### 3. Причина — вместе с человеком

- Назови гипотезу и 1–2 альтернативы.
- Если подходящее правило или skill уже есть в настройках, сначала выясни, почему не сработало: не попало в контекст (другой проект, другой агент, файл не загружается), конфликтует с другим правилом или агент его не применил. Покажи файл и строку.
- Задай 1–2 вопроса, ответ на которые знает только человек: намеренно ли так было, чего он ждал, важно ли это.
- Без подтверждения человека причина остаётся гипотезой, и изменение предлагается как проверка (`test`), а не как решение.

### 4. Один слой

Выбери один слой и объясни, почему не соседний.

| Основание | Слой (`layer`) | Что меняется |
|---|---|---|
| Поведение человека: длинные сессии, долгие паузы, постановка задачи | Опыт (`experience`) | Договорённость; если полезно — шаблон (файл передачи, шаблон постановки) |
| Устойчивое предпочтение или правило | Инструкции (`instructions`) | `~/.codex/AGENTS.md`, `~/.claude/CLAUDE.md`, проектные `AGENTS.md`/`CLAUDE.md` |
| Повторяемая процедура, требующая суждения | Skill (`skill`) | Правка существующего или новый в `~/.codex/skills`, `~/.claude/skills` |
| Условие, которое нужно проверять во время работы; операция с определённым входом и выходом; доступ, MCP, разрешения, модель | Техника (`technical`) | Hook, скрипт, конфиг MCP, настройки разрешений и модели |

Допустимый исход — «изменение не обосновано» (`not_justified`): причина разовая, в окружении или в самом агенте при уже действующем правиле. Сбой окружения (нет программы, не настроен доступ) — починка в слое «Техника», а не правило.

Для нового skill нужен тестовый пример из сессии: реплика человека, на которой skill должен сработать, и что он должен сделать.

### 5. Точное изменение

Прочитай текущий файл и покажи diff; для нового skill — полный текст. Объясни, как это будет работать и как откатить. Посчитай `shasum -a 256` файла до изменения.

Инструкции Codex по умолчанию — `~/.codex/AGENTS.md`. Если персонализация Codex задана в поле настроек приложения, файл не правь: дай блок для вставки и скажи, где это поле.

### 6. Внести после явного «да»

- Одно изменение за раз. «Да» относится только к показанному diff.
- Файлы своего агента и другого агента на этой машине правишь прямо в сессии, с согласия человека.
- Нужно поле в настройках приложения — дай блок для вставки и скажи, где это поле.
- После правки посчитай `sha256` снова и покажи итоговый diff.

### 7. Журнал

Любое решение по теме записывай через `coach_journal` `append`: `applied`, `declined`, `not_justified` или `test`. Одна запись — одно изменение одной цели.

```json
{"op": "append", "entry": {
  "topic_key": "scope-creep-engineering",
  "agent": "codex",
  "topic": "Агент делает больше, чем просили",
  "findings": ["live:804bbff68ec9807bbc33"],
  "evidence": [{"session": "019f…", "at": "2026-09-30T08:14:02Z", "seq": 412, "quote": "до 200 символов, маскировано"}],
  "decision": "applied",
  "layer": "instructions",
  "target": "~/.codex/AGENTS.md",
  "before_sha256": "…", "after_sha256": "…",
  "change": "Кратко, что изменено",
  "rollback": "Как откатить",
  "check": "Класс задач и сигнал, по которым проверять",
  "check_after": "2026-10-08"
}}
```

- `id` и `at` ставит журнал. `topic_key` — устойчивый ключ темы латиницей; для темы, которая уже была в журнале, бери прежний.
- `declined` и `not_justified`: `layer` может быть `null`; `target`, `change`, `rollback`, `check` не нужны. Цитаты нужны всегда.
- `applied` и `test`: обязательны `layer`, `change`, `rollback`, `check`, `check_after` (обычно через 7 дней) и `target` (кроме слоя `experience`).
- Итог проверки — отдельная запись: `{"check_of": "<id>", "topic_key": "<тот же>", "agent": "codex", "result": "not_repeated", "observations": 4}`; для `repeated` — ещё `repeats` (на скольких из `observations` повторилось, дашборд пишет «повторилось на N из M») и `evidence`. Проверок может быть несколько; `read` показывает у изменения последнюю (`result`, `observations`, `repeats`, `checked_at`) и их число (`checks`).

## Правила

- Не дублировать. Если правило уже есть в настройках, но нарушается, уточнять существующее (файл и строка), а не добавлять второе.
- Сначала поправить существующий skill, потом создавать новый. Имя нового — по классу задач, а не по сегодняшней задаче.
- Не превращать в правило: сбои окружения (это починка), утверждения «инструмент X не работает», разовые ошибки, разовые задачи, способы, которые так и не сработали.
- Предпочтение, относящееся к задаче, у которой есть skill, — в этот skill; общее — в инструкции. Никогда в оба места.
- Без цитат из сессий темы нет.
- Без явного согласия человека ничего не менять.
- Секреты, пути клиентов и имена третьих лиц маскируются везде: в цитатах, в изменениях и в журнале. В журнал попадают ссылки на события и цитата не длиннее 200 символов.

Не делаешь: фоновых и плановых запусков, слежения за вставками по маркерам, уборки старых правил.
````

**Шаг 4.** `python3 -m unittest analytics.test_coach_skill -v` — зелёный.

**Шаг 5.** `git add analytics/coach analytics/test_coach_skill.py && git commit -m "hottell-coach: skill — методология, правила дословно, данные hottell-local"`

## T10: Установка skill и проверка вызова

**Шаг 1.** Симлинки на эту копию (после слияния — на основную, T17):

```bash
ln -sfn "$PWD/analytics/coach" ~/.codex/skills/hottell-coach
ln -sfn "$PWD/analytics/coach" ~/.claude/skills/hottell-coach
```

**Шаг 2. Claude Code:** `claude '/hottell-coach обзор, период 7 дней'` — первый ход вызывает `coach_journal` (read) и `findings` у `hottell-local`; ответ — до 3 тем со строкой полноты. Прервать после обзора.

**Шаг 3. Codex:** `codex '$hottell-coach обзор, период 7 дней'` — то же. Если skill не подхватился: (а) видит ли Codex skill через симлинк (`/skills` в TUI); нет — заменить симлинк каталога на каталог с симлинком `SKILL.md`; (б) работает ли `$hottell-coach` при вводе в TUI. В TUI работает, из аргумента нет — реплика для Codex в интерфейсе (T34): `Используй skill hottell-coach: <тема>, период 7 дней`. Вывод — в описание PR.

**Шаг 4.** Дописать в `README.md` раздел:

````markdown
## hottell-coach

Skill коуча — `analytics/coach/SKILL.md`: разговор об улучшении работы с агентом по данным локального MCP `hottell-local` (`hottell mcp-local`, регистрирует `hottell install`): `sessions_list`, `session_read` и `session_stats` с окном `from`/`to`, `findings`, `coach_journal`. Установка для обоих агентов — симлинки на основную копию репозитория:

```bash
ln -sfn ~/Life/projects/ai-hottell/analytics/coach ~/.codex/skills/hottell-coach
ln -sfn ~/Life/projects/ai-hottell/analytics/coach ~/.claude/skills/hottell-coach
```

Вызов: `$hottell-coach <id находки или тема>, период 7 дней` в Codex, `/hottell-coach …` в Claude Code; без темы — обзор. Журнал решений — `local-data/coach/journal.jsonl` (каталог задаёт `HOTTELL_DATA_DIR`).
````

`git commit -am "README: установка и вызов hottell-coach"`

## T11: Приёмка skill и MCP на реальных данных — 5 случаев

Владелец участвует лично: на вопросы о причине отвечает только он. Протокол — в `local-data/coach/acceptance-<дата>.md` (вне git: там реальные цитаты); в PR — только итог «прошёл/не прошёл» и номера записей журнала.

Перед началом: `findings` `{"since": "168h"}` у `hottell-local` — все источники `ok`; живой набор пересобран не раньше 5 минут назад (:8801 пересобирает сам).

| # | Как запустить | Прошёл | Провал |
|---|---|---|---|
| 1. Правило уже есть | Codex: `$hottell-coach просьба не раздувать реализацию, период 7 дней` (если 4 сессии темы старше — период 14 дней) | Skill находит действующее правило (файл и строка), разбирает, почему не сработало; исход — уточнение этой строки (diff по ней) или `not_justified`; запись в журнале | Предложено второе правило рядом с существующим |
| 2. Сбой окружения | Найти в `findings` ошибки вызова из-за отсутствующей программы или настройки (`friction:mcpfail`, `command not found`); вызвать с этим id | Слой `technical`, предложена починка (установка, конфиг, путь) | Предложено правило в инструкциях |
| 3. Повторяемая процедура | Тема, где человек несколько раз объяснял агенту один и тот же порядок действий | Правка существующего skill или новый skill с именем по классу задач и тестовым примером из сессии | Новый skill при подходящем существующем; имя по сегодняшней задаче; нет тестового примера |
| 4. Нет сопоставимых задач | После случая с `applied` (1–3) владелец просит поставить `check_after` на сегодня; новая сессия `$hottell-coach обзор, период 7 дней` | В начале — проверка журнала: `not_enough_data`, запись с `check_of`; человеку сказано, что задач после изменения не было. Если затем появилась сессия того же агента с задачей того же класса — следующий `обзор` проверяет запись снова: `repeated` или `not_repeated` с `observations` ≥ 1, у записи `checks: 2`; если новых сессий нет — повторная проверка пропущена с объяснением | Итог `not_repeated` без сопоставимых задач; проверка пропущена; запись с `not_enough_data` не перепроверена при новых сессиях |
| 5. Граница периода | Найти файл сессии, изменённый за 7 дней, с событиями старше 7 дней: `sessions_list {"since":"168h"}` и `session_stats {"id": …}` → `first` раньше окна | В обзоре нет цитат и чисел из событий старше `from` и нет находок только со старыми доказательствами; находки из `undated`, если названы, — с пометкой «время неизвестно»; есть строка полноты «просмотрено S сессий и E событий из F» | Цитата, время или находка из событий до окна |

Случаи 1 и 4 повторить в Claude Code (`/hottell-coach …`). Отказ хотя бы в одном — правка SKILL.md или инструментов, прогон заново; в протоколе — что изменено и почему.

Коммит только при правках: `git commit -m "hottell-coach: правки по приёмке — <что>"`.

---

## Часть C — интерфейс

## T12: `GET /api/coach/journal`

**Файлы:**
- Создать: `ui/coach.go`, `ui/coach_test.go`
- Изменить: `ui/main.go` (поле `journal`, флаг `-journal`, маршрут)

**Шаг 1. Падающий тест** `ui/coach_test.go`:

```go
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoachJournal(t *testing.T) {
	s, dir := testServer(t)
	s.journal = filepath.Join(dir, "coach", "journal.jsonl")
	h := s.routes()
	if rec := get(t, h, "/api/coach/journal"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"entries":[]`) {
		t.Fatalf("нет журнала — пустой список: %d %s", rec.Code, rec.Body)
	}
	os.MkdirAll(filepath.Dir(s.journal), 0o700)
	os.WriteFile(s.journal, []byte(`{"id":"a","topic_key":"k","at":"2026-10-01T06:00:00Z","agent":"codex","decision":"applied","layer":"instructions","change":"x","result":null,"observations":null}
{broken
{"id":"b","check_of":"a","topic_key":"k","at":"2026-10-09T00:00:00Z","agent":"codex","layer":null,"result":"not_repeated","observations":5}
{"id":"b2","check_of":"a","topic_key":"k","at":"2026-10-16T00:00:00Z","agent":"codex","layer":null,"result":"repeated","observations":2,"repeats":1}
{"id":"c","topic_key":"k2","at":"2026-10-02T06:00:00Z","agent":"claude","decision":"declined","layer":null,"result":null,"observations":null}
`), 0o600)
	rec := get(t, h, "/api/coach/journal")
	var out struct {
		Entries []map[string]any `json:"entries"`
		Broken  int              `json:"broken_lines"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Entries) != 2 || out.Broken != 1 {
		t.Fatalf("решения без проверок, битая строка посчитана: %+v", out)
	}
	a := out.Entries[0]
	if a["result"] != "repeated" || a["observations"] != float64(2) || a["checked_at"] != "2026-10-16T00:00:00Z" || a["checks"] != float64(2) || a["repeats"] != float64(1) {
		t.Fatalf("в записи a — последняя из двух проверок: %+v", a)
	}
}
```

**Шаг 2.** `cd ui && go test -count=1 -run CoachJournal ./...` — падает: `s.journal undefined`.

**Шаг 3. `ui/coach.go`:**

```go
package main

import (
	"bufio"
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
// действует последняя проверка, checks — сколько их было.

func readCoachJournal(path string) (entries []map[string]any, broken int, err error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	idx := map[string]map[string]any{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		if json.Unmarshal(line, &m) != nil {
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
	return entries, broken, sc.Err()
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
```

**Шаг 4. `ui/main.go`.** В `server` после `dataDir`:

```go
	journal string // журнал hottell-coach; по умолчанию <родитель dataDir>/coach/journal.jsonl
```

В `main()` после флага `refresh`:

```go
	journal := flag.String("journal", "", "журнал hottell-coach (по умолчанию <родитель -data>/coach/journal.jsonl)")
```

После `s := &server{…}`:

```go
	s.journal = *journal
	if s.journal == "" {
		s.journal = filepath.Join(filepath.Dir(abs), "coach", "journal.jsonl") // local-data/ui и local-data/ui-live → local-data/coach
	}
```

В `routes()` после `/api/meta`:

```go
	mux.HandleFunc("GET /api/coach/journal", s.coachJournal)
```

**Шаг 5.** `cd ui && go vet ./... && go test -count=1 ./...` — зелёные.

**Шаг 6.** `git add ui/coach.go ui/coach_test.go ui/main.go && git commit -m "ui: /api/coach/journal — журнал коуча для «Исправлено»"`

## T13: «Исправлено» из журнала — перенесено в PR 4

Делается по прототипу v5.1 в T34.

## T14: Кнопки «Обсудить» — перенесено в PR 4

Делается по прототипу v5.1 в T34 (заголовок и темы) и T36 (лента сессии).

## T15: Приёмка интерфейса — перенесено в PR 4

Цикл из дашборда в Codex и Claude Code, отклонённая тема и «Исправлено» проверяются в T39. В PR 1 цикл проверяется без интерфейса: T11.

## T16: Документация

- `temp/docs/specs/hottell-contract/mcp.md` — короткий раздел «Локальный сервер `hottell-local`»: команда `hottell mcp-local` (stdio), записи в `~/.claude.json` и `config.toml`, что пишет `install` и снимает `uninstall`, что `~/.claude.json` не в наборе резервных копий (решение 12), что сервер не обращается к сервису.
- `temp/cmd/hottell` — doc-комментарий `main.go` (T6).
- `ui/README.md` — `/api/coach/journal`, флаг `-journal`.
- `ui/CONTRACT.md` — «Журнал коуча»: формат записи (ссылка на SKILL.md), свёртка проверок (`checks`), `broken_lines`.

`git commit -am "Документация: hottell-local, журнал коуча"`

## T17: PR 1

**Шаг 1.** Все проверки:

```bash
(cd temp && go vet ./... && go test -race ./... && golangci-lint run && go-arch-lint check) && (cd ui && go vet ./... && go test -count=1 ./...) && (cd ui/builder && python3 -m unittest -q) && python3 -m unittest analytics.test_coach_skill
```

**Шаг 2.** Навык superpowers:requesting-code-review; замечания — через superpowers:receiving-code-review.

**Шаг 3.** `git push -u origin claude/hottell-coach`; PR в `camp` «hottell-coach: разговор об улучшениях в Codex и Claude Code, локальный MCP hottell-local». В описании: части A–C, решения плана, итоги T8 (путь А или Б), T10, T11; ограничения (маскирование путей клиентов — на skill; `findings` свеж до 5 минут; `~/.claude.json` не в резервных копиях). Затем `ccd_pr` `get_status`/`bind_pr`.

**Шаг 4.** После слияния: симлинки skill — на основную копию; на пути Б — пересобрать `local-data/bin/hottell-next` из `camp`.

---

# PR 2 — живой дашборд: исправления сборщика

## T18: Ветка

```bash
cd ~/Life/projects/ai-hottell && git fetch origin
git worktree add .claude/worktrees/live-dashboard-fixes -b claude/live-dashboard-fixes origin/camp
cd .claude/worktrees/live-dashboard-fixes
(cd ui/builder && python3 -m unittest -q) && (cd ui && go test -count=1 ./...)
```

## T19 (2.1): «Записан только хвост» — только у resume без startup

**Файлы:** изменить `ui/builder/live.py` (присваивание `S["partial"]` в разборе сессии; новая функция рядом с `classify_session`); тест — `ui/builder/test_live.py`.

**Шаг 1. Падающие тесты** — новый класс в `test_live.py`:

```python
class PartialTest(unittest.TestCase):
    """partial — запись началась не с начала сессии. Codex Desktop и Claude Code присылают
    SessionStart source=resume и при возврате к сессии, записанной с первого события."""

    def _session(self, sid, t0, sources):
        hooks = [_hook(_ts(t0, 0), "SessionStart", sid, source=sources[0]),
                 _hook(_ts(t0, 1), "UserPromptSubmit", sid, turn_id="T1", prompt="Почини сборку"),
                 *_call(t0, sid, 5, "c1", '{"command":"make"}')]
        for k, src in enumerate(sources[1:], 1):
            s = 600 * k
            hooks += [_hook(_ts(t0, s), "SessionStart", sid, source=src),
                      _hook(_ts(t0, s + 1), "UserPromptSubmit", sid, turn_id=f"T{k + 1}", prompt="Продолжай"),
                      *_call(t0, sid, s + 5, f"c{k + 1}", '{"command":"make test"}')]
        ds, _ = run(hooks)
        return next(x for x in ds["sessions"] if x["id"] == sid)

    def test_resume_after_startup_is_recorded(self):
        s = self._session(_sid(T0, "a"), T0, ["startup", "resume", "resume"])
        self.assertEqual(s["sources"]["hooks"], "recorded")

    def test_resume_without_startup_is_partial(self):
        s = self._session(_sid(T0, "b"), T0, ["resume"])
        self.assertEqual(s["sources"]["hooks"], "partial")

    def test_late_first_hook_is_partial_even_with_startup(self):
        # uuid7: сессия создана за 2 ч до первого события хуков — проверка PARTIAL_AFTER_H остаётся
        s = self._session(_sid(T0 - dt.timedelta(hours=2), "c"), T0, ["startup"])
        self.assertEqual(s["sources"]["hooks"], "partial")
```

**Шаг 2.** `cd ui/builder && python3 -m unittest test_live.PartialTest -v` — падает `test_resume_after_startup_is_recorded` (`'partial' != 'recorded'`); два других проходят (фиксируют текущее поведение).

**Шаг 3. Реализация.** Рядом с `classify_session`:

```python
def resumed_without_start(starts):
    """True, если запись началась с возврата к сессии: первый SessionStart с source resume|startup — resume.
    clear и compact (Claude Code) границу записи не показывают и пропускаются."""
    for r in sorted(starts, key=lambda r: r["at"]):
        src = r.get("source") or ""
        if src == "startup":
            return False
        if src == "resume":
            return True
    return False
```

Присваивание `S["partial"] = …` заменить на:

```python
    S["partial"] = bool(S["created"] and first_hook and (first_hook - S["created"]).total_seconds() > PARTIAL_AFTER_H * 3600) \
        or resumed_without_start(starts)
```

**Шаг 4.** `python3 -m unittest -v` — всё зелёное.

**Шаг 5.** `git add ui/builder/live.py ui/builder/test_live.py && git commit -m "Live: resume после startup — не «записан только хвост»"`

## T20 (2.2): Короткий ID — первые 8 и последние 4 знака везде

**Файлы:** `ui/builder/telemetry.py` (функция), `ui/builder/live.py`, `ui/builder/build.py`, `ui/builder/conclusions.py`, `ui/static/index.html`; тест — `ui/builder/test_live.py`.

**Шаг 1. Падающий тест:**

```python
class ShortIdTest(unittest.TestCase):
    def test_short_id_format(self):
        self.assertEqual(L.T.short_id("01a0f57b-0000-7000-8000-000000004b74"), "01a0f57b…4b74")
        self.assertEqual(L.T.short_id("abc"), "abc")

    def test_sessions_started_in_one_minute_differ(self):
        # у uuid7 первые 8 знаков — время с шагом 65,536 с: две сессии в одном шаге
        base_ms = (int(T0.timestamp() * 1000) // 65536) * 65536 + 1000
        t1 = dt.datetime.fromtimestamp(base_ms / 1000, tz=dt.timezone.utc)
        t2 = t1 + dt.timedelta(seconds=30)
        a, b = _sid(t1, "a"), _sid(t2, "b")
        self.assertEqual(a[:8], b[:8])
        hooks = []
        for sid, t in ((a, t1), (b, t2)):
            hooks += [_hook(_ts(t, 0), "SessionStart", sid, source="startup"),
                      _hook(_ts(t, 1), "UserPromptSubmit", sid, turn_id="T1", prompt="Проверь сборку"),
                      *_call(t, sid, 5, "c-" + sid[-4:], '{"command":"make"}')]
        ds, _ = run(hooks)
        shorts = {s["id"]: s["short"] for s in ds["sessions"]}
        self.assertEqual(shorts[a], f"{a[:8]}…{a[-4:]}")
        self.assertNotEqual(shorts[a], shorts[b])
```

**Шаг 2.** Падение: `AttributeError: module 'telemetry' has no attribute 'short_id'`.

**Шаг 3.** В `ui/builder/telemetry.py` в разделе `# --- text`:

```python
def short_id(sid):
    """Короткий id сессии: первые 8 и последние 4 знака (01a0f57b…4b74). У uuid7 первые 8 знаков —
    время с шагом ~65 с, и сессии, начатые в одну минуту, по ним не различить."""
    s = str(sid or "")
    return s if len(s) <= 12 else f"{s[:8]}…{s[-4:]}"
```

**Шаг 4.** Заменить срезы **id сессии** (не `aid` субагента и не срезы списков `[:8]`):

```bash
grep -n "sid\]\[:8\]\|sid'\]\[:8\]\|sid\[:8\]" ui/builder/live.py ui/builder/telemetry.py ui/builder/build.py ui/builder/conclusions.py
```

Каждое вхождение `X[:8]`, где `X` — id сессии, → `T.short_id(X)` (в `telemetry.py` — `short_id(X)`; в `build.py`/`conclusions.py` — через их импорт `telemetry`). На ветке после #14 это: `live.py` — поле `"short"`, заголовок ленты, «Больше всего у …», список пустых сессий в `gaps`, `short = S["sid"][:8]`; `telemetry.py` — `"short"`, заголовок ленты, `short = P["sid"][:8]`; `build.py` — четыре сообщения `gaps`; `conclusions.py` — подпись группы.

Короткий ID на странице (`short()` в `index.html`) — в PR 4 (T33).

**Шаг 6.** `cd ui/builder && python3 -m unittest -v` — зелёные (тесты, сверявшие `[:8]`, обновить на новый формат).

**Шаг 7.** `git add -A ui/builder && git commit -m "Сборщик: короткий id сессии — первые 8 и последние 4 знака"`

## T21: Служебные сессии — подсказки Codex

Прототип v5.1 (HANDOFF, «Что в прототипе имитируется»): в правило типа сессии добавить подсказки Codex. Стартовая вкладка, прежняя T21, — в PR 4.

**Файлы:** `ui/builder/live.py` (`SYSTEM_PROMPTS`), тест — `ui/builder/test_live.py` (`SessionKindTest`).

**Шаг 1. Падающий тест** — в `SessionKindTest`:

```python
    def test_codex_ambient_suggestions_are_system(self):
        sid = _sid(T0, "f")
        hooks = [_hook(_ts(T0, 0), "SessionStart", sid, source="startup"),
                 _hook(_ts(T0, 1), "UserPromptSubmit", sid, turn_id="T1",
                       prompt="You are an expert at upholding safety and compliance standards for Codex ambient suggestions. Review"),
                 *_call(T0, sid, 5, "a1", '{"command":"git status"}')]
        ds, _ = run(hooks)
        s = next(x for x in ds["sessions"] if x["id"] == sid)
        self.assertEqual((s["kind"], s["kind_reason"]), ("system", "проверка подсказок Codex"))
```

**Шаг 2.** `cd ui/builder && python3 -m unittest test_live.SessionKindTest -v` — падает: `'user' != 'system'`.

**Шаг 3.** В `SYSTEM_PROMPTS` добавить строку:

```python
    (re.compile(r"^You are an expert at upholding safety and compliance standards for Codex ambient suggestions"),
     "проверка подсказок Codex"),
```

**Шаг 4.** Тесты зелёные; коммит: `git commit -am "Live: проверка подсказок Codex — служебная сессия"`.

## T22 (2.4): «Длит.» → «Работа» — перенесено в PR 4

Колонка «Работа» = `active_min`, `title` — сколько сессия была открыта: T36.

## T23 (2.5): Запуски по расписанию в KPI — перенесено в PR 4

KPI «Ваше время» и «Агент работал» без `kind=automation` с подписью «ещё N ч — по расписанию»: T35.

## T24 (приёмка 2): «Холодный кэш» — правка сборщика не нужна

По прототипу v5.1 карточка темы и «Трение» берут стоимость эпизодов из `friction.cost_usd` (её уже считает `aggregate_friction` по запросам детектора) и показывают её, только если все сессии сигнала в выборке. Проверка — в T39.

## T25: Проверка данных

**Шаг 1.** Живой набор кодом ветки в scratchpad (ClickHouse стенда: `docker compose -f local/docker-compose.yml up -d`):

```bash
python3 ui/builder/live.py --out <scratchpad>/ui-live-check
```

**Шаг 2.** По `dataset.json` (только чтение, без вывода текстов):

```bash
python3 - <<'EOF'
import json,collections
D=json.load(open("<scratchpad>/ui-live-check/dataset.json"))
S={s["id"]:s for s in D["sessions"]}
print("01a0f57b:", [s["sources"]["hooks"] for i,s in S.items() if i.startswith("01a0f57b")])     # recorded
print("повторы short:", [k for k,n in collections.Counter(s["short"] for s in S.values()).items() if n>1])  # []
print("служебные:", collections.Counter(s["kind_reason"] for s in S.values() if s["kind"]=="system"))
EOF
```

**Шаг 3.** :8800 — `python3 ui/builder/build.py --out <scratchpad>/ui-check` без ошибок, короткие ID в новом формате. Вид страниц проверяется в PR 4.

## T26: PR 2

Все проверки (как в T17), ревью, `git push -u origin claude/live-dashboard-fixes`, PR в `camp` «Сборщик живого дашборда: метка хвоста, короткие ID, служебные подсказки Codex». В описании — пункты 2.1, 2.2 и T21 со ссылками на тесты; 2.3–2.5 и «Холодный кэш» — в PR 4; `ccd_pr` `get_status`/`bind_pr`. После слияния пересобрать сервер дашборда: `ui/start-local.sh`.

---

# PR 3 — статус отправки: `/api/status`

Спека, раздел 3: ручной пакет для хаба не делать; раз в минуту выполнять `hottell status --json`, отдавать через `GET /api/status` (только loopback). Строка в шапке — в PR 4 (T34).

## T27: Ветка

Как T18, ветка `claude/send-status`, каталог `.claude/worktrees/send-status`.

## T28: `GET /api/status`

**Файлы:** создать `ui/sendstatus.go`, `ui/sendstatus_test.go`; изменить `ui/main.go` (поле, флаг, запуск, маршрут).

**Шаг 1. Падающий тест** `ui/sendstatus_test.go`:

```go
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeHottell — исполняемый скрипт, который печатает out и выходит с кодом code, как hottell status --json.
func fakeHottell(t *testing.T, out string, code int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "hottell")
	script := "#!/bin/sh\ncat <<'EOF'\n" + out + "\nEOF\nexit " + string(rune('0'+code)) + "\n"
	if err := os.WriteFile(p, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

const statusConnected = `{"ok":true,"version":"0.2.3","mcp":{"agent":"claude","url":"http://localhost:8080/mcp"},
"token":{"present":true,"last_sent":"2026-10-01T06:58:00Z"},"settings":{"cached":true,"version":7,"applied":7},
"queue":{"queued":0},"problems":[]}`

const statusSendError = `{"ok":false,"version":"0.2.3","mcp":{"agent":"claude","url":"http://localhost:8080/mcp"},
"token":{"present":true,"last_sent":"2026-10-01T06:40:00Z","last_error":{"status":500}},"settings":{"cached":true,"version":7,"applied":7},
"queue":{"queued":12},"problems":["последняя отправка не принята сервисом: 500","вторая проблема"]}`

func statusOf(t *testing.T, s *server) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSendStatusStates(t *testing.T) {
	for name, tc := range map[string]struct {
		bin    func(t *testing.T) string
		state  string
		origin string
	}{
		"бинаря нет":         {func(t *testing.T) string { return filepath.Join(t.TempDir(), "nope") }, "not_configured", ""},
		"подключено":         {func(t *testing.T) string { return fakeHottell(t, statusConnected, 0) }, "ok", "http://localhost:8080"},
		"отправка с ошибкой": {func(t *testing.T) string { return fakeHottell(t, statusSendError, 1) }, "problem", "http://localhost:8080"},
		"старый прототип":    {func(t *testing.T) string { return fakeHottell(t, "использование: hottell -agent claude|codex | mcp", 2) }, "not_configured", ""},
		"нет подключения":    {func(t *testing.T) string { return fakeHottell(t, `{"ok":false,"mcp":{},"problems":["MCP hottell не найден"]}`, 1) }, "not_configured", ""},
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := testServer(t)
			s.hottell = tc.bin(t)
			s.checkSendStatus()
			got := statusOf(t, s)
			if got["state"] != tc.state || (tc.origin != "" && got["service_origin"] != tc.origin) {
				t.Fatalf("%+v", got)
			}
			if tc.state != "not_configured" && got["status"] == nil {
				t.Fatal("вывод hottell status отдаётся как есть")
			}
		})
	}
}

func TestSendStatusLoopbackOnly(t *testing.T) {
	s, _ := testServer(t)
	s.hottell = fakeHottell(t, statusConnected, 0)
	s.checkSendStatus()
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil) // RemoteAddr 192.0.2.1
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "localhost:8080") {
		t.Fatalf("не loopback: %d %s", rec.Code, rec.Body)
	}
}

func TestSendStatusBeforeFirstCheck(t *testing.T) {
	s, _ := testServer(t)
	if got := statusOf(t, s); got["state"] != "unknown" {
		t.Fatalf("до первой проверки: %+v", got)
	}
}
```

**Шаг 2.** `cd ui && go test -count=1 -run SendStatus ./...` — падает: `s.hottell undefined`, `s.checkSendStatus undefined`.

**Шаг 3. `ui/sendstatus.go`:**

```go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"sync"
	"time"
)

// Статус отправки в сервис команды: раз в минуту `hottell status --json`, последний итог —
// в GET /api/status. Ключ MCP и токен коллектора hottell status не печатает, поэтому
// вывод отдаётся как есть; запрос не с loopback отклоняется.

// sendStatusTimeout bounds one hottell status: it asks launchd about the daemon.
const sendStatusTimeout = 20 * time.Second

type sendStatus struct {
	CheckedAt time.Time `json:"checked_at"`
	// State is ok, problem or not_configured (no binary, no MCP server, or a binary that
	// does not print the status as JSON, like the prototype).
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
	// ServiceOrigin is the origin of mcp.url: the links /telemetry and /connect lead there.
	ServiceOrigin string          `json:"service_origin,omitempty"`
	Status        json.RawMessage `json:"status,omitempty"`
}

type statusBox struct {
	mu   sync.Mutex
	last *sendStatus
}

func (b *statusBox) set(s sendStatus) { b.mu.Lock(); b.last = &s; b.mu.Unlock() }

func (b *statusBox) get() *sendStatus { b.mu.Lock(); defer b.mu.Unlock(); return b.last }

// checkSendStatus runs hottell status --json once and keeps the outcome.
func (s *server) checkSendStatus() {
	ctx, cancel := context.WithTimeout(context.Background(), sendStatusTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, s.hottell, "status", "--json").Output()
	s.status.set(classifyStatus(out, err, time.Now()))
}

func (s *server) watchSendStatus(every time.Duration) {
	s.checkSendStatus()
	for range time.Tick(every) {
		s.checkSendStatus()
	}
}

// classifyStatus reads hottell status --json. Exit 1 with JSON is a problem, not a failure.
func classifyStatus(out []byte, runErr error, now time.Time) sendStatus {
	st := sendStatus{CheckedAt: now.UTC(), State: "not_configured"}
	var exitErr *exec.ExitError
	if runErr != nil && !errors.As(runErr, &exitErr) {
		st.Reason = "бинаря hottell нет"
		if !errors.Is(runErr, fs.ErrNotExist) && !errors.Is(runErr, exec.ErrNotFound) {
			st.Reason = "hottell status не запустился: " + runErr.Error()
		}
		return st
	}
	var rep struct {
		OK  bool `json:"ok"`
		MCP struct {
			URL string `json:"url"`
		} `json:"mcp"`
	}
	if json.Unmarshal(out, &rep) != nil {
		st.Reason = "hottell status --json не дал JSON — бинарь не той версии"
		return st
	}
	if rep.MCP.URL == "" {
		st.Reason = "MCP hottell не подключён"
		return st
	}
	st.Status = out
	if u, err := url.Parse(rep.MCP.URL); err == nil && u.Scheme != "" && u.Host != "" {
		st.ServiceOrigin = u.Scheme + "://" + u.Host
	}
	st.State = "problem"
	if rep.OK {
		st.State = "ok"
	}
	return st
}

func (s *server) sendStatusHandler(w http.ResponseWriter, r *http.Request) {
	if !fromLoopback(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "только с этой машины"})
		return
	}
	st := s.status.get()
	if st == nil {
		writeJSON(w, http.StatusOK, map[string]string{"state": "unknown"})
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func fromLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	return err == nil && ip != nil && ip.IsLoopback()
}
```

**Шаг 4. `ui/main.go`.** В `server`: `hottell string // hottell for the send status; empty disables it` и `status statusBox`. Флаг:

```go
	home, _ := os.UserHomeDir()
	hottell := flag.String("hottell", filepath.Join(home, ".local", "bin", "hottell"), "hottell binary for the send status (empty disables it)")
```

После создания `s`: `s.hottell = *hottell` и `if s.hottell != "" { go s.watchSendStatus(time.Minute) }`. В `routes()`: `mux.HandleFunc("GET /api/status", s.sendStatusHandler)`.

**Шаг 5.** `cd ui && go vet ./... && go test -count=1 ./...` — зелёные.

**Шаг 6.** `git add ui/sendstatus.go ui/sendstatus_test.go ui/main.go && git commit -m "ui: /api/status — статус отправки из hottell status --json раз в минуту"`

## T29: Строка «Отправка» в шапке — перенесено в PR 4

Текст и вид строки — по README v5.1 («Отправка: работает / ошибка / не настроена»): T34, шаг 6.

## T30: Приёмка и PR 3

**Шаг 1. Три состояния спеки** — сервер проверки с флагом `-hottell` на поддельные бинари из теста T28 и на настоящий:

```bash
curl -s http://127.0.0.1:8813/api/status | python3 -m json.tool
```

| Состояние | `-hottell` | Ожидается |
|---|---|---|
| Бинаря нет | `/nonexistent` | `state: not_configured`, `reason` «бинаря hottell нет» |
| Подключено | скрипт с `statusConnected` | `state: ok`, `service_origin: http://localhost:8080`, `status` — вывод как есть |
| Последняя отправка с ошибкой | скрипт с `statusSendError` | `state: problem`, `status.problems[0]` — первая проблема |
| Настоящий бинарь | `~/.local/bin/hottell` | на прототипе — `not_configured` («бинарь не той версии»); после перехода на сервис (T8, путь А) — `ok` |

Строка в шапке по этим состояниям проверяется в T39.

**Шаг 2.** `ui/README.md` — флаг `-hottell` и `/api/status`; `ui/CONTRACT.md` — формат ответа `/api/status`.

**Шаг 3.** Все проверки, ревью, `git push -u origin claude/send-status`, PR в `camp` «Дашборд: /api/status — состояние отправки в сервис команды». `ccd_pr` `get_status`/`bind_pr`.

---

# PR 4 — интерфейс v5.1

Задание — пакет `design_handoff_hottell_dashboard_v5.1` (`HANDOFF.md` — порядок и приёмка, `README.md` — вид, значения, поведение и анимация, финальные; прототип `Hottell Dashboard v5.dc.html`). Интерфейс — в `ui/static/index.html` (ванильный JS и CSS, без сборки и зависимостей) и `GET /api/pulse` в `ui/main.go`. Прототип — референс вида и поведения, не код: класс `Component` написан для среды прототипа (`support.js`), её не переносить.

Условия начала: #14 слит (`kind`, `kind_reason`, `scope`, `friction[].by_session`, `impact_by_session`, `outcome_known`), PR 1–3 слиты (`/api/coach/journal`, `/api/status`, правки сборщика 2.1, 2.2, служебные подсказки Codex).

## T31: Ветка и пакет дизайна

**Шаг 1.** Ветка `claude/dashboard-v5.1` от свежего `camp`, каталог `.claude/worktrees/dashboard-v5.1` (как T18).

**Шаг 2.** Пакет уже лежит в основной копии (владелец разрешил 01.10.2026): `docs/design/hottell-dashboard-v5.1/` — `README.md`, `HANDOFF.md`, `Hottell Dashboard v5.dc.html`, `support.js`; `data/` с реальными репликами и путями туда не кладётся. Перенести в ветку и закоммитить:

```bash
D=docs/design/hottell-dashboard-v5.1 && mkdir -p "$D"
cp ~/Life/projects/ai-hottell/$D/* "$D"/
grep -c "/Users/" "$D"/*   # ожидается 0 во всех файлах
git add "$D" && git commit -m "Дизайн дашборда v5.1: задание и прототип (без данных)"
```

Прототип смотреть из распакованного архива с данными: `cd <распаковка> && python3 -m http.server 8790` → `http://127.0.0.1:8790/Hottell Dashboard v5.dc.html`. Дальше ссылки «README, раздел …» ведут на `docs/design/hottell-dashboard-v5.1/README.md`.

## T32: `GET /api/pulse`

**Файлы:** создать `ui/pulse.go`, `ui/pulse_test.go`; изменить `ui/main.go` (флаги `-clickhouse`, `-clickhouse-db`, маршрут, поле `pulse` в `/api/meta`), `ui/start-local.sh` (`-clickhouse http://127.0.0.1:8123` только у :8801).

**Шаг 1. Падающий тест** `ui/pulse_test.go`:

```go
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const sidLive = "01a0f57b-c10a-7243-9e0f-a1b774b94b74"

// fakeClickHouse отвечает на два запроса пульса и считает обращения.
func fakeClickHouse(t *testing.T, calls *int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		b, _ := io.ReadAll(r.Body)
		switch q := string(b); {
		case strings.Contains(q, "INTERVAL 10 SECOND"):
			io.WriteString(w, `{"t":"2026-10-01 06:58:10.000000000","agent":"codex","n":7}`+"\n"+`{"t":"2026-10-01 06:58:20.000000000","agent":"claude","n":3}`+"\n")
		case strings.Contains(q, "per_min"):
			io.WriteString(w, `{"sid":"`+sidLive+`","agent":"codex","cwd":"/Users/x/Life/projects/ai-hottell","last_at":"2026-10-01 06:58:25.000000000","per_min":12}`+"\n")
		default:
			http.Error(w, "unexpected query", 400)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func pulseOf(t *testing.T, s *server, remote string) (int, pulse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/pulse", nil)
	req.RemoteAddr = remote
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	var p pulse
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	return rec.Code, p
}

func TestPulse(t *testing.T) {
	var calls int32
	s, dir := testServer(t)
	s.clickhouse, s.chDB = fakeClickHouse(t, &calls).URL, "otel"
	os.WriteFile(filepath.Join(dir, "dataset.json"),
		[]byte(`{"sessions":[{"id":"`+sidLive+`","project":"ai-hottell","first":"Почини сборку"}]}`), 0o600)

	code, p := pulseOf(t, s, "127.0.0.1:5000")
	if code != 200 || len(p.Bars) != 2 || p.Bars[0].T != "2026-10-01T06:58:10Z" || p.Bars[0].N != 7 {
		t.Fatalf("bars: %d %+v", code, p)
	}
	if len(p.Active) != 1 || p.Active[0].First != "Почини сборку" || p.Active[0].Project != "ai-hottell" || p.Active[0].PerMin != 12 {
		t.Fatalf("active — первая реплика и проект из датасета: %+v", p.Active)
	}
	pulseOf(t, s, "127.0.0.1:5000")
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Fatalf("повтор в пределах 5 с берётся из кэша: обращений к ClickHouse %d", n)
	}
	if code, _ := pulseOf(t, s, "192.0.2.1:5000"); code != http.StatusForbidden {
		t.Fatalf("не loopback: %d", code)
	}
}

func TestPulseUnavailable(t *testing.T) {
	s, _ := testServer(t)
	if code, _ := pulseOf(t, s, "127.0.0.1:5000"); code != http.StatusNotFound {
		t.Fatalf("без -clickhouse пульса нет (:8800): %d", code)
	}
	s.clickhouse = "http://127.0.0.1:1" // никто не слушает
	code, p := pulseOf(t, s, "127.0.0.1:5000")
	if code != 200 || p.Error == "" || p.Bars == nil || len(p.Bars) != 0 {
		t.Fatalf("ClickHouse недоступен — ошибка в ответе, пустые ряды: %d %+v", code, p)
	}
}
```

Запуск — падение: `undefined: pulse`, `s.clickhouse`.

**Шаг 2. `ui/pulse.go`:**

```go
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

type pulseCache struct {
	mu   sync.Mutex
	last *pulse
}

func (s *server) pulseHandler(w http.ResponseWriter, r *http.Request) {
	if !fromLoopback(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "только с этой машины"})
		return
	}
	if s.clickhouse == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "пульса в этой версии нет"})
		return
	}
	s.pulse.mu.Lock() // один запрос к ClickHouse на всех: остальные ждут и берут кэш
	defer s.pulse.mu.Unlock()
	if p := s.pulse.last; p != nil && time.Since(p.At) < pulseTTL {
		writeJSON(w, http.StatusOK, p)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), pulseTimeout)
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
			a.Project = path.Base(a.Cwd)
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
func (s *server) chRows(ctx context.Context, sql string, row func([]byte) error) error {
	u := s.clickhouse + "/?" + url.Values{"database": {s.chDB}}.Encode()
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
```

Если ClickHouse стенда отдаёт `Timestamp` не в UTC (`SELECT timezone()`), добавить в `chRows` параметр `session_timezone=UTC`.

**Шаг 3. `ui/main.go`.** Поля `clickhouse, chDB string` и `pulse pulseCache`; флаги:

```go
	clickhouse := flag.String("clickhouse", "", "ClickHouse HTTP for the pulse (empty: no pulse, as :8800)")
	chDB := flag.String("clickhouse-db", "otel", "ClickHouse database")
```

Маршрут `mux.HandleFunc("GET /api/pulse", s.pulseHandler)`; в `meta` — `"pulse": s.clickhouse != ""`. В `ui/start-local.sh` у процесса :8801 — `-clickhouse http://127.0.0.1:8123`.

**Шаг 4.** `cd ui && go vet ./... && go test -count=1 ./...`; коммит `git commit -am "ui: /api/pulse — события хуков за 10 минут и идущие сессии, кэш 5 с"`.

## T33: Каркас страницы

**Файл:** `ui/static/index.html`. Читать README, разделы «Общие правила значений», «Шапка», «State Management», «Design Tokens».

**Шаг 1. Шапка** — две строки по README: бренд, переключатель «Живые данные | Глубокий разбор» (ссылки — из `/api/meta` `links`, источник — в `title`), вкладки в порядке «Что исправить · Обзор · Сессии · Трение · Инструменты и MCP · Skills»; вторая строка — период (по умолчанию 7 дн), агент, проекты, «Без служебных | Все», справа пульс (только если `meta.pulse`), строка отправки (T34, шаг 6) и «Обновить» (бывшее «Пересобрать»: `POST /api/rebuild`, затем `/api/dataset`; «Обновляю…» → «Без изменений», если `generated_at` тот же, иначе тихая подмена; «Не удалось» при ошибке; через 2,2 с — обратно). Надпись «есть новые данные — обновить» удалить.

**Шаг 2. Состояние** — объект `st` по README «State Management»; `view` по умолчанию `fix`, `days` — 7, `kind` — `work`.

**Шаг 3. Значения** — общие помощники:

```js
const NNBSP=" ";
const fmtN=n=>n==null||isNaN(n)?"—":Math.round(n).toLocaleString("ru-RU").replace(/\s/g,NNBSP);
const fmt$=v=>v==null||isNaN(v)?"—":v>=100?"$"+fmtN(v):"$"+v.toFixed(2);
const short=id=>{const s=String(id||"");return s.length<=12?s:s.slice(0,8)+"…"+s.slice(-4)};
const pad=n=>String(n).padStart(2,"0");
// местное время; сегодня — «14:22», раньше — «30.09 14:22»; UTC — в title
function when(iso){if(!iso)return {text:"—",title:""};const d=new Date(iso),now=new Date();
  const hm=pad(d.getHours())+":"+pad(d.getMinutes()),same=d.toDateString()===now.toDateString();
  return {text:same?hm:pad(d.getDate())+"."+pad(d.getMonth()+1)+" "+hm,title:d.toISOString().replace("T"," ").slice(0,16)+" UTC"}}
const fmtD=m=>m==null||isNaN(m)?"—":m>=60?Math.floor(m/60)+" ч "+Math.round(m%60)+" м":Math.round(m)+" м";
const fmtOpen=m=>m!=null&&m>=1440?"23 ч+":fmtD(m); // «Открыта» больше суток
```

ID сессии — моноширинной кнопкой `data-copyid`, по клику `navigator.clipboard.writeText(полный id)` и подпись «ID скопирован» на 1,6 с. `plural` — существующий.

**Шаг 4. База подсчётов** — одна функция выборки `S` (период, агент, проект, «Без служебных» скрывает только `kind=system`); все числа страницы считаются по ней (README «База подсчётов»). Пустой проект — «без проекта».

**Шаг 5.** Узкий экран: сохранить адаптивные правила `index.html`, на 390px нет горизонтальной прокрутки, широкие таблицы — в панелях с `overflow-x:auto`. Тема — системная.

**Шаг 6.** Коммит: `git commit -am "ui v5.1: каркас — шапка, фильтры, состояние, форматы значений"`.

## T34: «Что исправить»

README, раздел «Экран «Что исправить»». Правила данных, которых в README нет или которые в прототипе сделаны иначе, — здесь.

**Шаг 1. Эпизоды сигнала в выборке** (README v5.1: доказательства не использовать — они обрезаны до 60):

```js
// friction-сигнал в выборке: сессии, полный ли охват, эпизоды (null — счёта по сессиям нет)
function frIn(f,ids){const all=f.sessions||[],ss=all.filter(x=>ids.has(x)),full=ss.length===all.length;
  const eps=!ss.length?0:full?f.count:f.by_session?ss.reduce((n,x)=>n+(f.by_session[x]||0),0):null;
  return {ss,full,eps}}
// тема и её сигнал — по pattern_id (прототип сопоставляет по названию — так не делать)
const signalOf=f=>(D.friction||[]).find(r=>r.key===f.pattern_id)||null;
function topicEpisodes(f,ids){const r=signalOf(f);if(r)return {...frIn(r,ids),sig:r};
  const all=f.sessions||[],ss=all.filter(x=>ids.has(x)),bs=f.impact_by_session;
  return {ss,full:ss.length===all.length,eps:bs?ss.reduce((n,x)=>n+(bs[x]||0),0):null,sig:null}}
```

Стоимость темы — `sig.cost_usd`, только при `full`; иначе без суммы и « · в выбранных».

**Шаг 2. Цикл улучшений** — шесть шагов README; «Сигналы» — сумма `frIn(...).eps` по сигналам с сессиями в выборке, `null` хотя бы у одного — «—» и «нет счёта по сессиям». Шаги 4–6 — по `JOURNAL` (`/api/coach/journal` из T12 отдаёт свёрнутые проверки): «Обсуждено» — все решения, «Внедрено» — `applied`, «Проверено» — с `result`, « из N» внедрённых. Пустой журнал — «—», «журнал пуст» и подсказка под колонками 4–6.

**Шаг 3. Заголовок и «Обсудить»** — по README, но команда для терминала в **одинарных** кавычках (решение 1; в прототипе двойные — в zsh и bash `$hottell` подставится пустым):

```js
const shq=s=>"'"+String(s).replace(/'/g,"'\\''")+"'";
const periodPhrase=()=>st.days?`период ${st.days} ${plural(st.days,"день","дня","дней")}`:"весь период";
const coachReply=(agent,subject)=>(agent==="codex"?"$":"/")+`hottell-coach ${subject}, ${periodPhrase()}`;
const coachCmd=(agent,subject)=>(agent==="codex"?"codex ":"claude ")+shq(coachReply(agent,subject));
```

Субъекты: заголовок — `обзор`, тема — её `id`, лента сессии — `сессия <полный id>` (T36). Реплика для Codex — по итогу T10 PR 1.

**Шаг 4. Темы** — карточки по README (свёрнуты, до 3, «Ещё N»), только `scope ≠ collection`, сортировка sev, затем эпизоды. Строка «почему» — по `by_session` сигнала: «Больше всего в 01a0f058…5b9f · <проект>: N из M эпизодов».

**Шаг 5. «Исправлено»** — строки журнала по README, все решения (в том числе `declined`, `not_justified`). Итог:

```js
const LAYER_RU={experience:"Опыт",instructions:"Инструкции",skill:"Skill",technical:"Техника"};
function journalTag(j){
  if(j.decision==="declined"||j.decision==="not_justified")return ["не внедряли","muted"];
  if(j.result==="not_repeated")return [`повтора не было на ${j.observations} ${plural(j.observations,"задаче","задачах","задачах")}`,"ok"];
  if(j.result==="repeated")return [`повторилось на ${j.repeats} из ${j.observations}`,"warn"];
  if(j.result==="not_enough_data")return ["мало данных",""];
  return [j.check_after?`проверка ${j.check_after.slice(8,10)}.${j.check_after.slice(5,7)}`:"без проверки",""];
}
```

Раскрытие «Основания · Изменение · Проверка · Откат»: «Основания» — `topic`, затем ссылки доказательств (`session` 8…4 · местное время · цитата), «Изменение» — `change` и `target`, «Проверка» — `check` и итог, «Откат» — `rollback`. Отдельного поля «основания» в журнале нет (решение 17). Новая запись въезжает сверху; журнал перечитывается при фокусе окна и по таймеру обновления.

**Шаг 6. Строка отправки** (шапка, README «Строка 2», п. 2) по ответу `/api/status`:

```js
function sendLine(x){const s=x.status||{},o=x.service_origin,a=(p,t)=>o?`<a href="${esc(o+p)}" target="_blank" rel="noopener">${t}</a>`:t;
  if(x.state==="ok"){const q=(s.queue||{}).queued??0,v=(s.settings||{}).version,ls=(s.token||{}).last_sent;
    return `<b class="ok">Отправка: работает</b>${ls?` · последняя ${ago(ls)}`:""} · в очереди ${fmtN(q)}${v!=null?` · настройки v${v}`:""} · ${a("/telemetry","Что отправлять →")}`}
  if(x.state==="problem")return `<b class="warn">Отправка: ошибка</b> · ${esc((s.problems||[])[0]||"")} · ${a("/connect","Подключение →")}`;
  if(x.state==="not_configured")return `<span class="muted">Отправка не настроена · ${a("/connect","Подключение →")}</span>`;
  return ""}
```

`ago()` — «только что / N мин назад / N ч назад». Опрос — раз в минуту, в скрытой вкладке — нет.

**Шаг 7. Здоровье сбора данных и Ограничения данных** — по README, разделы 6 и 7; ограничения — пункты страницы, затем `dataset.gaps` без префикса `<id8>:`.

**Шаг 8.** Коммит: `git commit -am "ui v5.1: «Что исправить» — цикл, темы, «Обсудить», «Исправлено», строка отправки"`.

## T35: «Обзор»

README, раздел «Экран «Обзор»»: KPI в порядке «Ваше время · Агент работал · Ваши сессии · Расходы · Попадание в кэш · Ошибки инструментов». «Ваше время» и «Агент работал» — без `kind=automation`, подпись «ещё N ч — по расписанию» (пункт 2.5 спеки); «Ваши сессии» — `kind=user`, подпись про расписание и скрытые служебные. Расходы — «≈ $X» и «у N из M» при неполноте. Графики и списки — по README. Коммит: `git commit -am "ui v5.1: «Обзор»"`.

## T36: «Сессии» и лента сессии

README, разделы «Экран «Сессии»» и «Экран «Лента сессии»»:
- таблица `table-layout:fixed`, колонки и ширины README; «Работа» = `active_min`, `title` «открыта 23 ч 42 м» (пункт 2.4 спеки); «Тип» с `kind_reason` в `title`; «Задача» в одну строку; «Сигналы» — один тег и «+N»; «Исход» в живых данных не показывать;
- шапка ленты: ID 8+4 с копированием, «записан только хвост» — по `sources.hooks = partial` (после T19 PR 2 ложных меток нет), «Обсудить эту сессию» — `coachReply(agent, "сессия "+s.id)`;
- лента: «Главное | Все события», заголовки ходов, «идёт сейчас», дописывание снизу и плашка «+N событий ↓».

Коммит: `git commit -am "ui v5.1: «Сессии» и лента сессии"`.

## T37: «Трение», «Инструменты и MCP», «Skills»

README, одноимённые разделы. «Трение»: «Стоимость эпизодов» — `cost_usd` только при полном охвате сигнала выборкой, иначе «—» (приёмка пункта 2 спеки про «Холодный кэш»); эпизоды — `frIn`. Коммит: `git commit -am "ui v5.1: «Трение», «Инструменты и MCP», «Skills»"`.

## T38: Анимация, пульс, тихое обновление

README, раздел «Анимация: живой процесс»:
- пульс по `/api/pulse` раз в 5 с: 60 столбиков, сдвиг `translateX(3px) → 0` за 400 ms, точка с кольцом при новых событиях, тишина после 12 пустых столбиков, выпадающий список «Идут сейчас»; при `error` в ответе — серая точка и `title` с ошибкой;
- тихое обновление при новом `generated_at`: досчёт чисел за 600 ms, вспышка изменённых карточек, точка между шагами цикла, въезд новой записи журнала;
- только `transform` и `opacity`, кривая `cubic-bezier(.2,0,0,1)`;
- `prefers-reduced-motion: reduce` — без движения (правило из текущего `index.html` распространить на `animation`);
- `document.visibilityState === "hidden"` — опросы `/api/pulse`, `/api/dataset`, `/api/status`, `/api/coach/journal` останавливаются, при возврате — один немедленный запрос.

Коммит: `git commit -am "ui v5.1: пульс, тихое обновление, анимация с учётом reduced-motion"`.

## T39: Приёмка и PR 4

**Шаг 1. Сервер проверки** — копия `local-data/ui-live` в scratchpad, страница из ветки (`-static static`), `-clickhouse http://127.0.0.1:8123`, журнал — синтетический (записи `applied` с двумя проверками, `repeated` с `repeats`, `test` до срока, `declined`), `-hottell` — поддельные бинари из T28 для трёх состояний отправки. Отдельно — :8800-копия без `-clickhouse`.

**Шаг 2. Приёмка HANDOFF.md** — по пунктам (встроенный браузер: `read_page`, `javascript_tool` только для чтения, `resize_window` 1440×900 и 390px, вкладка «Сеть» через `read_network_requests`):
- открывается «Что исправить» за 7 дней, тема системная;
- «Сигналы» в цикле = сумма эпизодов тем и строк «Трения» при той же выборке; без `by_session` — «—» и «нет счёта по сессиям»;
- «Сессии» при 1440×900 — около 20 строк по 36px, «Задача» читается, в «Сигналах» один тег и «+N»;
- «Ваши сессии», «Агент работал», «Расходы», строка полноты и «Сессии» считают одну выборку и называют расписание и скрытые служебные;
- склонения «1 событие», «2 события», «5 событий»;
- 390px — нет горизонтальной прокрутки страницы;
- `prefers-reduced-motion` — без движения; скрытая вкладка не опрашивает `/api/pulse` и `/api/dataset`;
- :8800 — тот же каркас без пульса, без регрессий.

**Шаг 3. Приёмка интерфейса из спеки:**
- пункт 1: по кнопке на карточке и в заголовке — в Codex и в Claude Code цикл «тема с цитатами → вопрос о причине → одно изменение с diff или „не обосновано“ → запись в журнал»; запись появляется в «Исправлено» и в цикле; отклонённая тема без новых оснований не предлагается снова; «Исправлено» показывает итог и число наблюдений («повтора не было на N задачах», «повторилось на N из M»);
- пункт 2 на :8801: 01a0f57b не помечена «записан только хвост»; короткие ID в «Сессиях» не повторяются; открывается «Что исправить»; у «Холодного кэша» в карточке и в «Трении» — стоимость эпизодов детектора; :8800 без регрессий;
- пункт 3: строка отправки совпадает с `hottell status --json` в трёх состояниях — бинаря нет, подключено, последняя отправка с ошибкой.

**Шаг 4.** `cd ui && go vet ./... && go test -count=1 ./...`, `cd ui/builder && python3 -m unittest -q` — зелёные. Ревью, `git push -u origin claude/dashboard-v5.1`, PR в `camp` «Дашборд v5.1: цикл улучшений, темы, журнал коуча, пульс». В описании — скриншоты экранов, итоги приёмки, отличие от прототипа (кавычки команды, связь темы с сигналом по `pattern_id`). `ccd_pr` `get_status`/`bind_pr`. После слияния — `ui/start-local.sh`.

---

## Сверка со спекой

| Требование спеки | Задача |
|---|---|
| Кнопки «Обсудить…» в начале «Что исправить» и на карточках; реплика и команда | T34 (кавычки — решение 1), T36 (лента) |
| Вызов без темы начинается с обзора | T9 «Вход», T10 |
| Локальный MCP `hottell-local` в `temp/cmd/hottell` (`hottell mcp-local`, stdio): перенос `sessions_list`, `session_read`, `session_stats` | T1, T2, T6 |
| Регистрация в конфигах обоих агентов командой `hottell install` | T7 (и снятие в `uninstall`) |
| `from`/`to` по времени событий в `session_read`, `session_stats` | T3 |
| Дочитывание до `next_offset = 0`, полнота выборки в обзоре | T3 (`coverage`, `offset`, счётчики по всей сессии), T9 |
| `findings`: оба набора, `since`/`until`/`source`/`id`, компактный ответ, `HOTTELL_DATA_DIR` | T4, T6 (каталог — в `mcpLocalConfigFrom`) |
| `coach_journal`: `read`/`append`, файл 0600, одна строка на запись | T5 |
| Методология 1–7, таблица слоёв, правила дословно, запись журнала | T9 |
| Симлинки в `~/.codex/skills`, `~/.claude/skills`, команда в README | T10 |
| Тесты: события вне периода, оба набора, нет файлов, журнал 0600 | T3, T4, T5 |
| Приёмка skill на 5 случаях | T11 |
| `/api/coach/journal`, «Исправлено» с итогом и числом наблюдений | T12, T34 |
| Цикл из дашборда в обоих агентах; отклонённая тема не повторяется | T39 |
| Вид и расположение элементов — по `2026-10-01-ux.spec.md` и пакету v5.1 | PR 4, T31–T39 |
| «Открыто»: поле настроек Codex → блок для вставки | T9 (SKILL.md, шаги 5–6) |
| 2.1, 2.2 (сборщик) | T19, T20 |
| 2.2 (страница), 2.3–2.5 и приёмка пункта 2 в браузере | T33, T35, T36, T37, T39 |
| Служебные подсказки Codex (HANDOFF v5.1) | T21 |
| 3: `GET /api/status` раз в минуту, только loopback | T28 |
| 3: строка в шапке в трёх видах, ссылки на `/telemetry` и `/connect` по `mcp.url` | T34, T39 |
| 3: приёмка в трёх состояниях | T30 (API), T39 (строка) |
| Пульс `/api/pulse` (ux-спека, HANDOFF v5.1) | T32, T38 |
| Приёмка HANDOFF v5.1 | T39 |
| Не делать: изменения без согласия, фоновый коуч, маркеры, ручной пакет для хаба | SKILL.md «Правила»; пакета для хаба в плане нет |
| «Открыто»: выбор отдельных сессий для отправки | решение владельца, в план не входит |
