# Контракт hottell: модель настроек-запретов

Задача HT-52, эпик HT-51. Документ задаёт, что пользователь может запретить
и как бинарь `hottell` применяет запреты. По нему строятся JSON Schema
настроек (HT-56), хранение и API настроек (HT-60, HT-62, HT-69), страница
«Что отправлять» (HT-70, HT-73), ресурс настроек в MCP (HT-71), ключи
нативного OTel (HT-55) и сам бинарь.

Имена событий, полей и сигналов взяты из отчётов исследования HT-50:
«Claude Code 2.1.285 — хуки, OTel, транскрипты» и «Codex 0.159.0 — хуки,
OTel, транскрипты». Версии агентов, на которые опирается документ:
Claude Code 2.1.285, Codex CLI 0.159.0.

## Общие правила

- **По умолчанию собирается всё.** Настройки хранят только запреты.
  Пустые настройки означают «отправлять всё, что умеет бинарь».
  Исключение — флаг «догрузить историю», он по умолчанию выключен.
- **Настройки задаются на пользователя.** Одни и те же настройки действуют
  на всех машинах пользователя. Всё, что зависит от машины (например,
  домашний каталог в шаблонах папок), бинарь раскрывает на месте.
- **Запрет только сужает.** Ни один запрет не включает то, что выключено
  другим запретом. Единственное «разрешение» — исключение внутри запрета
  папки (см. «Папка проекта»).
- **Кто применяет.** Запреты на хуки и транскрипты применяет сам бинарь
  до отправки. Запреты на нативный OTel бинарь применяет, записывая
  настройки OTel в конфиги агентов; данные нативного OTel агенты
  отправляют сами, бинарь их не видит и не фильтрует.

Примеры значений ниже — фрагменты документа настроек. Окончательную форму
документа фиксирует JSON Schema [`settings.schema.json`](settings.schema.json)
(HT-56), см. «Схема и примеры».

## Виды запретов

### 1. Агент

Выключает агента целиком: бинарь не ставит ему хуки, не читает его
транскрипты и выключает его нативный OTel.

Значения агента: `claude` (Claude Code), `codex` (Codex CLI).

```json
{ "agents": { "codex": { "enabled": false } } }
```

### 2. Источник

Выключает один источник данных у одного агента.

| Значение | Источник | Что это у Claude Code | Что это у Codex |
|---|---|---|---|
| `hooks` | события хуков | события, которые бинарь получает как command-хук | то же |
| `transcripts` | транскрипты | `~/.claude/projects/<project>/<session-id>.jsonl` и транскрипты субагентов | rollout `~/.codex/sessions/…/rollout-*.jsonl` (и `.jsonl.zst`) |
| `native_metrics` | нативные метрики | `OTEL_METRICS_EXPORTER` | `[otel] metrics_exporter` |
| `native_logs` | нативные события (логи) | `OTEL_LOGS_EXPORTER` | `[otel] exporter` |
| `native_traces` | нативные трейсы | `OTEL_TRACES_EXPORTER` (бета-трейсинг) | `[otel] trace_exporter` |

Выключенный нативный сигнал бинарь выключает явно, а не оставляет без
настройки. Для Codex это обязательно: без явного `metrics_exporter` метрики
по умолчанию уходят в Statsig (OpenAI).

```json
{ "agents": { "claude": { "sources": { "transcripts": false, "native_traces": false } } } }
```

### 3. Событие хука

Бинарь не отправляет события с этим `hook_event_name`. Для каждого агента
свой список. Бинарь подписывается на все события из списка агента,
кроме запрещённых.

**Claude Code** (33 события):
`SessionStart`, `Setup`, `InstructionsLoaded`, `UserPromptSubmit`,
`UserPromptExpansion`, `MessageDisplay`, `PreToolUse`, `PermissionRequest`,
`PermissionDenied`, `PostToolUse`, `PostToolUseFailure`, `PostToolBatch`,
`Notification`, `SubagentStart`, `SubagentStop`, `TaskCreated`,
`TaskCompleted`, `Stop`, `StopFailure`, `TeammateIdle`, `ConfigChange`,
`CwdChanged`, `DirectoryAdded`, `FileChanged`, `WorktreeCreate`,
`WorktreeRemove`, `PreCompact`, `PostCompact`, `PreModelSwitch`,
`PostModelSwitch`, `Elicitation`, `ElicitationResult`, `SessionEnd`.

**Codex** (12 событий):
`SessionStart`, `SessionEnd`, `UserPromptSubmit`, `PreToolUse`,
`PermissionRequest`, `PostToolUse`, `PreCompact`, `PostCompact`,
`SubagentStart`, `SubagentStop`, `Stop`, `Interrupt`.

