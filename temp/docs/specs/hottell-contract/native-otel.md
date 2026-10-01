# Контракт hottell: ключи нативного OTel Claude Code и Codex

Задача HT-55, эпик HT-51. Документ задаёт, какие ключи нативного
OpenTelemetry бинарь `hottell` пишет в конфиги Claude Code и Codex
и как запреты из [модели настроек](settings.md) (HT-52) превращаются
в значения этих ключей. По нему работают бинарь и страница «Что
отправлять» (HT-70, HT-73).

Версии агентов: Claude Code 2.1.285, Codex CLI 0.159.0.

## Источники

Каждый ключ ссылается на отчёт исследования HT-50, приложенный к карточке
документом:

- **[CC §N]** — «Исследование: Claude Code 2.1.285 — хуки, OTel,
  транскрипты», раздел N;
- **[CX §N]** — «Исследование: Codex 0.159.0 — хуки, OTel, транскрипты»,
  раздел N.

Где в HT-50 значения нет, это указано явно, а источник назван рядом:

- **[bin CC]** — разбор переменных окружения в бинаре Claude Code 2.1.285;
- **[HT-78]** — эксперимент с живыми сессиями Claude Code 2.1.285
  и Codex 0.159.0, [apply-timing.md](apply-timing.md);
- **[run CX]** — загрузка конфига настоящим Codex 0.159.0 во временном
  `CODEX_HOME` (`codex features list`: код 0 — конфиг принят,
  код 1 — ошибка разбора).

## Обозначения

Все значения в примерах синтетические.

- `<SERVICE_URL>` — базовый адрес сервиса без завершающего `/`, тот же,
  на котором работает `/mcp` (HT-51). Пример:
  `https://telemetry.example.test`.
- `<COLLECTOR_TOKEN>` — токен коллектора пользователя, который бинарь
  получает через MCP (HT-54, HT-67). Пример: `ht_col_0123456789abcdef`.
- `<HOTTELL_VERSION>` — версия бинаря, например `0.1.0`.
- `<HOSTNAME>` — короткое имя машины, например `mac-1`.

## Общие правила

- **Всё включено по умолчанию.** Без запретов бинарь включает все три
  сигнала и всё содержимое, а лимиты содержимого поднимает до максимума
  (решение владельца по HT-50).
- **Выключенное пишется явно.** Выключенный сигнал или категорию бинарь
  записывает выключающим значением, а не удаляет ключ ([settings.md](settings.md),
  «Источник»). Для Codex это обязательно: без `metrics_exporter` метрики
  уходят в Statsig (OpenAI) [CX §2.1, §5 п. 8].
- **Бинарь трогает только свои ключи.** Он меняет ключи из этого
  документа и не трогает остальное содержимое файлов.
- **Имена ключей пишутся точно.** Codex молча игнорирует незнакомый ключ
  внутри `[otel]`: `tool_result.max_bytez = 5` загружается без ошибки
  [run CX]. Опечатка оставляет значение по умолчанию.
- **Заголовок с токеном.** Токен коллектора передаётся в заголовке
  `Authorization: Bearer <COLLECTOR_TOKEN>`. Точное имя заголовка фиксирует
  контракт приёма HT-57; если оно изменится, меняются только значения
  заголовка ниже. Токен не должен содержать `,` и `=`: у Claude
  заголовки задаются строкой `ключ=значение` через запятую.
- **Потолок размера задаёт приём.** Лимиты содержимого агентов подняты
  до максимума, поэтому размер записи на деле ограничивает приём
  сервиса: по рекомендации [payload-sizes.md](payload-sizes.md)
  для HT-57 — 8 МиБ на запись и 16 МиБ на запрос. Окончательные лимиты
  фиксирует HT-57.

## 1. Claude Code: блок `env` в `~/.claude/settings.json`

