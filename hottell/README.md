# hottell — шиппер lifecycle-хуков агентов в otel-lab

Забирает то, что нативный OTel агентов не отдаёт (у Codex — почти всё детальное:
полные `tool_input`/`tool_response`, субагенты, компакты, жизненный цикл сессии),
и шлёт в приёмник `otel-lab` тем же OTLP/HTTP. Токены и стоимость остаются за
нативным экспортом — их hottell не дублирует; исключение — токены хода Codex,
которые нативный OTel Desktop-приложения к ходу не привязывает (см. «Обогащение
Codex из rollout»).

Один статический Go-бинарь без зависимостей, два режима:

- **хук-режим** (`hottell -agent claude|codex`) — агент подаёт JSON события на
  stdin; бинарь пишет событие файлом в спул (локально, миллисекунды), поднимает
  дрейнер и выходит с кодом 0 всегда — телеметрия не имеет права мешать агенту.
  Сети на этом пути нет, недоступность коллектора на агентов не влияет.
- **дрейнер** (`hottell drain`) — фоновый отправщик: синглтон на flock,
  разгребает спул батчами в `/v1/logs`, при недоступности ретраит с бэкоффом,
  на пустом спуле умирает. Резидентного сервиса нет: дрейнера поднимает каждое
  событие, лишний запуск утыкается в flock и сразу выходит.

- **MCP-сервер** (`hottell mcp`) — stdio-сервер для агентов: тулы анализа
  сессий и настройки телеметрии. Install регистрирует его у обоих агентов
  (`~/.claude.json` → `mcpServers.hottell`, `~/.codex/config.toml` →
  `[mcp_servers.hottell]`), uninstall снимает. SDK — официальный
  `github.com/modelcontextprotocol/go-sdk` (а не `mark3labs/mcp-go`): его ведут
  авторы протокола вместе с Google, типизированные тулы со схемой из структур.

**Буфер** — спул-каталог: настраивается лимитами `buffer_max_events` /
`buffer_max_mb`, при переполнении дропаются старые события (свежее ценнее).
Буфер переживает перезагрузку: недоставленное уйдёт, когда сеть вернётся.

## Бэкап и полный откат

Перед каждой установкой сохраняются исходные файлы `~/.claude/settings.json`,
`~/.claude.json`, `~/.codex/hooks.json` и `~/.codex/config.toml`.
Каталог выводится перед изменениями: `~/.config/hottell-backups/<дата>-<id>`;
каталог имеет права 0700, копии — 0600. Ошибка бэкапа останавливает установку.
Бэкапы не удаляются командой `uninstall`.

`uninstall` удаляет хуки и MCP, но оставляет OTel-настройки. Полный откат конфигов:

```sh
# Из корня репозитория; отдельный бинарь сохраняется даже после uninstall.
mkdir -p local-data/bin
(cd hottell && go build -o ../local-data/bin/hottell .)
local-data/bin/hottell restore -backup /полный/путь/к/бэкапу
```

Восстановление заменяет четыре файла целиком, включая настройки OTel;
файлы, отсутствовавшие до установки, удаляются. Закройте сессии агентов перед
откатом и перезапустите их после. Изменения, сделанные после выбранного бэкапа,
уйдут из активных конфигов: перед восстановлением автоматически сохраняется
ещё один бэкап текущих файлов. Бэкап может содержать секреты — не публикуйте его.
Это откат конфигов агентов, не удаление собранных данных или установки hottell.
Если установка была выполнена старой версией без бэкапа, первоначальное состояние
автоматически восстановить невозможно.

## Что шлётся и откуда (scope)

Решение принимается в хук-режиме до записи в буфер:

1. `HOTTELL_OFF=1` в окружении сессии — киллсвитч, молчим;
2. `skip_events` в конфиге — перечисленные события не шлются;
3. маркер `.hottell` — ищется от `cwd` события вверх по дереву, ближайший
   выигрывает; пустой файл = слать, `{"send": false}` — явный отказ при любом
   scope, `{"send_prompts": false}` — переопределение конфига для проекта;
4. `scope` в конфиге: **`marked` (по умолчанию)** — только проекты с маркером,
   `all` — отовсюду, кроме явного отказа.