Имя события вне списка своего агента — ошибка настроек, сервер её
не сохраняет.

```json
{ "agents": { "claude": { "hook_events": { "denied": ["MessageDisplay", "FileChanged"] } } } }
```

### 4. Поле события хука

Бинарь вырезает из события поле верхнего уровня с этим именем, остальное
событие уходит. Запрет действует на все события агента, в которых есть
такое поле. Список свой у каждого агента.

Поля, которые несут содержимое и которые страница предлагает в первую
очередь:

| Агент | Поле | В каких событиях |
|---|---|---|
| оба | `prompt` | `UserPromptSubmit`; у Claude ещё `UserPromptExpansion` |
| оба | `tool_input` | `PreToolUse`, `PermissionRequest`, `PostToolUse`; у Claude ещё `PermissionDenied`, `PostToolUseFailure` |
| оба | `tool_response` | `PostToolUse` |
| оба | `last_assistant_message` | `Stop`, `SubagentStop`; у Claude ещё `StopFailure` |
| claude | `compact_summary` | `PostCompact` |
| claude | `custom_instructions` | `PreCompact` |
| claude | `error` | `PostToolUseFailure`, `StopFailure` |
| claude | `tool_calls` | `PostToolBatch` (внутри — `tool_input` и `tool_response` пачки) |
| claude | `delta` | `MessageDisplay` |
| claude | `message` | `Notification`, `Elicitation` |
| claude | `command_args` | `UserPromptExpansion` |
| claude | `content` | `ElicitationResult` |
| claude | `background_tasks`, `session_crons` | `Stop`, `SubagentStop` |

Допускается любое другое имя поля верхнего уровня. Нельзя запретить
служебные поля, без которых запись не связать с сессией: `session_id`,
`hook_event_name`, `cwd`. Вложенные поля (например, часть `tool_input`)
этим запретом не вырезаются.

```json
{ "agents": { "claude": { "hook_fields": { "denied": ["tool_response", "compact_summary"] } } } }
```

### 5. Папка проекта

Запрещает всё, что относится к сессиям, запущенным в этих папках.
Проверяется `cwd` сессии:

- для события хука — поле `cwd` события;
- для транскрипта — `cwd` сессии из самого файла: у Claude — поле `cwd`
  первой записи, где оно есть; у Codex — `session_meta.payload.cwd`.
  Транскрипт отправляется или не отправляется целиком.

Шаблон — путь, который сравнивается с `cwd` целиком:

- начинается с `/` или с `~/`; `~` бинарь заменяет домашним каталогом
  текущей машины;
- `**` — любое число сегментов пути, в том числе ноль;
- остальные символы сравниваются буквально, с учётом регистра;
  завершающий `/` в `cwd` и в шаблоне не учитывается;
- символические ссылки не раскрываются: сравнивается `cwd` так,
  как его сообщил агент.

Два списка: `denied` — запретить, `allowed` — разрешить внутри
запрещённого. Папка запрещена, если `cwd` подходит хотя бы под один шаблон
из `denied` и ни под один из `allowed`. Шаблон из `allowed`, который
не лежит внутри какого-либо запрета, ничего не меняет.

Запрет папки общий для обоих агентов.

```json
{
  "folders": {
    "denied": ["~/work/**", "/Volumes/secret/**"],
    "allowed": ["~/work/oss/**"]
  }
}
```

Здесь `~/work/client-a` запрещена, `~/work/oss/tool` разрешена,
`~/projects/x` разрешена.

### 6. Категория содержимого нативного OTel

Запрещает содержимое внутри нативных сигналов агента, не выключая сам
сигнал: метрики, счётчики, длины и размеры продолжают уходить. Список
свой у каждого агента.

| Значение | Категория | Claude Code | Codex |
|---|---|---|---|
| `prompts` | промпты | `OTEL_LOG_USER_PROMPTS` | `[otel] log_user_prompt` |
| `assistant_responses` | ответы ассистента | `OTEL_LOG_ASSISTANT_RESPONSES` | `[otel] log_agent_responses` |
| `tool_details` | детали инструментов | `OTEL_LOG_TOOL_DETAILS` | отдельного ключа нет |
| `tool_content` | содержимое инструментов | `OTEL_LOG_TOOL_CONTENT` | только урезание `[otel] tool_result.max_bytes` |
| `raw_api_bodies` | сырые тела API | `OTEL_LOG_RAW_API_BODIES` | у Codex такого содержимого нет |

Незапрещённые категории бинарь включает, а лимиты содержимого поднимает
до максимума (решение владельца по HT-50). Точные ключи и значения —
в HT-55.