Claude Code берёт настройки OTel из переменных окружения. Значение
в `env` пользовательских настроек перекрывает переменную, заданную
в shell [CC §2.2]. Поэтому бинарь пишет все ключи в `env` файла
`~/.claude/settings.json`. Из проектных настроек `OTEL_*` не действуют
вовсе, ни включающие, ни выключающие значения [HT-78].

| Ключ | Значение, когда ничего не запрещено | Что делает | Источник |
|---|---|---|---|
| `CLAUDE_CODE_ENABLE_TELEMETRY` | `1` | главный включатель телеметрии | [CC §2.1] |
| `CLAUDE_CODE_ENHANCED_TELEMETRY_BETA` | `1` | включает трейсы (бета) | [CC §2.1, §2.6] |
| `OTEL_METRICS_EXPORTER` | `otlp` | экспортёр метрик | [CC §2.1] |
| `OTEL_LOGS_EXPORTER` | `otlp` | экспортёр событий (логов) | [CC §2.1] |
| `OTEL_TRACES_EXPORTER` | `otlp` | экспортёр трейсов | [CC §2.1, §2.6] |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `<SERVICE_URL>` | общий адрес для всех сигналов; путь `/v1/logs`, `/v1/metrics`, `/v1/traces` к общему адресу дописывает SDK по спецификации OTLP. Данные Claude в HT-50 пришли именно с общими `ENDPOINT` и `PROTOCOL` | [CC §2.1] |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | `http/protobuf` | протокол; по умолчанию его нет, поэтому задаётся всегда | [CC §2.1] |
| `OTEL_EXPORTER_OTLP_HEADERS` | `Authorization=Bearer <COLLECTOR_TOKEN>` | заголовок с токеном коллектора для всех сигналов | [CC §2.1] |
| `OTEL_METRIC_EXPORT_INTERVAL` | `60000` | интервал отправки метрик, мс | [CC §2.1] |
| `OTEL_LOGS_EXPORT_INTERVAL` | `5000` | интервал отправки событий, мс | [CC §2.1] |
| `OTEL_TRACES_EXPORT_INTERVAL` | `5000` | интервал отправки трейсов, мс | [CC §2.1] |
| `OTEL_LOG_USER_PROMPTS` | `1` | текст промпта | [CC §2.1, §2.7] |
| `OTEL_LOG_ASSISTANT_RESPONSES` | `1` | текст ответа ассистента | [CC §2.1, §2.7] |
| `OTEL_LOG_TOOL_DETAILS` | `1` | параметры и вход инструментов, настоящие имена агентов, скиллов, плагинов и MCP | [CC §2.1, §2.7] |
| `OTEL_LOG_TOOL_CONTENT` | `1` | содержимое инструментов (span-событие `tool.output`, только в трейсах) | [CC §2.1, §2.6] |
| `OTEL_LOG_RAW_API_BODIES` | `1` | тела запросов и ответов API прямо в событии | [CC §2.1, §2.7] |
| `CLAUDE_CODE_OTEL_CONTENT_MAX_LENGTH` | `9007199254740991` | лимит длины содержимого в атрибутах | [CC §2.1]; максимум — [bin CC] |
| `OTEL_ATTRIBUTE_VALUE_LENGTH_LIMIT` | `9007199254740991` | общий лимит длины атрибута в SDK | [CC §2.1]; максимум — [bin CC] |
| `OTEL_LOGRECORD_ATTRIBUTE_VALUE_LENGTH_LIMIT` | `9007199254740991` | лимит длины атрибута события | [CC §2.1] — правило «лимит урезается лимитом атрибутов SDK»; сам ключ HT-50 не называет, он из [bin CC] |
| `OTEL_SPAN_ATTRIBUTE_VALUE_LENGTH_LIMIT` | `9007199254740991` | лимит длины атрибута спана | [CC §2.1] — правило «лимит урезается лимитом атрибутов SDK»; сам ключ HT-50 не называет, он из [bin CC] |
| `OTEL_RESOURCE_ATTRIBUTES` | `deployment.environment=hottell,host.name=<HOSTNAME>,hottell.version=<HOTTELL_VERSION>` | ресурсные атрибуты; по умолчанию попадают и в точки метрик | [CC §2.1, §2.3] |

