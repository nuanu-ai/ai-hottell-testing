#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
[ "$#" = 1 ] || { echo 'Usage: ./scripts/restore.sh /path/to/backup (empty data volumes only)' >&2; exit 1; }
backup=$(cd "$1" && pwd)
for file in pgdata.tar.gz chdata.tar.gz SHA256SUMS; do
  [ -f "$backup/$file" ] || { echo "Missing backup file: $file" >&2; exit 1; }
done
docker image inspect alpine:3.22 >/dev/null 2>&1 || docker pull alpine:3.22 >/dev/null
docker run --rm -v "$backup:/backup:ro" -w /backup alpine:3.22 sha256sum -c SHA256SUMS
# Container creation allocates the same named volumes that the application will use.
docker compose create postgres clickhouse >/dev/null
volume_of() {
  cid=$(docker compose ps -aq "$1")
  running=$(docker inspect --format '{{.State.Running}}' "$cid")
  [ "$running" = false ] || { echo "$1 is running; stop this installation first" >&2; return 1; }
  docker inspect --format "{{range .Mounts}}{{if eq .Destination \"$2\"}}{{.Name}}{{end}}{{end}}" "$cid"
}
pgvolume=$(volume_of postgres /var/lib/postgresql/data)
chvolume=$(volume_of clickhouse /var/lib/clickhouse)
[ -n "$pgvolume" ] && [ -n "$chvolume" ]
# Check both volumes before writing either; never overwrite an existing installation.
for volume in "$pgvolume" "$chvolume"; do
  docker run --rm -v "$volume:/data:ro" alpine:3.22 \
    sh -c '[ -z "$(ls -A /data)" ]' || { echo 'Restore requires empty volumes; use a new Compose project.' >&2; exit 1; }
done
for database in pgdata chdata; do
  case "$database" in pgdata) volume=$pgvolume ;; chdata) volume=$chvolume ;; esac
  docker run --rm -v "$volume:/data" -v "$backup:/backup:ro" alpine:3.22 \
    tar -C /data -xzf "/backup/$database.tar.gz"
done
echo 'Data restored. Check .env addresses and use the backed-up database passwords, then run docker compose up -d.'
