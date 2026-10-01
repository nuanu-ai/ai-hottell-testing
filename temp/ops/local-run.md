# Локальный запуск

Два пути: с Docker (всё в контейнерах, одна команда) и без него (свой PostgreSQL,
сервис и фронтенд запускаются на машине). Команды выполняются из корня репозитория.

## Требования

- [mise](https://mise.jdx.dev) — ставит go, node, pnpm, task и остальные инструменты
  в версиях из `mise.toml`:

  ```sh
  mise trust
  mise install
  ```

  Инструменты попадают в `PATH`, если в оболочке включён `mise activate`
  (см. документацию mise). Без него каждую команду запускать через `mise exec -- <команда>`,
  например `mise exec -- task dev`.
- Docker — для пути с Docker и для `task check`: тесты адаптеров БД поднимают PostgreSQL
  через testcontainers.
- Свободные порты: 8080 (сервис), 5432 (PostgreSQL), 5173 (Vite dev server, только в режиме
  разработки), 14317 и 14318 (OTel Collector, OTLP gRPC и HTTP), 18123 (ClickHouse, HTTP).
  Если порт занят, см. [Занятые порты](#занятые-порты).

## С Docker

```sh
task up
```

Собирает образ, поднимает PostgreSQL, применяет миграции, поднимает ClickHouse и OTel Collector
и запускает сервис. Приложение открывается на http://localhost:8080. Команда работает на переднем
плане, Ctrl+C останавливает контейнеры.

- Остановить и удалить контейнеры (база сохраняется в томе): `task down`.
- Сбросить базу: `docker compose down -v` — удаляет и том с данными; следующий `task up`
  создаст пустую базу и снова применит миграции.

### Раздача hottell

`/install.sh` и `/download/*` сервис берёт из последнего релиза `hottell-v*` в Gitea.
Репозиторий приватный, поэтому стенду нужен токен Gitea с правом чтения репозитория —
без него эти адреса отвечают 503. Токен передаётся только через окружение, в Git
его не писать:

```sh
HT_GITEA_TOKEN=<токен> task up
```

либо строкой `HT_GITEA_TOKEN=<токен>` в `.env` (файл в `.gitignore`): `docker compose`
сам читает `.env` из корня репозитория. Переменная из оболочки важнее `.env`.
`HT_GITEA_URL` и `HT_GITEA_REPO` задаются так же; пустые — значения по умолчанию
`https://git.alva.dev` и `alva/harness-telemetry`.

Проверка: `curl -fsS http://localhost:8080/install.sh | head` печатает начало скрипта.

### Коллектор и ClickHouse

Стенд приёма телеметрии: OTel Collector (конфиг `deployments/otel-collector.yaml`) принимает
OTLP и пишет его в ClickHouse, в базу `otel`. `task up` поднимает их вместе с сервисом;
только их, без сервиса и PostgreSQL, — `docker compose up clickhouse collector`.

- OTLP с хоста: gRPC — `localhost:14317`, HTTP — `http://localhost:14318`.
- Сервис передаёт принятое в коллектор по `HT_COLLECTOR_URL`: в compose это
  `http://collector:4318` (адрес внутри сети compose), при запуске сервиса на машине —
  `http://localhost:14318` (значение из `.env.example`, оно же по умолчанию).
- Запросы к ClickHouse — в браузере на http://localhost:18123/play, пользователь `ht`,
  пароль `ht` (локальные учётные данные, не секрет). Например, `SHOW TABLES FROM otel`.
- Данные ClickHouse хранятся в томе `chdata`; `docker compose down -v` удаляет и его.

## Без Docker

Коллектор и ClickHouse без Docker не поднимаются: этот путь запускает только PostgreSQL,
сервис и фронтенд. Для приёма телеметрии поднять их контейнерами
(`docker compose up clickhouse collector`), сервис тогда найдёт коллектор
по `HT_COLLECTOR_URL=http://localhost:14318` из `.env`.

1. Поставить PostgreSQL 17, например через Homebrew, и запустить его:

   ```sh
   brew install postgresql@17
   brew services start postgresql@17
   export PATH="$(brew --prefix postgresql@17)/bin:$PATH"
   ```

2. Создать пользователя и базу `ht` с паролем `ht` (это локальные учётные данные из
   `.env.example`, не секрет):

   ```sh
   psql -d postgres -c "CREATE ROLE ht LOGIN PASSWORD 'ht'"
   createdb -O ht ht
   ```

   Команды выше подключаются от имени пользователя ОС — так настроен PostgreSQL из
   Homebrew. Для другой установки добавить к `psql` и `createdb` её суперпользователя,
   хост и порт, например `-U postgres -h localhost -p 5432`.

3. Создать `.env` из примера. Taskfile сам загружает его в `task dev`, `task run:api`
   и `task migrate:*`; переменная, уже заданная в окружении оболочки, важнее `.env`.

   ```sh
   cp .env.example .env
   ```

   Если PostgreSQL слушает не `localhost:5432`, поправить `HT_DATABASE_URL` в `.env`.

4. Применить миграции и запустить режим разработки:

   ```sh
   task migrate:up
   task dev
   ```

   Приложение открывается на http://localhost:5173. Ctrl+C останавливает оба процесса.

## Первый пользователь

Для обоих путей: `cp .env.example .env` (если `.env` ещё нет), заполнить
`HT_BOOTSTRAP_EMAIL`, `HT_BOOTSTRAP_NAME`, `HT_BOOTSTRAP_PASSWORD` в `.env` и выполнить
`task user:create-first`, пока база запущена. Команда подключается по `HT_DATABASE_URL`
из `.env`; для пути с Docker подходит значение из примера — compose публикует базу
на `localhost:5432`.

Без входа приложение открывает страницу входа; после входа этим пользователем
открывается оболочка страницы.

## Режим разработки

`task dev` запускает параллельно:

- `task run:api` — собирает `bin/api` и запускает сервис на :8080;
- `task run:web` — Vite dev server с горячей перезагрузкой на http://localhost:5173;
  запросы к `/api` он проксирует на http://localhost:8080.

Работать в режиме разработки — через http://localhost:5173. Сервис на :8080 отдаёт
собранный фронтенд, а он появляется только после `task build:web` (или `task build`).

Порядок остальных команд — `task --list`.

## Проверки

- `task check` — вся проверка: генерация без изменений в дереве, формат, `go vet`,
  golangci-lint, go-arch-lint, `go test -race`, затем фронтенд — prettier, eslint,
  `tsc`, vitest и сборка. Нужен запущенный Docker.
- `task generate` — перегенерировать код: сервер oapi-codegen, моки mockgen, типы клиента
  API из `api/openapi/openapi.yaml`.

`task check` первым шагом выполняет `task generate` и падает с
`tree is dirty after task generate`, если после генерации `git status` не пуст.
Это значит одно из двух:

- изменён контракт или порт, а сгенерированный код не закоммичен — посмотреть
  `git status`, закоммитить сгенерированные файлы вместе с изменением;
- в дереве есть любые другие незакоммиченные изменения — проверка требует чистого дерева,
  поэтому сначала закоммитить их (или отложить), затем снова запустить `task check`.

## Браузерные тесты

`task e2e` — сценарии Playwright в Chromium на собранном приложении: вход по паролю,
приглашение, сброс пароля, смена своего пароля, выход. В `task check` не входит: нужен
запущенный Docker, свободный порт 18080 и несколько минут на сборку образа.

Команда:

1. ставит Chromium для Playwright, если его ещё нет (`playwright install chromium`);
2. поднимает отдельный стенд `docker compose -p ht-e2e -f compose.yaml -f compose.e2e.yaml
   up -d --build` — приложение на http://localhost:18080, база, коллектор и ClickHouse
   только внутри сети compose, так что запущенный рядом `task up` ему не мешает;
3. ждёт `/readyz` до 60 секунд;
4. заводит первого пользователя (`cli user create-first`) из `web/e2e/.env.e2e` — это
   тестовые значения, не секрет;
5. запускает тесты из `web/e2e/` (конфиг `web/playwright.config.ts`);
6. удаляет стенд вместе с базой (`down -v`) — и когда тесты упали тоже.

След упавшего теста остаётся в `web/test-results/`; открыть его —
`pnpm --dir web exec playwright show-trace <путь к trace.zip>`.

## Занятые порты

- Без Docker: порт базы задаётся в `HT_DATABASE_URL` в `.env`. Сервис должен остаться
  на :8080 — на этот адрес Vite проксирует `/api`.
- С Docker: порты зашиты в `compose.yaml`. Переопределить их можно своим файлом вне
  репозитория, например `~/ht-ports.yaml`:

  ```yaml
  services:
    postgres:
      ports: !override
        - "55432:5432"
    collector:
      ports: !override
        - "24317:4317"
        - "24318:4318"
    clickhouse:
      ports: !override
        - "28123:8123"
    app:
      ports: !override
        - "18080:8080"
      # Вход принимает запросы только со своего адреса — он должен совпадать с новым портом.
      environment:
        HT_PUBLIC_ORIGIN: http://localhost:18080
        HT_WEBAUTHN_RP_ORIGINS: http://localhost:18080
  ```

  ```sh
  docker compose -f compose.yaml -f ~/ht-ports.yaml up --build
  ```

  Приложение тогда открывается на http://localhost:18080. Останавливать и сбрасывать
  базу той же парой файлов вместо `task down`:
  `docker compose -f compose.yaml -f ~/ht-ports.yaml down` (с `-v` — вместе с томом).
  `HT_DATABASE_URL` в `.env` для `task user:create-first` тогда указывает на порт 55432.
  Сервис в compose ходит в коллектор по внутреннему адресу, его порты хоста не важны;
  ClickHouse тогда открывается на http://localhost:28123/play.