Пояснения:

- **`OTEL_LOG_RAW_API_BODIES=1`, а не `file:<dir>`.** Режим `file:`
  пишет тела на диск, а в событие кладёт только ссылку `body_ref`
  [CC §2.1]. На сервер содержимое уходит только при `1`. Extended thinking
  Claude Code редактирует всегда [CC §2.7].
- **`OTEL_LOG_ASSISTANT_RESPONSES` пишется явно.** Без него значение берётся
  из `OTEL_LOG_USER_PROMPTS` [CC §2.1], и запрет промптов выключил бы
  и ответы.
- **Интервалы** — значения Claude Code по умолчанию, записанные явно,
  чтобы их не переопределила переменная shell [CC §2.1, §2.2].
- **Лимит содержимого.**
  - HT-50 даёт ключ, значение по умолчанию 61 440 UTF-16-единиц и правило:
    лимит урезается до `OTEL_ATTRIBUTE_VALUE_LENGTH_LIMIT`, если тот
    меньше [CC §2.1]. Наибольшего допустимого значения в HT-50 нет.
  - [bin CC]: `CLAUDE_CODE_OTEL_CONTENT_MAX_LENGTH` разбирается как целое
    из одних цифр, не меньше 1, без верхней границы. Итоговый лимит — это
    наименьшее из четырёх значений: `CLAUDE_CODE_OTEL_CONTENT_MAX_LENGTH`
    (без него 61 440), `OTEL_ATTRIBUTE_VALUE_LENGTH_LIMIT`,
    `OTEL_LOGRECORD_ATTRIBUTE_VALUE_LENGTH_LIMIT`,
    `OTEL_SPAN_ATTRIBUTE_VALUE_LENGTH_LIMIT` (без них — без ограничения).
    По этому лимиту режутся промпт, ответ ассистента, `tool.output`
    и тела API в событии.
  - Бинарь пишет во все четыре ключа `9007199254740991`
    (`Number.MAX_SAFE_INTEGER`): это наибольшее целое, которое Claude Code
    читает без потери точности. Три ключа SDK пишутся явно, чтобы
    переменная shell не опустила лимит.
  - Лимиты, которые этими ключами не поднимаются: `tool_input` в событии
    `tool_result` — 512 символов на значение и около 4K всего;
    `managed_settings.settings` — 8 KB [CC §2.5, §2.7].
- **`OTEL_RESOURCE_ATTRIBUTES`.**
  - `deployment.environment=hottell` отмечает данные, настроенные бинарём.
  - `host.name` Claude Code сам не шлёт; в HT-50 он пришёл через этот ключ
    [CC §2.3].
  - `hottell.version` — версия бинаря.
  - Значения не должны содержать `,` и `=`.
  - `hottell.user.id` бинарь не пишет: его проставляет только сервер
    (HT-51).

Пример блока, когда ничего не запрещено:

