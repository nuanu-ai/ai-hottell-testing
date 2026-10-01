# Контракт hottell: интерфейс MCP

Задача HT-54, эпик HT-51. Документ задаёт MCP-сервер `/mcp` Go-сервиса:
как ключ MCP попадает в конфиги агентов и откуда его берёт бинарь `hottell`,
как сервер проверяет ключ, какие инструменты и какой ресурс он отдаёт
и как бинарь держит подписку на настройки. По нему строятся каркас сервера
(HT-65), инструменты (HT-67, HT-68), ресурс настроек (HT-71), страница
«Подключение» (HT-66) и MCP-клиент бинаря.

Документ настроек и его версия — `settings.md` (HT-52) и
[`settings.schema.json`](settings.schema.json) (HT-56). Приём OTLP, куда
бинарь отправляет данные с токеном коллектора, — `ingest.md`.

Версии, на которых проверено: Claude Code 2.1.285, Codex CLI 0.159.0
(`rust-v0.159.0`), macOS arm64, 2026-10-01. Все ключи, токены, адреса
и почта в примерах синтетические.

## Сервер

- Транспорт — streamable HTTP, официальный `github.com/modelcontextprotocol/go-sdk`.
  Поведение SDK, на которое опирается документ, сверено с v1.8.0.
- Адрес — `<HT_PUBLIC_ORIGIN>/mcp`, тот же сервис и тот же origin, что
  у SPA и приёма `/v1/*`. `HT_PUBLIC_ORIGIN` — переменная конфигурации
  сервиса, например `http://localhost:8080`.
- В ответе на `initialize` сервер называет себя `hottell`, версия — версия
  сервиса. Возможности сервера:

  ```json
  { "tools": { "listChanged": false }, "resources": { "subscribe": true, "listChanged": false } }
  ```

- Сессия MCP (`Mcp-Session-Id`) принадлежит пользователю, чей ключ открыл
  её в `initialize`. Сессии живут в памяти процесса: после перезапуска
  сервиса или при чужом `Mcp-Session-Id` сервер отвечает `404`, клиент
  заново проходит `initialize` (так требует спецификация MCP
  для streamable HTTP).
- Неизвестный метод — ошибка JSON-RPC `-32601` (так отвечает go-sdk).
  Claude Code перед `initialize` шлёт `server/discover` и после `-32601`
  продолжает обычным `initialize` (см. «Проверка», вывод Claude Code).

## Имя сервера и конфиги агентов

Имя сервера в конфигах агентов постоянное: **`hottell`**. Бинарь ищет
только это имя.

Ключ MCP и токен коллектора — непрозрачные строки из символов
`A–Z a–z 0–9 _ -` (`^[A-Za-z0-9_-]+$`): их можно без экранирования вставить
в строку TOML, в JSON, в аргумент оболочки в кавычках и в заголовки OTel.
Её формат задаёт сценарий выпуска ключа (HT-63): сейчас это 32 случайных
байта в base64url без выравнивания. Бинарь ключ не разбирает и не проверяет,
а передаёт как есть. `ht_mcp_…` и `ht_col_…` в примерах — только заполнители, как в `native-otel.md`.

### Claude Code

Страница «Подключение» даёт команду, которую пользователь выполняет
в терминале:

```sh
claude mcp add --scope user --transport http hottell http://localhost:8080/mcp \
  --header "Authorization: Bearer ht_mcp_0123456789abcdef"
```

`--scope user` обязателен: без него Claude Code записывает сервер в область
текущего проекта (`projects.<путь>.mcpServers`), и он виден только в этой
папке. Команда записывает в `~/.claude.json` (при заданном
`CLAUDE_CONFIG_DIR` — в `$CLAUDE_CONFIG_DIR/.claude.json`) объект верхнего
уровня:

```json
{
  "mcpServers": {
    "hottell": {
      "type": "http",
      "url": "http://localhost:8080/mcp",
      "headers": {
        "Authorization": "Bearer ht_mcp_0123456789abcdef"
      }
    }
  }
}
```

Бинарь читает из `mcpServers.hottell`:

| Поле | Что делает бинарь |
|---|---|
| `type` | должно быть `http`; другое значение — запись не годится |
| `url` | адрес MCP-сервера, ровно как записан |
| `headers.Authorization` | заголовок авторизации целиком, `Bearer <ключ>`. Имя заголовка ищется без учёта регистра |

Остальные поля записи бинарь не читает. Области `local`
(`projects.<путь>.mcpServers.hottell`) и `project` (`.mcp.json` в папке
проекта) бинарь не читает: если `hottell` есть только там,
`hottell status` показывает «сервер hottell найден не в области user,
выполните команду со страницы «Подключение»».

### Codex

**Результат проверки: Codex 0.159.0 поддерживает `http_headers`
у streamable HTTP-сервера и отправляет этот заголовок в каждом запросе
к серверу.** Доказательство — в разделе «Проверка `http_headers` у Codex».
Поэтому запасной путь с копией ключа при установке не нужен и не
реализуется.

`codex mcp add` умеет только `--url` и `--bearer-token-env-var`, записать
им `http_headers` нельзя (`codex mcp add --help` в 0.159.0: из опций
транспорта HTTP есть только `--url`, `--bearer-token-env-var`
и `--oauth-*`). Поэтому страница «Подключение» даёт блок, который
пользователь вставляет в `~/.codex/config.toml` (при заданном `CODEX_HOME` —
в `$CODEX_HOME/config.toml`):

```toml
[mcp_servers.hottell]
url = "http://localhost:8080/mcp"
http_headers = { Authorization = "Bearer ht_mcp_0123456789abcdef" }
```

Бинарь читает из таблицы `mcp_servers.hottell`:

| Поле | Что делает бинарь |
|---|---|
| `url` | адрес MCP-сервера, ровно как записан |
| `http_headers.Authorization` | заголовок авторизации целиком, `Bearer <ключ>`. Имя заголовка ищется без учёта регистра |

Ключ в переменной окружения (`bearer_token_env_var`, `env_http_headers`)
и `http_headers_helper` бинарь не поддерживает: фоновый процесс под launchd
окружения оболочки не видит. Если в таблице нет `http_headers.Authorization`,
запись не годится, и `hottell status` показывает «ключ MCP для Codex
не в http_headers, вставьте блок со страницы «Подключение»». Поле `enabled`
бинарь не читает.

### Какую запись берёт бинарь

1. Бинарь читает запись `hottell` из `~/.claude.json`, затем из
   `~/.codex/config.toml` (или из путей по `CLAUDE_CONFIG_DIR`
   и `CODEX_HOME`).
2. Берётся первая годная запись в этом порядке: **при сервере в обоих
   конфигах URL и ключ берутся из `~/.claude.json`**. Запись Codex
   используется, только если в `~/.claude.json` нет годной записи.
3. URL и ключ всегда берутся из одной записи, не смешиваются.
4. Годной записи нет ни в одном конфиге — бинарь не подключается к MCP,
   `hottell status` называет причину по каждому конфигу.

Установка запускается из оболочки пользователя и видит `CLAUDE_CONFIG_DIR`
и `CODEX_HOME`. Она записывает найденные пути обоих конфигов в состояние
бинаря, и фоновый процесс читает конфиги по этим путям.

Своей копии ключа и адреса у бинаря нет. Бинарь читает запись заново
**при каждом подключении** к MCP. Если пользователь перевыпустил ключ
и обновил конфиг агента, бинарь подхватит новый ключ при следующем
переподключении (см. «Разрыв и переподключение»).

## Авторизация

- Клиент передаёт ключ в каждом HTTP-запросе к `/mcp` (`POST`, `GET`,
  `DELETE`) заголовком `Authorization: Bearer <ключ>`. Схема `Bearer`
  сравнивается без учёта регистра, между схемой и ключом один пробел.
- Сервер опознаёт пользователя сценарием `Resolve(mcp, <ключ>)` (HT-63)
  на каждом запросе, до разбора JSON-RPC. `/mcp` обходит сессию браузера
  и CSRF-проверку `Origin`: они для SPA.
- Ключ не принимается, если его нет, заголовок не в форме `Bearer <ключ>`,
  ключ неизвестен, отозван или другого вида (например, токен коллектора).
  Во всех этих случаях ответ одинаковый — сервер не сообщает, почему ключ
  не подошёл:

  ```http
  HTTP/1.1 401 Unauthorized
  WWW-Authenticate: Bearer realm="hottell", error="invalid_token"
  Content-Type: application/json

  {"error":"unauthorized","message":"MCP key is missing, unknown or revoked; issue a new one on the Connect page"}
  ```

  Без заголовка `Authorization` в `WWW-Authenticate` нет `error`
  (RFC 6750, 3.1): `WWW-Authenticate: Bearer realm="hottell"`.
- Сервер не отдаёт метаданные OAuth (`/.well-known/oauth-protected-resource`,
  `/.well-known/oauth-authorization-server` — `404`). Оба агента при заданном
  заголовке `Authorization` в OAuth не уходят: Claude Code пишет «OAuth
  fallback is disabled when headers.Authorization is set», Codex при
  `Authorization` в `http_headers` не создаёт OAuth-провайдера
  (`rmcp_client.rs`, см. «Проверка»).
- Отзыв и перевыпуск ключа. Перевыпуск на странице «Подключение» отзывает
  прежний ключ. Каждый следующий запрос со старым ключом получает `401`.
  Уже открытый поток `GET` сервер при отзыве не рвёт; ключ проверяется
  снова на следующем запросе клиента. Безопасность в эпике отложена,
  более строгое поведение — отдельной задачей.

**Что делает бинарь на `401`.** Прекращает попытки через этот ключ до
следующего переподключения, показывает в `hottell status` «ключ MCP
отклонён сервером (401): выпустите новый на странице «Подключение»
и обновите конфиг агента» и переподключается по правилам раздела
«Разрыв и переподключение», каждый раз перечитывая конфиг. Уже полученный
токен коллектора и применённые настройки остаются в силе: отправка данных
не зависит от ключа MCP.

## Инструменты

Общие правила для всех инструментов:

- Вход — объект; у всех инструментов этой версии он пустой: `{}`
  (`"additionalProperties": false`). Лишнее поле — результат с
  `isError: true` и текстом go-sdk `validating "arguments": …` (так go-sdk
  проверяет вход по схеме). Неизвестное имя инструмента — ошибка JSON-RPC
  `-32602` (Invalid params).
- Успешный результат несёт `structuredContent` по выходной схеме
  инструмента и тот же объект JSON-текстом в `content[0]`
  (`{"type": "text", "text": "<JSON>"}`) — так делает go-sdk для
  инструмента с типизированным выходом.
- Ошибка выполнения — результат с `isError: true`, без
  `structuredContent`, и одним `content` вида
  `{"type": "text", "text": "<код>: <сообщение>"}`. Коды:
  - `internal` — сбой хранилища или другой внутренний сбой; клиент
    повторяет позже.
- Ошибок авторизации у инструментов нет: запрос без годного ключа
  отвергается `401` до вызова (см. «Авторизация»).
- Пользователь всегда тот, чей ключ пришёл в запросе. Чужие данные
  через инструменты недоступны.

