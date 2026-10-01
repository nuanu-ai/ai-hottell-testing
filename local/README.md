# Локальный стенд телеметрии агентов

Всё на своей машине: OTel Collector + ClickHouse в Docker, `hottell` шлёт туда
всё, что умеют Claude Code и Codex. Без токена, без настроек, без фильтров.

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

## Запуск

```sh
docker compose -f local/docker-compose.yml up -d     # стенд: OTLP :4318, ClickHouse :8123
cd hottell && go build -o /tmp/hottell . && /tmp/hottell install
```

`install` ничего не спрашивает и:

- ставит хуки Claude Code и Codex — события отовсюду, с текстами промптов;
- включает нативный OTel обоих агентов на `http://localhost:4318`: метрики, логи,
  трейсы, промпты, детали и содержимое инструментов, ответы ассистента, тела API;
- регистрирует MCP-сервер `hottell` у обоих агентов;
- шлёт тестовое событие.

Руками: в Codex `/hooks` → доверить хуки hottell; перезапустить открытые сессии.

## Где данные

http://localhost:8123/play — пользователь `default`, без пароля, база `otel`.

```sql
-- что пришло и от кого
SELECT ServiceName, LogAttributes['event.name'] ev, count()
FROM otel.otel_logs GROUP BY 1, 2 ORDER BY 1, 3 DESC;

-- промпты обоих агентов
SELECT Timestamp, ServiceName, LogAttributes['prompt']
FROM otel.otel_logs WHERE LogAttributes['prompt'] != '' ORDER BY Timestamp DESC LIMIT 20;

-- события хуков
SELECT LogAttributes['agent'], Body, count()
FROM otel.otel_logs WHERE ServiceName = 'agent-hooks' GROUP BY 1, 2;

-- стоимость и токены Claude Code
SELECT MetricName, Attributes['model'], Attributes['type'], sum(Value)
FROM otel.otel_metrics_sum WHERE MetricName IN ('claude_code.cost.usage', 'claude_code.token.usage')
GROUP BY 1, 2, 3;
```

Хранение 30 суток. Остановить: `docker compose -f local/docker-compose.yml down`
(`down -v` — вместе с данными).