```json
{
  "env": {
    "CLAUDE_CODE_ENABLE_TELEMETRY": "1",
    "CLAUDE_CODE_ENHANCED_TELEMETRY_BETA": "1",
    "OTEL_METRICS_EXPORTER": "otlp",
    "OTEL_LOGS_EXPORTER": "otlp",
    "OTEL_TRACES_EXPORTER": "otlp",
    "OTEL_EXPORTER_OTLP_ENDPOINT": "https://telemetry.example.test",
    "OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf",
    "OTEL_EXPORTER_OTLP_HEADERS": "Authorization=Bearer ht_col_0123456789abcdef",
    "OTEL_METRIC_EXPORT_INTERVAL": "60000",
    "OTEL_LOGS_EXPORT_INTERVAL": "5000",
    "OTEL_TRACES_EXPORT_INTERVAL": "5000",
    "OTEL_LOG_USER_PROMPTS": "1",
    "OTEL_LOG_ASSISTANT_RESPONSES": "1",
    "OTEL_LOG_TOOL_DETAILS": "1",
    "OTEL_LOG_TOOL_CONTENT": "1",
    "OTEL_LOG_RAW_API_BODIES": "1",
    "CLAUDE_CODE_OTEL_CONTENT_MAX_LENGTH": "9007199254740991",
    "OTEL_ATTRIBUTE_VALUE_LENGTH_LIMIT": "9007199254740991",
    "OTEL_LOGRECORD_ATTRIBUTE_VALUE_LENGTH_LIMIT": "9007199254740991",
    "OTEL_SPAN_ATTRIBUTE_VALUE_LENGTH_LIMIT": "9007199254740991",
    "OTEL_RESOURCE_ATTRIBUTES": "deployment.environment=hottell,host.name=mac-1,hottell.version=0.1.0"
  }
}
```

## 2. Codex: семейство `[otel]` в `~/.codex/config.toml`

Codex читает `[otel]` только из пользовательского, системного
и managed-слоёв и из `-c`; проектный `[otel]` он игнорирует [CX §2.1].
Бинарь пишет в `~/.codex/config.toml`.

| Ключ | Значение, когда ничего не запрещено | Что делает | Источник |
|---|---|---|---|
| `exporter` | `otlp-http`: `endpoint = "<SERVICE_URL>/v1/logs"`, `protocol = "binary"`, заголовок | экспортёр событий (логов) | [CX §2.1] |
| `trace_exporter` | `otlp-http`: `endpoint = "<SERVICE_URL>/v1/traces"`, `protocol = "binary"`, заголовок | экспортёр трейсов; не наследует `exporter` | [CX §2.1] |
| `metrics_exporter` | `otlp-http`: `endpoint = "<SERVICE_URL>/v1/metrics"`, `protocol = "binary"`, заголовок | экспортёр метрик; задаётся всегда, иначе метрики уходят в Statsig | [CX §2.1, §5 п. 8] |
| `headers` внутри каждого `otlp-http` | `{ "Authorization" = "Bearer <COLLECTOR_TOKEN>" }` | заголовок с токеном коллектора | [CX §2.1] |
| `log_user_prompt` | `true` | текст промпта в `codex.user_prompt` | [CX §2.1, §2.2] |
| `log_agent_responses` | `true` | финальный ответ в `codex.agent_response` | [CX §2.1, §2.2] |
| `tool_result.max_bytes` | `9223372036854775807` | длина вывода инструмента в `codex.tool_result.output` | [CX §2.1, §2.2]; максимум — [run CX] |
| `environment` | `"hottell"` | ресурсный атрибут `env` | [CX §2.1] |

Пояснения:

- **Полный путь сигнала в `endpoint`.** У `otlp-http` Codex не дописывает
  путь, поэтому у каждого экспортёра свой полный адрес [CX §2.1].
- **`protocol = "binary"`** — OTLP/protobuf, тот же формат, что у Claude
  (`http/protobuf`) [CX §2.1].
- **Заголовок пишется открытым значением.** Подстановка `${VAR}`
  в `headers` в исходниках не подтверждена [CX §2.1, §5 п. 5].
- **`tool_result.max_bytes`.**
  - HT-50 даёт тип `usize` и значение по умолчанию 2048 [CX §2.1].
    Наибольшего значения в HT-50 нет.
  - [run CX]: `9223372036854775807` (наибольшее целое TOML) загружается,
    `9223372036854775808` — ошибка `u64 value was too large`,
    `-1` — ошибка `expected usize`. Бинарь пишет `9223372036854775807`.
  - Аргументы инструмента (`codex.tool_result.arguments`) Codex отправляет
    целиком всегда [CX §2.2].
  - Ответ агента (`codex.agent_response.response`) Codex режет
    на 65 536 байтах. Этот лимит ключом не поднимается [CX §2.2].