| Имя | Кто вызывает | Задача |
|---|---|---|
| `ping` | агент, установка бинаря | HT-65 |
| `get_collector_token` | бинарь | HT-67 |
| `get_settings` | бинарь, клиенты без ресурсов | HT-71 |
| `get_install_instructions` | агент пользователя, установка бинаря | HT-68 |

### `ping`

Проверка подключения и ключа.

- Аннотации: `readOnlyHint: true`.
- Входная схема:

  ```json
  { "type": "object", "properties": {}, "additionalProperties": false }
  ```

- Выходная схема:

  ```json
  {
    "type": "object",
    "required": ["version", "email"],
    "additionalProperties": false,
    "properties": {
      "version": { "type": "string", "description": "Версия сервиса." },
      "email": { "type": "string", "description": "Email пользователя, чей ключ MCP пришёл в запросе." }
    }
  }
  ```

- Ошибки: `internal`.
- Пример:

  ```json
  {"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ping","arguments":{}}}
  ```

  ```json
  {"jsonrpc":"2.0","id":2,"result":{
    "structuredContent":{"version":"0.1.0","email":"dev@example.test"},
    "content":[{"type":"text","text":"{\"version\":\"0.1.0\",\"email\":\"dev@example.test\"}"}]
  }}
  ```

### `get_collector_token`

Токен коллектора пользователя и адрес приёма OTLP. С этим токеном бинарь
отправляет хуки и транскрипты, а нативный OTel агентов — свои сигналы
(`ingest.md`).

- Сценарий — `IngestToken(userID)` (HT-63): возвращает действующий токен,
  а если его нет, создаёт новый. Прежний не отзывается. Токен один на
  пользователя, все машины получают один и тот же.
- Аннотации: `readOnlyHint: false`, `idempotentHint: true`.
- Описание инструмента говорит агенту, что инструмент нужен бинарю
  `hottell` и агенту его вызывать не нужно.
- Входная схема:

  ```json
  { "type": "object", "properties": {}, "additionalProperties": false }
  ```

- Выходная схема:

  ```json
  {
    "type": "object",
    "required": ["token", "otlp_endpoint"],
    "additionalProperties": false,
    "properties": {
      "token": {
        "type": "string",
        "pattern": "^[A-Za-z0-9_-]+$",
        "description": "Токен коллектора, открытым значением. Без символов «,» и «=»: у Claude заголовки OTel задаются строкой ключ=значение через запятую (native-otel.md)."
      },
      "otlp_endpoint": {
        "type": "string",
        "format": "uri",
        "description": "Базовый адрес приёма OTLP/HTTP: HT_PUBLIC_ORIGIN без пути /v1/...; клиент дописывает /v1/logs, /v1/metrics, /v1/traces."
      }
    }
  }
  ```

- Ошибки: `internal`.
- Когда вызывает бинарь: при каждом подключении фонового процесса к MCP
  и после ответа `401` приёма OTLP на токен коллектора (токен перевыпущен
  на странице; ответы приёма задаёт вторая часть `ingest.md`, HT-57).
  Токен отличается от сохранённого — бинарь сохраняет новый и переписывает
  нативный OTel в конфигах агентов.
- Пример:

  ```json
  {"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_collector_token","arguments":{}}}
  ```

  ```json
  {"jsonrpc":"2.0","id":3,"result":{
    "structuredContent":{"token":"ht_col_0123456789abcdef","otlp_endpoint":"http://localhost:8080"},
    "content":[{"type":"text","text":"{\"token\":\"ht_col_0123456789abcdef\",\"otlp_endpoint\":\"http://localhost:8080\"}"}]
  }}
  ```

### `get_settings`

Текущие настройки-запреты пользователя. Те же данные, что в ресурсе
`hottell://settings`, для клиентов без поддержки ресурсов. Бинарь читает
настройки через ресурс.

- Аннотации: `readOnlyHint: true`.
- Входная схема:

  ```json
  { "type": "object", "properties": {}, "additionalProperties": false }
  ```

- Выходная схема — [`settings.schema.json`](settings.schema.json) целиком:
  сервер встраивает её содержимое в `outputSchema` инструмента.
  `structuredContent` — документ настроек в полной форме, с `version`
  (`settings.md`, «Поле версии» и «Схема и примеры»). У пользователя,
  который ни разу не сохранял настройки, — документ «ничего не запрещено»
  с `version: 0`.
- Ошибки: `internal`.
- Пример (документ сокращён до одного агента; сервер всегда отдаёт оба):

  ```json
  {"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"get_settings","arguments":{}}}
  ```

  ```json
  {"jsonrpc":"2.0","id":4,"result":{
    "structuredContent":{
      "version":12,
      "agents":{"claude":{"enabled":true,
        "sources":{"hooks":true,"transcripts":true,"native_metrics":true,"native_logs":true,"native_traces":false},
        "hook_events":{"denied":["MessageDisplay"]},"hook_fields":{"denied":["tool_response"]},
        "native_content":{"denied":["raw_api_bodies"]}}},
      "folders":{"denied":["~/work/**"],"allowed":["~/work/oss/**"]},
      "backfill_history":true
    },
    "content":[{"type":"text","text":"{\"version\":12,…}"}]
  }}
  ```

  Полный документ — [`examples/settings-full.json`](examples/settings-full.json).

### `get_install_instructions`

Как поставить бинарь на эту машину. Агент вызывает инструмент, когда
пользователь просит поставить телеметрию, и либо выполняет команду сам,
**после подтверждения пользователя**, либо показывает её. Это сказано
в описании инструмента.

- Аннотации: `readOnlyHint: true`.
- Входная схема:

  ```json
  { "type": "object", "properties": {}, "additionalProperties": false }
  ```

