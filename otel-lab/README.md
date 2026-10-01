# otel-lab — приёмник телеметрии агентов на 5 дней

OTel Collector (приём OTLP/HTTP, редакция секретов) → ClickHouse (хранение, TTL 10 суток, SQL по HTTP).
Развёрнуто 2026-09-29 диспетчером на swarm2, снятие запланировано на 2026-10-04.
Клиенты раскатаны на все рабочие хосты, Claude Code и Codex шлют с 2026-09-29.
Проверка 2026-09-29: обе службы стека 1/1, `otel.alva.dev` без токена отвечает 401,
`ch.alva.dev/ping` — Ok, конвейер коллектора запущен без ошибок.

## Где что

| Что | Значение |
|---|---|
| Стек | `otel-lab`, swarm2 (VM 102 на nuc2) |
| Данные | диск RBD 32 ГБ из `ceph-nvme`, `scsi2` у VM 102, ext4 `otel-lab`, `/var/lib/otel-lab` (fstab по UUID, `nofail`; бэкап `/etc/fstab.bak-otel-lab-20260929`) |
| Приём | `https://otel.alva.dev` → `collector:4318`, заголовок `Authorization: Bearer <otel_lab_ingest_token>`; публичное имя, `max_body` 50M |
| Забор | `https://ch.alva.dev` → `clickhouse:8123`, basic auth `reader:<пароль>` (только SELECT на `otel.*`), страница `/play`; публичное имя |
| Секреты стека | `otel_lab_ingest_token`, `otel_lab_ch_password` (пользователь `default`, только для коллектора и администрирования) — в хранилище диспетчера |
| Пользователь `reader` | заведён вручную после первого старта: `CREATE USER reader … SETTINGS readonly=1 …; GRANT SELECT ON otel.* TO reader`; пароль у владельца |

Таблицы создаёт экспортёр: `otel.otel_logs`, `otel.otel_traces`, `otel.otel_metrics_{gauge,sum,histogram,exponential_histogram,summary}`.

## Настройки на хостах-отправителях

Раскатаны на все рабочие хосты 2026-09-29; прежние конфиги сохранены с суффиксом
`bak-otel-lab-20260929`. Блоки ниже — образец для подключения нового хоста.

Claude Code — `~/.claude/settings.json`, блок `env`:
```
CLAUDE_CODE_ENABLE_TELEMETRY=1
OTEL_METRICS_EXPORTER=otlp
OTEL_LOGS_EXPORTER=otlp
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
OTEL_EXPORTER_OTLP_ENDPOINT=https://otel.alva.dev
OTEL_EXPORTER_OTLP_HEADERS=Authorization=Bearer <токен>
OTEL_RESOURCE_ATTRIBUTES=host.name=<машина>,deployment.environment=lab
OTEL_METRIC_EXPORT_INTERVAL=30000
OTEL_LOGS_EXPORT_INTERVAL=5000
OTEL_LOG_TOOL_DETAILS=1
# по желанию: OTEL_LOG_USER_PROMPTS=1  OTEL_LOG_ASSISTANT_RESPONSES=1
```

Codex — `~/.codex/config.toml`:
```toml
[otel]
environment = "lab"
log_user_prompt = false

[otel.exporter.otlp-http]
endpoint = "https://otel.alva.dev/v1/logs"
protocol = "binary"
headers = { Authorization = "Bearer <токен>" }

[otel.metrics_exporter.otlp-http]
endpoint = "https://otel.alva.dev/v1/metrics"
protocol = "binary"
headers = { Authorization = "Bearer <токен>" }

[otel.trace_exporter.otlp-http]
endpoint = "https://otel.alva.dev/v1/traces"
protocol = "binary"
headers = { Authorization = "Bearer <токен>" }
```

## Забор
```
curl -u reader:<пароль> 'https://ch.alva.dev/?query=SELECT+ServiceName,count()+FROM+otel.otel_logs+GROUP+BY+1+FORMAT+TSV'
curl -u reader:<пароль> 'https://ch.alva.dev/' --data-binary "SELECT Timestamp, Body FROM otel.otel_logs ORDER BY Timestamp DESC LIMIT 20 FORMAT Pretty"
```

## Грабли, на которых уже стояли

- **Знак доллара в compose недопустим, даже удвоенный.** Диспетчер раскрывает интерполяцию сам,
  Portainer получает одинарный знак и падает на разборе. Пароль ClickHouse — через штатный
  `CLICKHOUSE_PASSWORD_FILE`, healthcheck — bash `/dev/tcp` на `/ping`.
- **Короткое имя службы в конфиге коллектора уходит на wildcard Cloudflare**, если служба ещё не
  в DNS Docker (та же ловушка, что у Huly/nginx в `Server/WORKLOG.md`). В `collector/config.yaml`
  имя `clickhouse.` — с точкой в конце.
- **Экспортёр ClickHouse завершает процесс, если база недоступна при старте.** Коллектор
  перевыкатывается при каждом прогоне (новый тег образа), поэтому у него нет
  `failure_action: rollback` — Swarm перезапускает его с паузой, пока ClickHouse не поднимется.
- **Оверлейная сеть стека не attachable**: проверки изнутри — `docker exec` в контейнер ClickHouse.

## Снятие (2026-10-04)
1. Имена `otel.alva.dev` и `ch.alva.dev` — удалить из реестра и применить (первым делом).
2. Убрать блок `env` OTel из `~/.claude/settings.json` и секцию `[otel]` из `~/.codex/config.toml`
   (бэкапы с суффиксом `bak-otel-lab-20260929`).
3. Стек — удалить через Portainer/диспетчер; данные, если нужны, сначала выгрузить.
4. Диск: на swarm2 `umount /var/lib/otel-lab`, строка из fstab; на nuc2 `qm set 102 --delete scsi2`
   и `rbd rm ceph-nvme/vm-102-disk-1`.