- **`environment`** попадает в ресурсный атрибут `env`, а не
  в `deployment.environment` [CX §2.1]. Значение `hottell` отмечает данные,
  настроенные бинарём.
- **`log_guardian_assessments`** бинарь не пишет: к категориям запретов
  HT-52 этот ключ не относится.
- **Интервал метрик** в `[otel]` не задаётся. Метрики отправляются
  с интервалом SDK по умолчанию, вероятно 60 с [CX §2.1].

Пример, когда ничего не запрещено (проверен загрузкой в Codex 0.159.0,
[run CX]):

```toml
[otel]
environment = "hottell"
log_user_prompt = true
log_agent_responses = true
tool_result.max_bytes = 9223372036854775807

[otel.exporter.otlp-http]
endpoint = "https://telemetry.example.test/v1/logs"
protocol = "binary"
headers = { "Authorization" = "Bearer ht_col_0123456789abcdef" }

[otel.trace_exporter.otlp-http]
endpoint = "https://telemetry.example.test/v1/traces"
protocol = "binary"
headers = { "Authorization" = "Bearer ht_col_0123456789abcdef" }

[otel.metrics_exporter.otlp-http]
endpoint = "https://telemetry.example.test/v1/metrics"
protocol = "binary"
headers = { "Authorization" = "Bearer ht_col_0123456789abcdef" }
```

Выключенный экспортёр — строка `"none"` вместо таблицы `otlp-http`
[CX §2.1]. В TOML у ключа одна форма, поэтому бинарь заменяет таблицу
строкой и обратно:

```toml
[otel]
metrics_exporter = "none"
```

## 3. Таблица соответствия запретам

Какие ключи бинарь меняет при каждом запрете из [settings.md](settings.md).
Остальные ключи остаются со значениями из разделов 1 и 2.

Обозначения, как в таблице применимости HT-52: **—** — вид запрета
к нативному OTel не относится, ключи не меняются; **✗** — не действует,
ключа нет; **⚠** — действует с ограничением.

| Вид запрета (HT-52) | Claude Code: ключ = значение | Codex: ключ = значение | Источник |
|---|---|---|---|
| Агент выключен (`enabled: false`) | `CLAUDE_CODE_ENABLE_TELEMETRY=0`, `OTEL_METRICS_EXPORTER=none`, `OTEL_LOGS_EXPORTER=none`, `OTEL_TRACES_EXPORTER=none`; ключ `OTEL_EXPORTER_OTLP_HEADERS` удаляется | `exporter = "none"`, `trace_exporter = "none"`, `metrics_exporter = "none"`, `log_user_prompt = false`, `log_agent_responses = false` | [CC §2.1]; [CX §2.1] |
| Источник `hooks` | — | — | settings.md |
| Источник `transcripts` | — | — | settings.md |
| Источник `native_metrics` | `OTEL_METRICS_EXPORTER=none` | `metrics_exporter = "none"` | [CC §2.1]; [CX §2.1] |
| Источник `native_logs` | `OTEL_LOGS_EXPORTER=none` | `exporter = "none"` | [CC §2.1]; [CX §2.1] |
| Источник `native_traces` | `OTEL_TRACES_EXPORTER=none` | `trace_exporter = "none"` | [CC §2.1]; [CX §2.1] |
| Все три нативных источника выключены | ещё `CLAUDE_CODE_ENABLE_TELEMETRY=0`; `OTEL_EXPORTER_OTLP_HEADERS` удаляется | как у выключенного агента | [CC §2.1]; [CX §2.1] |
| Событие хука | — | — | settings.md |
| Поле события хука | — | — | settings.md |
| Папка проекта | ✗ проектные `OTEL_*` игнорируются, бинарь ничего не пишет (раздел 4) | ✗ проектный `[otel]` игнорируется, бинарь ничего не пишет | [HT-78]; [CX §2.1] |
| Категория `prompts` | `OTEL_LOG_USER_PROMPTS=0` | `log_user_prompt = false` | [CC §2.1]; [CX §2.1, §2.2] |
| Категория `assistant_responses` | `OTEL_LOG_ASSISTANT_RESPONSES=0` | `log_agent_responses = false` | [CC §2.1]; [CX §2.1, §2.2] |
| Категория `tool_details` | `OTEL_LOG_TOOL_DETAILS=0` | ✗ ключа нет: `codex.tool_result.arguments` уходит всегда, скрыть можно только запретом `native_logs` | [CC §2.1]; [CX §2.2, §5 п. 9] |
| Категория `tool_content` | `OTEL_LOG_TOOL_CONTENT=0` | ⚠ `tool_result.max_bytes = 0` | [CC §2.1]; [CX §2.1, §2.2] |
| Категория `raw_api_bodies` | `OTEL_LOG_RAW_API_BODIES=0` | — (у Codex такого содержимого нет) | [CC §2.1]; [CX §2.2] |
| Флаг «догрузить историю» | — | — | settings.md |