- Выходная схема:

  ```json
  {
    "type": "object",
    "required": ["command", "summary", "manual_steps"],
    "additionalProperties": false,
    "properties": {
      "command": {
        "type": "string",
        "description": "Команда установки: curl -fsSL <HT_PUBLIC_ORIGIN>/install.sh | sh. Токенов и ключей в ней нет."
      },
      "summary": {
        "type": "string",
        "description": "Короткий текст для пользователя: что делает установка."
      },
      "manual_steps": {
        "type": "array",
        "description": "Ручные шаги после установки, по порядку.",
        "items": { "type": "string" }
      }
    }
  }
  ```

- Ошибки: `internal`.
- Пример:

  ```json
  {"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"get_install_instructions","arguments":{}}}
  ```

  ```json
  {"jsonrpc":"2.0","id":5,"result":{
    "structuredContent":{
      "command":"curl -fsSL http://localhost:8080/install.sh | sh",
      "summary":"Скрипт скачивает бинарь hottell для этого Mac, находит сервер hottell в конфиге Claude Code или Codex и запускает фоновый процесс под launchd. Он ставит хуки и нативный OTel агентов по настройкам со страницы «Что отправлять». Ключей и токенов в команде нет.",
      "manual_steps":[
        "Если установка остановилась на чужих хуках (foreign hooks), которые запускают бинарь hottell, покажите пользователю их список из вывода установки. Только с его согласия запустите установку снова с флагом, который удаляет эти хуки: curl -fsSL http://localhost:8080/install.sh | sh -s -- --replace-foreign-hooks. Конфиги агентов перед удалением сохраняются в резервную копию.",
        "Перезапустите открытые сессии Claude Code и Codex, чтобы они подхватили хуки и нативный OTel.",
        "Codex: если hottell status сообщает, что доверие к хукам не записано, выполните /hooks в Codex и подтвердите хуки hottell."
      ]
    },
    "content":[{"type":"text","text":"{\"command\":\"curl -fsSL http://localhost:8080/install.sh | sh\",…}"}]
  }}
  ```

## Ресурс настроек

- **URI**: `hottell://settings`. URI один для всех пользователей;
  содержимое — настройки пользователя, чей ключ пришёл в запросе.
- `resources/list` возвращает этот ресурс:

  ```json
  {"resources":[{"uri":"hottell://settings","name":"settings","title":"Настройки-запреты hottell","mimeType":"application/json"}]}
  ```

- `resources/read` с `{"uri": "hottell://settings"}` возвращает одно
  содержимое: `mimeType` `application/json`, `text` — документ настроек
  в полной форме по [`settings.schema.json`](settings.schema.json), с `version`
  (тот же объект, что `structuredContent` инструмента `get_settings`).
- `resources/subscribe` и `resources/unsubscribe` с
  `{"uri": "hottell://settings"}` отвечают `{}`. Подписка принадлежит
  сессии MCP: при закрытии сессии (`DELETE`, разрыв, перезапуск сервиса)
  она снимается.
- `notifications/resources/updated` с `{"uri": "hottell://settings"}`
  сервер шлёт в каждую подписанную сессию пользователя (на всех его
  машинах) после каждого сохранения, которое увеличило `version`.
  Сохранение без изменений версию не меняет и уведомления не даёт.
  Содержимого в уведомлении нет. Уведомление идёт в поток `GET /mcp`
  сессии.
- Простой соединения: пока сессия подписана на `hottell://settings`, сервер
  шлёт в неё метод протокола MCP `ping` (не инструмент `ping`) каждые
  30 секунд по потоку `GET /mcp`, чтобы простаивающий поток не закрывали
  прокси. Неподписанные сессии сервер не пингует: запрос сервера доходит
  до клиента только по потоку `GET /mcp`, а клиент без подписки может его
  не открывать (Claude Code). Неотвеченный `ping` сервер только записывает
  в журнал и сессию не закрывает. Клиент отвечает на `ping` (go-sdk делает
  это сам) и тоже шлёт метод `ping` каждые 30 секунд
  (`ClientOptions.KeepAlive`); нет ответа — клиент считает соединение
  разорванным и переподключается.

Ошибки:

| Случай | Ответ |
|---|---|
| `resources/read`, `resources/subscribe` или `resources/unsubscribe` с другим URI | ошибка JSON-RPC `-32602` (Invalid params), `message: "Resource not found"`, `data: {"uri": "<URI>"}` — так отвечает `mcp.ResourceNotFoundError` go-sdk v1.8.0 по SEP-2164 |
| сбой хранилища при чтении | ошибка JSON-RPC `-32603` (Internal error) |
| нет годного ключа | HTTP `401` (см. «Авторизация») |
| неизвестная или чужая сессия | HTTP `404`, клиент заново проходит `initialize` |

Пример чтения, подписки и уведомления:

```json
{"jsonrpc":"2.0","id":6,"method":"resources/subscribe","params":{"uri":"hottell://settings"}}
{"jsonrpc":"2.0","id":6,"result":{}}

{"jsonrpc":"2.0","id":7,"method":"resources/read","params":{"uri":"hottell://settings"}}
{"jsonrpc":"2.0","id":7,"result":{"contents":[{"uri":"hottell://settings","mimeType":"application/json","text":"{\"version\":12,\"agents\":{…},\"folders\":{…},\"backfill_history\":true}"}]}}

{"jsonrpc":"2.0","method":"notifications/resources/updated","params":{"uri":"hottell://settings"}}

{"jsonrpc":"2.0","id":8,"method":"resources/read","params":{"uri":"hottell://settings"}}
{"jsonrpc":"2.0","id":8,"result":{"contents":[{"uri":"hottell://settings","mimeType":"application/json","text":"{\"version\":13,…}"}]}}

{"jsonrpc":"2.0","id":9,"method":"resources/read","params":{"uri":"hottell://other"}}
{"jsonrpc":"2.0","id":9,"error":{"code":-32602,"message":"Resource not found","data":{"uri":"hottell://other"}}}
```