```json
{ "agents": { "claude": { "native_content": { "denied": ["raw_api_bodies", "tool_content"] } } } }
```

### 7. Флаг «догрузить историю»

Когда флаг включён, бинарь один раз отправляет транскрипты, которые
существовали до его установки, а не только дописанное после. По умолчанию
выключен. К истории применяются запреты агента, источника `transcripts`
и папки: при выключенных транскриптах история не отправляется.

```json
{ "backfill_history": true }
```

## Поле версии

`version` — целое число, которое ведёт сервер:

- у пользователя, который ни разу не сохранял настройки, `version` равно
  `0`, а настройки пустые (всё разрешено, история не догружается);
- каждое успешное сохранение увеличивает `version` ровно на 1; сохранение
  без изменений версию не меняет;
- клиент `version` не присылает как новое значение, сервер его
  не принимает от клиента;
- бинарь помнит версию последних применённых настроек и применяет
  полученные настройки, только если их `version` больше.

```json
{ "version": 7 }
```

## Таблица применимости

Таблицу показывает страница «Что отправлять» рядом с каждым запретом.

Обозначения:

- **✓** — запрет действует полностью;
- **⚠** — действует с ограничением, см. сноску;
- **✗** — не действует, хотя данные такого рода в источнике есть;
- **—** — не относится: в источнике нет того, что запрещает этот вид.

Столбцы: `hooks` — события хуков, `tr` — транскрипты, `n.met` — нативные
метрики, `n.log` — нативные события (логи), `n.tr` — нативные трейсы.

| Вид запрета | Claude `hooks` | Claude `tr` | Claude `n.met` | Claude `n.log` | Claude `n.tr` | Codex `hooks` | Codex `tr` | Codex `n.met` | Codex `n.log` | Codex `n.tr` |
|---|---|---|---|---|---|---|---|---|---|---|
| Агент | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| Источник `hooks` | ✓ | — | — | — | — | ✓ | — | — | — | — |
| Источник `transcripts` | — | ✓ | — | — | — | — | ✓ | — | — | — |
| Источник `native_metrics` | — | — | ✓ | — | — | — | — | ✓ | — | — |
| Источник `native_logs` | — | — | — | ✓ | — | — | — | — | ✓ | — |
| Источник `native_traces` | — | — | — | — | ✓ | — | — | — | — | ✓ |
| Событие хука | ✓ | — | — | — | — | ✓ | — | — | — | — |
| Поле события хука | ✓ | — | — | — | — | ✓ | — | — | — | — |
| Папка проекта | ✓ | ✓ | ✗¹ | ✗¹ | ✗¹ | ✓ | ✓ | ✗² | ✗² | ✗² |
| Категория `prompts` | — | — | — | ✓ | ✓ | — | — | — | ✓ | — |
| Категория `assistant_responses` | — | — | — | ✓ | — | — | — | — | ✓ | — |
| Категория `tool_details` | — | — | ⚠³ | ✓ | ✓ | — | — | — | ✗⁴ | — |
| Категория `tool_content` | — | — | — | — | ✓ | — | — | — | ⚠⁵ | — |
| Категория `raw_api_bodies` | — | — | — | ✓ | — | — | — | — | — | — |
| Флаг «догрузить историю» | — | ✓ | — | — | — | — | ✓ | — | — | — |

Сноски:

1. **На нативный OTel Claude запрет папки не действует.** Claude Code
   2.1.285 не применяет `OTEL_*` из `.claude/settings.json`
   и `.claude/settings.local.json` проекта даже при старте новой сессии
   (эксперимент HT-78, [apply-timing.md](apply-timing.md)). Бинарь ничего
   не пишет в `<папка>/.claude/`; сессии Claude в запрещённой папке
   отправляют нативный OTel по пользовательским настройкам. Чтобы ничего
   не уходило, нужен запрет источника или агента
   ([native-otel.md](native-otel.md), раздел 4).
2. **На нативный OTel Codex запрет папки не действует.** Codex полностью
   игнорирует проектный `[otel]`. Сессии Codex в запрещённой папке
   продолжают отправлять нативный OTel по общим настройкам пользователя.
   Чтобы ничего не уходило, нужен запрет источника или агента.
3. Метрики Claude не несут содержимого, но реальные имена агентов,
   скиллов, плагинов и MCP-серверов в их атрибутах без
   `OTEL_LOG_TOOL_DETAILS` заменяются обобщёнными. Для метрик
   это не проверено экспериментом.
4. У Codex нет ключа для деталей инструментов: `codex.tool_result.arguments`
   уходит в логи целиком всегда. Скрыть их можно только запретом
   источника `native_logs`.