Маркер можно положить и выше проектов (например, в `~/projects/work`) — он
покроет всё дерево. MCP-тул `project_scope on|off|status` управляет проектом
одной командой: маркер плюс `env` в `.claude/settings.local.json` для
нативного потока Claude Code (`CLAUDE_CODE_ENABLE_TELEMETRY=0` перекрывает
глобальное включение). Codex по-проектно нативный OTel не умеет (проектный
`[otel]` игнорирует) — только глобально или профилями; его хуки фильтруются
маркером как обычно.

## Схема событий

Каждое событие — log record: `Body = agent.hook.<Событие>` (`PreToolUse`,
`PostToolUse`, `UserPromptSubmit`, `Stop`, `SubagentStart/Stop`,
`SessionStart/End`, `Pre/PostCompact`, `PermissionRequest`, `Interrupt`,
`Notification`), атрибуты — плоский payload хука (объекты — JSON-строкой,
поля длиннее `max_field_bytes` обрезаются) плюс `agent=claude|codex`.
Ресурс: `service.name=agent-hooks`, `host.name`, `deployment.environment=lab`.
Ложится в тот же `otel.otel_logs`, что и нативные события, — сравнение агентов
одним запросом:

```sql
SELECT LogAttributes['agent'], Body, count()
FROM otel.otel_logs WHERE ServiceName = 'agent-hooks'
GROUP BY 1, 2 ORDER BY 1, 3 DESC
```

На `SessionStart` добавляется `hottell.skill_snapshot`: имена, описания,
хэши и время изменения локально установленных пользовательских и проектных
`SKILL.md`. Абсолютные пути и тело skill не отправляются. Снимок исключает
plugin skills и показывает файлы на диске, а не доступность или вызов skill
агентом. `complete=false` и `limit_hit` запрещают считать его исчерпывающим
списком для D19. Объект ограничен размером атрибута.

**Промпты**: `UserPromptSubmit` несёт полный текст и обходит выключенный
`log_user_prompt` Codex. Управляется `send_prompts` в конфиге (сейчас `true` —
решение «сначала всё забрать»); при `false` текст заменяется заглушкой ещё до
записи в спул, то есть не покидает машину.

## Обогащение Codex из rollout

Хуки Codex не отдают двух ключевых фактов: PostToolUse для shell-команды —
голый вывод без кода выхода, Stop — без токенов хода. Оба лежат в rollout
сессии (`transcript_path` из payload хука). Дрейнер — не хук, горячий путь не
удлиняется — перед отправкой дочитывает их и добавляет атрибуты:

| Событие | Атрибут | Откуда |
|---|---|---|
| `PostToolUse` | `hottell.exit_code` (int, если известен) | `item_completed` с `item.id == tool_use_id` (`CommandExecution.exit_code`); запасной путь — заголовок `function_call_output`/`custom_tool_call_output` с `call_id == tool_use_id` (`Exit code: N`, `Process exited with code N`, `metadata.exit_code`) |
| | `hottell.tool_status` | `item.status` как в rollout: `completed`, `failed`, `declined`, … (CommandExecution, McpToolCall, FileChange, CollabAgentToolCall) |
| | `hottell.tool_duration_ms` (int) | `item.duration`, `item.durationMs` или `completed_at_ms − started_at_ms`; в запасном пути — `Wall time` |
| `Stop` | `hottell.turn.responses` | число ответов модели в ходе: `token_usage_record` с этим `turn_id` |
| | `hottell.turn.input_tokens`, `hottell.turn.cached_input_tokens`, `hottell.turn.output_tokens`, `hottell.turn.reasoning_output_tokens` | `turn_token_usage` последнего `token_usage_record` хода (накопительный); в старых rollout — сумма `token_count.info.last_token_usage` между `task_started` хода и его концом, повторы того же `total_token_usage` (опросы) не считаются |
| | `hottell.turn.model` | `turn_context` хода |
| | `hottell.turn.duration_ms` | `task_complete.duration_ms` |
| оба | `hottell.enrich_status` | всегда, см. ниже |

`input_tokens` у OpenAI включает `cached_input_tokens`.

`hottell.enrich_status`: `found`; `partial` (Stop: потолок чтения достигнут до
начала хода — суммы токенов всё равно полные, `responses` — только
прочитанные); `not_found`; `no_transcript` (в payload нет пути или файла нет);
`disabled`; `error` (путь вне `~/.codex`, не `*.jsonl`, не обычный файл,
ошибка чтения).