### Что делает бинарь

- **После уведомления** бинарь перечитывает ресурс `resources/read`.
  Применяет настройки, только если их `version` больше версии последних
  применённых (`settings.md`, «Поле версии»); иначе ничего не делает.
  Несколько уведомлений подряд можно свести к одному чтению: чтение всегда
  отдаёт последнюю версию.
- **Подписка раньше чтения.** После каждого `initialize` бинарь сначала
  делает `resources/subscribe`, потом `resources/read`. Так изменение между
  чтением и подпиской не теряется.

### Разрыв и переподключение

Соединение считается разорванным, когда закрылся поток `GET /mcp`, запрос
вернул сетевую ошибку, `401`, `404` или `5xx`, или `ping` остался без
ответа.

1. Бинарь ждёт паузу и подключается заново: перечитывает запись `hottell`
   из конфигов агентов, проходит `initialize`, вызывает
   `get_collector_token`, `resources/subscribe` и `resources/read`.
   Исключение — `404` на сессию (сервис перезапущен): первый повторный
   `initialize` идёт сразу, без паузы; паузы начинаются, если и он
   не удался.
2. Паузы: 1, 2, 4, 8, 16, 32, 60 секунд, дальше 60 секунд; каждая
   меняется на случайные ±20 %. После `401` пауза не меньше 60 секунд:
   ключ меняется только руками пользователя.
3. Счётчик пауз сбрасывается, когда подключение продержалось 60 секунд.
4. Пока соединения нет, бинарь работает на последних применённых
   настройках и последнем токене коллектора. Изменения, сделанные за это
   время, бинарь получает чтением ресурса после переподключения.
5. Ошибка отдельного вызова при живом соединении — `isError` у
   `get_collector_token`, `-32603` у `resources/read` или
   `resources/subscribe` — не разрыв. Бинарь повторяет этот вызов
   по тем же паузам, соединение не закрывает и продолжает работать
   на том, что у него уже есть. Пока нет ни одного токена коллектора,
   бинарь ничего не отправляет и не пишет нативный OTel; пока нет
   ни одних применённых настроек, он не ставит хуки.

## Хуки бинаря в конфигах агентов

Бинарь ставит по одному command-хуку на каждое своё событие: у Claude Code —
в `hooks` файла `~/.claude/settings.json`, у Codex — в `~/.codex/hooks.json`
(доверие к ним — `codex-trust.md`). Хук синхронный, без матчера, один в своей
группе. Команда:

```sh
'<путь к бинарю>' hook claude >/dev/null 2>&1 || true
'<путь к бинарю>' hook codex >/dev/null 2>&1 || true
```

Путь берётся в одинарные кавычки, только если в нём есть символы помимо
`A–Z a–z 0–9 / . _ - + @ % : , =`. Оба агента запускают команду через
оболочку.

**Хук не может помешать агенту**, что бы ни случилось с бинарём (HT-112):
пользователь может случайно удалить или подменить его. Оба агента действуют
по поведению хука: код выхода 2 блокирует вызов инструмента, промпт или
остановку, а stdout при коде 0 читается как решение. Поэтому stdout
и stderr бинаря уходят в `/dev/null`, а `|| true` даёт код 0 при любом
исходе: бинарь отсутствует, падает, выходит с кодом 2 или печатает решение.
Сам бинарь на хуке тоже всегда выходит с кодом 0 и ничего не печатает;
обёртка нужна на случай, когда запускается не он.

**Таймаут** (в секундах) задан у каждого хука, чтобы зависший бинарь
не держал агента до таймаута по умолчанию (600 с у обоих):

| Агент | События | `timeout` |
| --- | --- | --- |
| Claude Code | все, кроме `SessionEnd` | 10 |
| Claude Code | `SessionEnd` | 1 — в пределах общего бюджета 1,5 с на хуки `SessionEnd` |
| Codex | все, кроме `SessionEnd` и `Interrupt` | 10 |
| Codex | `SessionEnd`, `Interrupt` | 1 — как по умолчанию; Codex ждёт эти хуки при выходе и не даёт больше 3 |

**Свои хуки** бинарь узнаёт по команде: и в этой форме, и в форме
`'<путь к бинарю>' hook claude|codex` без обёртки и таймаута, которую
писали версии до HT-112. Установка и применение настроек заменяют хук
старой формы новым без дублей, но по-разному: у Claude Code все свои
обработчики события убираются, а новая группа добавляется в конец
события; у Codex новая группа пишется на место первой группы, состоящей
только из своих хуков, и добавляется в конец события, если такой нет.
Первая такая группа сохраняет свою позицию, а следующие свои группы того
же события (дубли) и свои обработчики в смешанных группах удаляются.
Позиция хука входит в ключ доверия (`codex-trust.md`), поэтому группы
других после удалённой группы-дубля сдвигаются, их ключи доверия
меняются, и Codex попросит заново подтвердить их в `/hooks`. Удаление
убирает обе формы.
Правило одно для обоих агентов и строгое: свой только обработчик
с `type: "command"`, чья команда — ровно одна из этих двух форм
с абсолютным путём к файлу `hottell`, записанным так, как его пишет
бинарь (без кавычек или в одинарных). Всё остальное чужое и остаётся
нетронутым: `echo hottell hook claude`, `hottell hook claude`
с относительным путём, `… hook claude --debug`, `… hook codex --dry-run`,
`~/bin/hottell hooks-audit`.