5. Вывод инструмента в `codex.tool_result.output` урезается ключом
   `tool_result.max_bytes`, но не убирается гарантированно.
   Межагентные сообщения `codex.agent_communication.content` ни одна
   категория не скрывает; их убирает только запрет `native_logs`.

## Порядок применения

Бинарь проверяет запреты от крупного к мелкому и останавливается на первом
сработавшем. Запрет всегда побеждает; единственное «разрешение» —
`folders.allowed`, и оно снимает только запрет папки.

1. **Агент.** Агент выключен — от него ничего не уходит, дальше
   не проверяется.
2. **Источник.** Источник агента выключен — из него ничего не уходит.
3. **Папка.** `cwd` сессии подходит под `folders.denied` и не подходит
   под `folders.allowed` — для этой сессии не уходят хуки и транскрипт;
   на нативный OTel запрет папки не действует (сноски 1 и 2). `folders.allowed`
   не отменяет запреты шагов 1–2 и 4–6.
4. **Событие хука.** Событие запрещено — оно не уходит.
5. **Поле события хука.** Запрещённые поля вырезаются, остальное событие
   уходит.
6. **Категория содержимого нативного OTel.** Применяется агентом по его
   конфигу к сигналам, которые остались включены после шагов 1–3.

Флаг «догрузить историю» — не запрет, а разрешение одноразовой отправки
старых транскриптов. Каждый старый транскрипт проходит шаги 1–3 так же,
как новый.

Пример пересечения: у Claude запрещено поле `prompt`, папка `~/work/**`
запрещена, `~/work/oss/**` разрешена. Сессия в `~/work/oss/tool` проходит
шаг 3, и её `UserPromptSubmit` уходит без поля `prompt` (шаг 5). Сессия
в `~/work/client-a` останавливается на шаге 3: её хуки не уходят совсем.

## Примеры документа настроек

Ничего не запрещено:

```json
{
  "version": 0,
  "agents": {
    "claude": {
      "enabled": true,
      "sources": { "hooks": true, "transcripts": true, "native_metrics": true, "native_logs": true, "native_traces": true },
      "hook_events": { "denied": [] },
      "hook_fields": { "denied": [] },
      "native_content": { "denied": [] }
    },
    "codex": {
      "enabled": true,
      "sources": { "hooks": true, "transcripts": true, "native_metrics": true, "native_logs": true, "native_traces": true },
      "hook_events": { "denied": [] },
      "hook_fields": { "denied": [] },
      "native_content": { "denied": [] }
    }
  },
  "folders": { "denied": [], "allowed": [] },
  "backfill_history": false
}
```

По одному запрету каждого вида:

```json
{
  "version": 12,
  "agents": {
    "claude": {
      "enabled": true,
      "sources": { "hooks": true, "transcripts": true, "native_metrics": true, "native_logs": true, "native_traces": false },
      "hook_events": { "denied": ["MessageDisplay"] },
      "hook_fields": { "denied": ["tool_response"] },
      "native_content": { "denied": ["raw_api_bodies"] }
    },
    "codex": {
      "enabled": false,
      "sources": { "hooks": true, "transcripts": true, "native_metrics": true, "native_logs": true, "native_traces": true },
      "hook_events": { "denied": [] },
      "hook_fields": { "denied": [] },
      "native_content": { "denied": [] }
    }
  },
  "folders": { "denied": ["~/work/**"], "allowed": ["~/work/oss/**"] },
  "backfill_history": true
}
```

## Схема и примеры

Форму документа настроек задаёт JSON Schema draft 2020-12
[`settings.schema.json`](settings.schema.json). Все объекты закрыты
(`additionalProperties: false`): неизвестный вид запрета, агент, источник
или событие хука вне списка своего агента делают документ недействительным.
Отсутствующее поле означает значение по умолчанию: всё разрешено, история
не догружается, `version` равно `0`. Сервер отдаёт документ в полной форме.

Примеры в [`examples/`](examples/):

- [`settings-empty.json`](examples/settings-empty.json) — ничего
  не запрещено;
- [`settings-full.json`](examples/settings-full.json) — по одному запрету
  каждого вида;
- [`invalid-unknown-kind.json`](examples/invalid-unknown-kind.json) —
  неизвестный вид запрета и неизвестное событие хука, схема его отвергает.

Проверка из каталога `docs/specs/hottell-contract/`:

```sh
npx ajv-cli validate --spec=draft2020 -s settings.schema.json \
  -d examples/settings-empty.json -d examples/settings-full.json   # valid
npx ajv-cli validate --spec=draft2020 --all-errors -s settings.schema.json \
  -d examples/invalid-unknown-kind.json                             # invalid
```