**Приватность.** В атрибуты попадают только числа, короткие статусы и имя
модели — тексты команд, выводы, аргументы и сообщения не копируются никогда.
Читаются только обычные `*.jsonl` внутри `$HOME/.codex` (симлинки раскрываются
до проверки; `CODEX_HOME` в другом месте не поддержан).

**Чтение.** Файл никогда не читается целиком: хвост назад, куском 1 МБ, затем
по 4 МБ, не больше 64 МБ на файл; поиск останавливается, как только найдено
всё нужное батчу, и не уходит раньше начала хода события. События одного
rollout в батче — одно чтение. Замер на живых rollout (тёплый кэш, M-серия):
последний вызов или ход — 0,2–12 мс (1–5 МБ), худший случай до потолка
64 МБ — ~0,1 с.

**Гонка с записью.** Запись в rollout появляется чуть позже хука (`task_complete`
— через десятки мс после Stop). Не найденное у события моложе 5 с не шлётся:
спул-файл ждёт следующего круга дрейнера (250 мс, 500 мс, 1 с, … до 5 с от
события), остальные события уходят без задержки. Потом событие уходит как
есть — `not_found` (Stop без `task_complete` — без `duration_ms`). При сбое
отправки результат обогащения сохраняется в спул-файл: ретрай не перечитывает
rollout.

Выключение — `"enrich_transcript": false` в конфиге (`enrich_status=disabled`).

```sql
-- упавшие команды Codex
SELECT LogAttributes['tool_name'] tool, LogAttributes['hottell.exit_code'] code, count()
FROM otel.otel_logs
WHERE ServiceName = 'agent-hooks' AND LogAttributes['agent'] = 'codex'
  AND Body = 'agent.hook.PostToolUse' AND LogAttributes['hottell.tool_status'] = 'failed'
GROUP BY 1, 2 ORDER BY 3 DESC;

-- токены по ходам Codex
SELECT LogAttributes['session_id'] s, LogAttributes['turn_id'] t, LogAttributes['hottell.turn.model'] model,
       toUInt64OrZero(LogAttributes['hottell.turn.responses']) responses,
       toUInt64OrZero(LogAttributes['hottell.turn.input_tokens']) input,
       toUInt64OrZero(LogAttributes['hottell.turn.cached_input_tokens']) cached,
       toUInt64OrZero(LogAttributes['hottell.turn.output_tokens']) output
FROM otel.otel_logs
WHERE ServiceName = 'agent-hooks' AND LogAttributes['agent'] = 'codex' AND Body = 'agent.hook.Stop'
ORDER BY Timestamp DESC LIMIT 20;
```

## MCP-тулы

| Тул | Что делает |
|---|---|
| `ping` | проверка связи, версия |
| `sessions_list` | инвентарь транскриптов обоих агентов (`~/.claude/projects`, `~/.codex/sessions`): фильтры agent, since/until, project, min_size, субагенты; тела не читаются |
| `session_read` | одна сессия постранично в общей модели событий (`user_message`, `assistant_message`, `reasoning`, `tool_call`, `tool_result`, `token_usage`, `turn_start/end`, `compact`, …), фильтр по видам, обрезка текстов |
| `session_stats` | агрегаты по сессии или периоду: события по видам, инструменты и их ошибки, токены по моделям, ходы, длительность |
| `sessions_ship` | backfill истории в otel-lab (см. ниже) |
| `otel_status` | нативный OTel обоих агентов: вкл/выкл, endpoint, протокол, интервалы, prompts, resource-атрибуты; секреты — только признак `headers_set` |
| `project_scope` | `action: on\|off\|status`, `path` (по умолчанию каталог сессии), `prompts` — см. «Что шлётся и откуда» |
| `otel_configure` | `agent: claude\|codex\|both`, `enabled`, `endpoint` (база без `/v1/...`), `prompts`. Claude — только OTel-ключи блока `env` в `~/.claude/settings.json`; Codex — семейство `[otel]` в `~/.codex/config.toml` (только глобально: проектный `[otel]` Codex игнорирует). Токен заголовка — токен hottell. Действует на новые сессии |

Токены в общей модели: `input` — весь вход с кэшем, `cache_read`/`cache_write` —
его части. Claude пишет сообщение API несколькими записями (по блоку) с одним
`usage` — учитывается один раз; Codex — `token_usage_record` на ответ, в старых
rollout только `token_count` (берётся он).

