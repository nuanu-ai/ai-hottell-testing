#!/bin/sh
# Поднять обе версии дашборда hottell локально (только loopback):
#   :8800 — «Глубокий разбор» (сырые журналы Codex + Deep 2.0 + 13 проверок);
#   :8801 — «Живые данные»: только записанные Hooks и OTel (ClickHouse локального стенда),
#           датасет пересобирается каждые 5 минут, в шапке — пульс из того же ClickHouse.
# Повторный запуск перезапускает оба процесса. Остановить: pkill -f ai-hottell-ui-v3
set -eu

ROOT=$(cd "$(dirname "$0")/.." && pwd)
BIN="$ROOT/local-data/bin/ai-hottell-ui-v3"
DATA="$ROOT/local-data/ui"
LIVE="$ROOT/local-data/ui-live"

mkdir -p "$ROOT/local-data/bin" "$DATA" "$LIVE"
chmod 700 "$DATA" "$LIVE"
(cd "$ROOT/ui" && go build -o "$BIN" .)
pkill -f "$BIN" 2>/dev/null || true

if [ ! -f "$DATA/dataset.json" ]; then
  if ! python3 "$ROOT/ui/builder/build.py" --out "$DATA"; then
    [ -s "$DATA/dataset.json" ] || exit 1
    echo "Глубокий разбор: пока нет локальных отчётов; открывается пустой датасет." >&2
  fi
fi
python3 "$ROOT/ui/builder/live.py" --out "$LIVE" || echo "live.py: ClickHouse недоступен? Запустите docker compose -f local/docker-compose.yml up -d" >&2

nohup "$BIN" -listen 127.0.0.1:8800 -data "$DATA" -builder "$ROOT/ui/builder/build.py" \
  -name "Глубокий разбор" -link "Живые данные=http://127.0.0.1:8801" \
  >"$DATA/server.log" 2>&1 &
nohup "$BIN" -listen 127.0.0.1:8801 -data "$LIVE" -builder "$ROOT/ui/builder/live.py" -refresh 5m \
  -clickhouse http://127.0.0.1:8123 \
  -name "Живые данные" -link "Глубокий разбор=http://127.0.0.1:8800" \
  >"$LIVE/server.log" 2>&1 &

sleep 1
echo "глубокий разбор: http://127.0.0.1:8800"
echo "живые данные:    http://127.0.0.1:8801"