Уточнения к таблице:

- **Запреты складываются.** Каждый сработавший запрет меняет свои ключи;
  ключ, который меняют два запрета, получает выключающее значение.
  Например, при выключенном агенте запрет категории ничего не добавляет.
- **Лимиты содержимого не меняются при запретах.** `CLAUDE_CODE_OTEL_CONTENT_MAX_LENGTH`
  и три лимита SDK остаются максимальными: они действуют на всё
  незапрещённое содержимое сразу.
- **Выключенный агент или все три источника.** Бинарь удаляет у Claude
  ключ с токеном, чтобы тот не лежал в конфиге без нужды. У Codex таблицы
  `otlp-http` вместе с заголовком заменяются строкой `"none"`.
- **`tool_content` у Codex (⚠).** По типу `0` допустим, конфиг
  с `tool_result.max_bytes = 0` загружается [run CX]. Что Codex отправляет
  при `0`, экспериментом не проверено. Ожидается пустой вывод с маркером
  обрезки. `output_truncated`, длины и межагентные сообщения
  `codex.agent_communication.content` продолжают уходить
  [CX §2.2, settings.md сноска 5].
- **`tool_details` у Claude.** Без `OTEL_LOG_TOOL_DETAILS` Claude Code
  заменяет настоящие имена агентов, скиллов, плагинов и MCP-серверов
  обобщёнными [CC §2.7]. Для атрибутов метрик это не проверено
  (settings.md, сноска 3).

## 4. Запрет папки

### Claude Code

✗ **Запрет папки на нативный OTel Claude не действует.** Claude Code
2.1.285 не применяет `OTEL_*` из `.claude/settings.json`
и `.claude/settings.local.json` проекта даже при старте новой сессии:
действуют только пользовательские настройки и окружение [HT-78].
HT-50 называл выключающие значения в проектных настройках рабочими
[CC §2.2, §5 п. 1], эксперимент это не подтвердил.

- Бинарь ничего не пишет в `<папка>/.claude/` ради нативного OTel.
- Сессии Claude Code в запрещённой папке отправляют нативный OTel
  по пользовательскому `~/.claude/settings.json`. Хуки и транскрипт
  такой сессии бинарь не отправляет (settings.md, «Порядок применения»).
- Чтобы из папки ничего не уходило, нужен запрет источника или агента.
  Страница показывает это ограничение рядом с запретом папки, как
  и для Codex.

### Codex