**Backfill.** ClickHouse хранит 10 суток по `Timestamp`, поэтому `Timestamp` =
время загрузки, родное время события — атрибут `event.time`. Ресурс
`service.name=agent-backfill` (не смешивается с живым `agent-hooks`), у записей
`source=backfill`, `session.id`, `event.seq`, `event.kind`. Отправка прямо
батчами, мимо спула. Реестр `~/.local/state/hottell/shipped.json` хранит число
доставленных событий на сессию: повтор ничего не шлёт, дописанная сессия
догружается хвостом. `send_prompts=false` заменяет тексты реплик пользователя.

```sql
SELECT LogAttributes['agent'], LogAttributes['event.kind'], count()
FROM otel.otel_logs WHERE ServiceName = 'agent-backfill' GROUP BY 1, 2
```

## Установка из релиза

Релизы — https://github.com/nuanu-ai/ai-hottell/releases (тег `vX.Y.Z`,
бинари darwin arm64/amd64 и `SHA256SUMS`). Репозиторий приватный, поэтому
через `gh`:

```
V=v0.0.1; ARCH=$(uname -m | sed 's/x86_64/amd64/')
gh release download $V -R nuanu-ai/ai-hottell -p "hottell-darwin-$ARCH" -p SHA256SUMS
shasum -a 256 -c --ignore-missing SHA256SUMS
chmod +x hottell-darwin-$ARCH && xattr -d com.apple.quarantine hottell-darwin-$ARCH 2>/dev/null
./hottell-darwin-$ARCH install -token-file /путь/к/токену   # поставит себя в ~/.local/bin/hottell
hottell version
```

Обновление — то же самое: install идемпотентен, конфиг и токен не трогает.

## Сборка и выпуск

Версия приходит из git-тега: `hottell version` → `0.0.1 (<commit>)`; сборка
без ldflags — `dev+<sha>`. Выпуск — тег на `camp`:

```
git tag v0.0.2 && git push origin v0.0.2   # workflow .github/workflows/release.yml
```

Локально:

```
go build -ldflags "-X main.version=0.0.0-local" -o dist/hottell-darwin-arm64 .
go test ./...
```

## Раскатка на хост

Бинарь ставит себя сам; токен передаётся только явно (из чужих конфигов не выуживается):

```
hottell install -token-file /путь/к/токену    # либо ... | hottell install -token-stdin
```

Install кладёт бинарь в `~/.local/bin/hottell`, токен и конфиг в
`~/.config/hottell/`, вписывает свои хуки в `~/.claude/settings.json` и
`~/.codex/hooks.json` и шлёт смоук-событие до коллектора. Правки конфигов —
хирургические и без бэкапов: добавляются только записи hottell (опознаются по
слову `hottell` в команде), чужие хуки и ключи не трогаются никогда — конфиги
параллельно правят другие процессы, и «восстановить из бэкапа» затёрло бы их
работу. Install идемпотентен: повторный запуск доложит недостающее и не
задублирует ничего. Битый JSON — отказ, не затирание.

После install руками остаётся одно: в Codex выполнить `/hooks` и доверить
новые хуки (его защита, не обходим). Живые сессии обоих агентов подхватывают
хуки после `/hooks` внутри сессии или рестарта (`claude --resume` /
`codex resume` — контекст не теряется).

`hottell status` — стоит ли всё (бинарь, токен, хуки по агентам, глубина
буфера). `hottell uninstall` — обратная хирургия по текущему состоянию
конфигов: снимает только записи hottell, удаляет каталоги и бинарь.

Ручная проверка доставки: `echo '{"hook_event_name":"SessionStart","session_id":"smoke"}' |
hottell -agent codex`, через пару секунд событие видно в ClickHouse, спул пуст.

## Устройство под капотом

- Спул: файл = событие, имя `<unixnano>-<rand>.json`, запись через temp+rename —
  писатели и дрейнер не разделяют локов.
- Хуки Клода синхронные, поэтому дрейнер запускается отвязанным (`setsid`):
  когда агент добивает процесс-группу хука по таймауту, отправщик выживает.
- Кривой stdin, битый конфиг, недоступный спул — молча выход 0 (код 2 у хуков
  блокирует действие агента; см. `debug_log` в конфиге для разбирательств).

## Снятие (вместе с otel-lab, план 2026-10-04)

На каждом хосте: `hottell uninstall`.