**Чужие хуки на пути бинаря** (HT-109) — обработчики с `type: "command"`
в тех же двух файлах, которые бинарь не считает своими, но чья команда
первым словом запускает файл, куда установка кладёт бинарь
(`~/.local/bin/hottell`). Первое слово берётся так, как его увидит
оболочка: без кавычек и экранирования, с раскрытыми `~` в начале,
`$HOME` и `${HOME}`; слово с другими подстановками не считается. Таковы
хуки прототипа `<путь>/hottell -agent claude|codex [-config …]`: они
получают вывод и код выхода бинаря без обёртки и дублируют события.
Команда, где путь стоит не первым словом (`echo ~/.local/bin/hottell …`,
`sh -c '…'`), чужим хуком на пути бинаря не считается и не трогается.

- `hottell install` ищет такие хуки первым шагом, до любой записи.
  Конфиг, который не удалось прочитать (битый JSON, повторяющиеся ключи,
  нет доступа), тоже останавливает установку с кодом 1, ничего не меняя:
  установка печатает файл и причину. Нашёл хуки и флага
  `--replace-foreign-hooks` нет — ничего не меняет, печатает список
  (файл, событие, команда) и подсказку о флаге, выходит с кодом 1.
  С флагом — после обращения к MCP и снятия резервной копии конфигов
  удаляет ровно эти обработчики (опустевшие группы и события — как при
  удалении своих хуков), печатает `foreign hooks removed N` и только
  затем заменяет бинарь; сбой любого шага до замены оставляет прежний
  бинарь на месте. У Codex удаление группы сдвигает следующие группы события,
  и Codex попросит заново подтвердить сдвинутые чужие хуки в `/hooks`.
- `install.sh` передаёт свои аргументы в `hottell install`:
  `curl -fsSL <HT_PUBLIC_ORIGIN>/install.sh | sh -s -- --replace-foreign-hooks`.
  Агент запускает её только с согласия пользователя, показав ему список
  (шаг из `manual_steps`).
- `hottell status` показывает каждый такой хук проблемой и выходит
  с кодом 1; конфиг, который не удалось проверить, — тоже проблема.

## Локальный сервер `hottell-local`

`hottell mcp-local` — второй MCP-сервер того же бинаря, для skill
`hottell-coach` (`analytics/coach/SKILL.md`). Транспорт — stdio: агент
запускает бинарь сам, по записи в своём конфиге. Имя сервера —
**`hottell-local`** (`mcpserver.Name`); оно не совпадает с `hottell`,
и запись сервиса из раздела «Имя сервера и конфиги агентов» бинарь
по-прежнему ищет только под своим именем.

**Сервер не обращается к сервису**: ни сети, ни ключа MCP, ни токена
коллектора. Он читает только файлы этой машины:

| Инструмент | Что отдаёт |
|---|---|
| `sessions_list` | транскрипты Claude Code (`~/.claude/projects`) и Codex (`sessions/` и `archived_sessions/` в Codex home, включая `.jsonl.zst`) |
| `session_read` | события одной сессии по страницам, с окном `from`/`to` |
| `session_stats` | агрегаты сессии или периода; страница ограничена числом сессий и объёмом транскриптов |
| `findings` | находки дашборда за период из каталога данных |
| `coach_journal` | журнал коуча `<каталог данных>/coach/journal.jsonl`: чтение и дописывание записи |

Каталог данных — `HOTTELL_DATA_DIR`, по умолчанию
`~/Life/projects/ai-hottell/local-data`. Каталоги агентов сервер находит
так же, как установка: по `CLAUDE_CONFIG_DIR` и `CODEX_HOME`; `CODEX_HOME`,
который не разрешается, берётся как задан. Переменные доходят до сервера
только из его окружения: Claude Code передаёт серверу своё окружение,
а Codex — лишь немногие переменные (такие как `HOME` и `PATH`). Поэтому
другой каталог данных или другой каталог агента задаётся в `env` записи:

```json
"hottell-local": { "type": "stdio", "command": "…", "args": ["mcp-local"], "env": { "HOTTELL_DATA_DIR": "/путь/к/local-data" } }
```

```toml
[mcp_servers.hottell-local.env]
HOTTELL_DATA_DIR = "/путь/к/local-data"
```

**Что пишет `hottell install`.** Шаг `mcp-local` регистрирует сервер
только у установленных агентов — у тех, чей каталог конфигов есть
(`~/.claude` или `CLAUDE_CONFIG_DIR`, Codex home). Агент, установленный
позже, получает запись при следующем `hottell install`. Путь, который есть,
но не каталог, — сбой шага. Команда — установленный бинарь
(`~/.local/bin/hottell`):

- Claude Code — `mcpServers.hottell-local` в `~/.claude.json` (при
  `CLAUDE_CONFIG_DIR` — в `$CLAUDE_CONFIG_DIR/.claude.json`), область user:

  ```json
  "hottell-local": { "type": "stdio", "command": "/Users/a/.local/bin/hottell", "args": ["mcp-local"], "env": {} }
  ```

- Codex — таблица в `config.toml` (`~/.codex` или `CODEX_HOME`):

  ```toml
  [mcp_servers.hottell-local]
  command = "/Users/a/.local/bin/hottell"
  args = ["mcp-local"]
  ```

Бинарю в записи принадлежат только `type`, `command` и `args`. Остальное —
`env`, у Codex `enabled`, `tool_timeout_sec`, подтаблица
`[mcp_servers.hottell-local.env]` — пользователя: повторная установка его
не трогает. Пустой `env` получает только новая запись Claude Code.
Существующая таблица Codex правится на месте, заменяются или вставляются
только строки `command` и `args`; таблица дописывается в конец файла, только
если её нет. Запись, которая уже такая, файл не переписывает.