✗ **Запрет папки на нативный OTel Codex не действует.** Codex полностью
игнорирует проектный `[otel]`: `otel` входит в
`PROJECT_LOCAL_CONFIG_DENYLIST` [CX §2.1]. Бинарь ничего не пишет
в `<папка>/.codex/`. Сессии Codex в запрещённой папке отправляют нативный
OTel по пользовательскому `~/.codex/config.toml`. Чтобы из папки ничего
не уходило, нужен запрет источника или агента. Страница показывает это
ограничение рядом с запретом папки (settings.md, сноска 2).

## 5. Когда изменение вступает в силу

| Агент | Что меняется | Когда действует | Источник |
|---|---|---|---|
| Claude Code | `env` в `~/.claude/settings.json` | с новой сессии. Идущая сессия ни добавление ключей OTel, ни смену `OTEL_LOG_USER_PROMPTS` не подхватывает | [HT-78] |
| Codex | `[otel]` в `~/.codex/config.toml` | с нового процесса Codex: провайдер OTel создаётся один раз на процесс. `codex exec` — при каждом запуске. Идущая сессия TUI изменение не видит. `codex-app-server` (им пользуются TUI и Codex Desktop) долгоживущий и пересобирает провайдер только при смене аккаунта, поэтому новые сессии внутри него изменение не видят; нужен перезапуск | [CX §2.1, §5 п. 3]; [HT-78] |

Страница настроек предупреждает об этом при сохранении: у Claude —
«с новой сессии», у Codex — «после перезапуска Codex». Сроки для хуков
и полная таблица наблюдений — в [apply-timing.md](apply-timing.md).

## 6. Поля идентичности, которые агенты отправляют сами

Эти поля агенты добавляют без участия бинаря. Ключами из этого документа
их не выключить, кроме UUID на метриках Claude (см. ниже).

Пользователя сервиса они не определяют: его определяет только атрибут
`hottell.user.id`, который проставляет сервер по токену коллектора
(HT-51). Поля ниже приходят как данные агента. `user.email` —
персональные данные.

### Claude Code

На всех метриках и событиях [CC §2.3]:

| Поле | Что это |
|---|---|
| `user.email` | email аккаунта; уходит всегда, когда известен [CC §5 п. 15] |
| `user.account_uuid` | UUID аккаунта |
| `user.account_id` | ID аккаунта |
| `user.id` | анонимный ID установки из `~/.claude.json` |
| `organization.id` | организация аккаунта |
| `session.id` | сессия Claude Code |
| `ccr.session.id` | сессия в облаке (только облачные сессии) |
| `terminal.type` | терминал |
| `user.groups`, `identity.source` | только через gateway |

Ресурс: `service.name` (`claude-code` или `claude-code-desktop`),
`service.version`, `os.type`, `os.version`, `host.arch`, а также ключи
из `OTEL_RESOURCE_ATTRIBUTES`: `host.name`, `deployment.environment`,
`hottell.version` [CC §2.3].

`OTEL_METRICS_INCLUDE_ACCOUNT_UUID=false` убирает `user.account_uuid`
и `user.account_id` только с метрик [CC §2.1, §5 п. 15]. Бинарь этот ключ
не пишет: по умолчанию собирается всё.

### Codex

На каждом лог-событии [CX §2.2, §5 п. 9]:

| Поле | Что это |
|---|---|
| `user.email` | email аккаунта ChatGPT; в каждом событии, от конфига не зависит |
| `user.account_id` | ID аккаунта; в каждом событии, от конфига не зависит |
| `conversation.id` | сессия (тред) Codex; по коду равна `session_id` хука, напрямую не сверялось [CX §4] |
| `auth_mode` | способ входа |
| `originator` | клиент (`codex_exec` и т. п.) |
| `terminal.type` | терминал |

В трейсах `user.email` и `user.account_id` нет [CX §2.2].
Ресурс: `service.name`, `service.version`, `env` (из `environment`),
`host.name` (только у логов) [CX §2.1]. Метрики несут `auth_mode`
и `originator`, но не `conversation.id` [CX §2.3].
