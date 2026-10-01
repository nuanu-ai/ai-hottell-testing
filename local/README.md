# Локальный стенд телеметрии агентов

Всё на своей машине: OTel Collector + ClickHouse в Docker, `hottell` шлёт туда
всё, что умеют Claude Code и Codex. Без токена, без настроек, без фильтров.

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