Определение сервера у Codex вне своей таблицы — встроенной таблицей
в `[mcp_servers]`, точечным ключом в корне, массивом таблиц, а также
`mcp_servers = { … }` или `[[mcp_servers]]` — установка не правит: шаг
`mcp-local` — сбой (Codex не прочитал бы файл с добавленной таблицей).
Сбой шага не останавливает установку: отправка работает и без коуча;
итог установки — код 1.

`~/.claude.json` Claude Code постоянно переписывает сам. Поэтому
**`~/.claude.json` не входит в набор резервных копий**, и `hottell restore`
его не трогает: восстановление файла целиком откатило бы чужое состояние.
Бинарь меняет в нём только свою запись и перед заменой файла перечитывает
его: если файл изменился после чтения, правка повторяется со свежего
чтения (до трёх раз), и изменение Claude Code не теряется. `config.toml`
Codex в набор входит, и `hottell restore` возвращает его вместе с таблицей
`hottell-local`, какой она была.

**Что снимает `hottell uninstall`.** Запись `mcpServers.hottell-local`
и таблицу `[mcp_servers.hottell-local]` с её подтаблицами. Определение вне
таблицы — пользователя, удаление его оставляет и не считает сбоем. Сервер
`hottell` и другие серверы остаются.

## Последовательности

### Установка → первый запуск фонового процесса → первые настройки

1. Пользователь на странице «Подключение» выпускает ключ MCP (HT-66,
   `IssueMCPKey`). Страница один раз показывает команду для Claude Code
   и блок для Codex с этим ключом.
2. Пользователь выполняет команду `claude mcp add --scope user …` и/или
   вставляет блок `[mcp_servers.hottell]` в `~/.codex/config.toml`,
   затем перезапускает агента.
3. Пользователь просит агента поставить телеметрию. Агент вызывает
   `get_install_instructions`, показывает `command` и `summary`
   и после подтверждения выполняет `curl -fsSL <HT_PUBLIC_ORIGIN>/install.sh | sh`
   (или пользователь выполняет её сам).
4. Скрипт скачивает бинарь (HT-103) и запускает установку бинаря
   из оболочки пользователя. Установка сначала ищет чужие хуки на пути
   бинаря и без `--replace-foreign-hooks` останавливается на них, ничего
   не меняя («Хуки бинаря в конфигах агентов»).
5. Установка находит годную запись `hottell` («Какую запись берёт
   бинарь»), записывает пути конфигов в состояние бинаря, подключается
   к MCP и вызывает `ping`. `401`, сетевая ошибка или `isError` —
   установка завершается с понятным сообщением и ненулевым кодом,
   фоновый процесс не ставится.
6. Установка ставит и запускает агент launchd (фоновый процесс), затем
   вызывает `get_install_instructions` и печатает пользователю
   его `manual_steps`.
7. Фоновый процесс при первом запуске читает запись `hottell` по
   сохранённым путям и проходит `initialize`.
8. Вызывает `get_collector_token` и сохраняет токен и `otlp_endpoint`
   в состоянии бинаря.
9. Делает `resources/subscribe`, затем `resources/read` для
   `hottell://settings`. Применённых настроек ещё нет (версия `-1`),
   поэтому применяется любая пришедшая, в том числе `version: 0`.
10. Применяет настройки (`settings.md`, «Порядок применения»):
    - записывает свои хуки в конфиги включённых агентов («Хуки бинаря
      в конфигах агентов»), для Codex — с доверием к хукам (`codex-trust.md`);
    - записывает нативный OTel агентов с токеном и адресом из шага 8
      (ключи — [`native-otel.md`](native-otel.md));
    - сохраняет документ настроек локально для `hottell hook`
      и запоминает применённую `version`.
11. Начинает отправку хуков и транскриптов в приём OTLP (`ingest.md`),
    при `backfill_history: true` — и истории. Держит соединение MCP
    открытым и ждёт уведомлений.

### Изменение в интерфейсе → уведомление → применение

1. Пользователь меняет запреты на странице «Что отправлять» и сохраняет.
2. API настроек (HT-69) сохраняет документ, сервер увеличивает `version`
   на 1 (было `12`, стало `13`). Сохранение без изменений здесь
   останавливается: версия та же, уведомления нет.
3. Сценарий настроек сообщает `Notifier` (HT-62) об изменении у этого
   пользователя.
4. MCP-сервер шлёт `notifications/resources/updated`
   с `{"uri": "hottell://settings"}` в каждую подписанную сессию этого
   пользователя — по одной на каждую машину с работающим фоновым
   процессом.
5. Фоновый процесс на каждой машине получает уведомление и делает
   `resources/read`.
6. Пришла `version: 13`, применена `12` — бинарь применяет настройки,
   как в шаге 10 установки: переписывает свои хуки и нативный OTel
   в конфигах агентов, обновляет локальную копию для `hottell hook`
   и запоминает `13`. Пришла версия не больше применённой — ничего
   не делает.
7. Новые запреты действуют для следующих событий хуков и строк транскриптов
   сразу; изменения нативного OTel агенты подхватывают в новых сессиях.
8. Машина была без соединения в момент изменения — уведомление ей
   не приходит; она получает `version: 13` чтением ресурса после
   переподключения («Разрыв и переподключение», шаг 1).

## Проверка `http_headers` у Codex

### По исходникам

Тег `rust-v0.159.0` репозитория `openai/codex`, коммит
`687a119f0fcaace47e1f1abcc77cec6c813fd6da`.

- [`codex-rs/config/src/mcp_types.rs#L637-L655`](https://github.com/openai/codex/blob/rust-v0.159.0/codex-rs/config/src/mcp_types.rs#L637-L655) —
  вариант `McpServerTransportConfig::StreamableHttp` содержит
  `url`, `bearer_token_env_var`, `http_headers: Option<HashMap<String, String>>`
  («Additional HTTP headers to include in requests to this server»),
  `env_http_headers` и `http_headers_helper`.
- [`codex-rs/rmcp-client/src/utils.rs#L103-L128`](https://github.com/openai/codex/blob/rust-v0.159.0/codex-rs/rmcp-client/src/utils.rs#L103-L128) —
  `build_default_headers` вставляет каждую пару `http_headers` в заголовки
  HTTP-клиента по умолчанию.
- [`codex-rs/rmcp-client/src/rmcp_client.rs#L1106-L1140`](https://github.com/openai/codex/blob/rust-v0.159.0/codex-rs/rmcp-client/src/rmcp_client.rs#L1106-L1140) —
  для streamable HTTP заголовки по умолчанию строятся из `http_headers`,
  и если среди них есть `Authorization`, OAuth-провайдер и OAuth-токены
  не используются.
- [`codex-rs/codex-mcp/src/rmcp_client.rs#L872-L905`](https://github.com/openai/codex/blob/rust-v0.159.0/codex-rs/codex-mcp/src/rmcp_client.rs#L872-L905) —
  `bearer_token_env_var` читается из окружения процесса Codex; без
  переменной — ошибка «Environment variable … is not set». Поэтому ключ
  в переменной окружения не годится для фонового процесса под launchd.

### Живой запуск

Временные `CODEX_HOME` и `CLAUDE_CONFIG_DIR`, настоящие конфиги
не затронуты. MCP-сервер — синтетический HTTP-сервер на
`127.0.0.1:18777`: печатает метод и заголовок `Authorization` каждого
запроса и отвечает `401` на любой ключ, кроме синтетического
`hk_mcp_SYNTHETIC_PROBE_KEY`. Модель Codex в запуске не авторизована
(ответ OpenAI `401`), это не мешает: Codex подключается к MCP-серверам
при старте сессии, до обращения к модели.

`$CODEX_HOME/config.toml`:

```toml
[mcp_servers.hottell]
url = "http://127.0.0.1:18777/mcp"
http_headers = { Authorization = "Bearer hk_mcp_SYNTHETIC_PROBE_KEY" }
```

`CODEX_HOME=<tmp> codex mcp get hottell --json` (фрагмент):

```json
"transport": {
  "type": "streamable_http",
  "url": "http://127.0.0.1:18777/mcp",
  "bearer_token_env_var": null,
  "http_headers": {
    "Authorization": "Bearer hk_mcp_SYNTHETIC_PROBE_KEY"
  },
  "env_http_headers": null,
  "http_headers_helper": null
}
```

`CODEX_HOME=<tmp> codex exec --skip-git-repo-check "say hi"` — шапка
Codex `OpenAI Codex v0.159.0` (`codex --version`: `codex-cli 0.159.0`),
вывод сервера:

```text
POST /mcp method=initialize authorization='Bearer hk_mcp_SYNTHETIC_PROBE_KEY' matches=True
POST /mcp method=notifications/initialized authorization='Bearer hk_mcp_SYNTHETIC_PROBE_KEY' matches=True
GET /mcp authorization='Bearer hk_mcp_SYNTHETIC_PROBE_KEY'
POST /mcp method=tools/list authorization='Bearer hk_mcp_SYNTHETIC_PROBE_KEY' matches=True
```

Контрольные запуски с `bearer_token_env_var = "HOTTELL_MCP_KEY"` вместо
`http_headers`:

- переменная не задана — сервер не получил ни одного запроса (Codex
  отказывается подключаться: «Environment variable … is not set», см.
  исходник выше);
- `HOTTELL_MCP_KEY=hk_mcp_SYNTHETIC_PROBE_KEY` — вывод сервера:

  ```text
  POST /mcp method=initialize authorization='Bearer hk_mcp_SYNTHETIC_PROBE_KEY' matches=True
  POST /mcp method=notifications/initialized authorization='Bearer hk_mcp_SYNTHETIC_PROBE_KEY' matches=True
  GET /mcp authorization='Bearer hk_mcp_SYNTHETIC_PROBE_KEY'
  POST /mcp method=tools/list authorization='Bearer hk_mcp_SYNTHETIC_PROBE_KEY' matches=True
  ```

Для сравнения — Claude Code с записью `claude mcp add --scope user …`
во временном `CLAUDE_CONFIG_DIR`, `claude mcp list`:

```text
hottell: http://127.0.0.1:18777/mcp (HTTP) - ✔ Connected
```

```text
POST /mcp method=server/discover authorization='Bearer hk_mcp_SYNTHETIC_PROBE_KEY' matches=True
POST /mcp method=initialize authorization='Bearer hk_mcp_SYNTHETIC_PROBE_KEY' matches=True
POST /mcp method=notifications/initialized authorization='Bearer hk_mcp_SYNTHETIC_PROBE_KEY' matches=True
GET /mcp authorization='Bearer hk_mcp_SYNTHETIC_PROBE_KEY'
POST /mcp method=tools/list authorization='Bearer hk_mcp_SYNTHETIC_PROBE_KEY' matches=True
```

С неверным ключом сервер отвечает `401`, Claude Code пишет:

```text
hottell: http://127.0.0.1:18777/mcp (HTTP) - ✘ Failed to connect — Server rejected the configured Authorization header (HTTP 401). Check that the token is valid for this MCP endpoint — OAuth fallback is disabled when headers.Authorization is set.
```

Итог: Codex 0.159.0 читает `http_headers` из `[mcp_servers.hottell]`
и отправляет `Authorization` из него в `initialize`, в поток `GET`
и в `tools/list`, без переменных окружения. Блок с `http_headers` —
основной и единственный путь для Codex.
