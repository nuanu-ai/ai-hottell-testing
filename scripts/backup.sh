#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
umask 077
stamp=$(date -u +%Y%m%dT%H%M%SZ)
destination=${1:-backups/$stamp}
mkdir -p "$destination"
destination=$(cd "$destination" && pwd)
[ ! -e "$destination/pgdata.tar.gz" ] || { echo 'Backup already exists; choose a new directory.' >&2; exit 1; }
volume_of() {
  cid=$(docker compose ps -aq "$1")
  [ -n "$cid" ] || { echo "Create the $1 container first" >&2; return 1; }
  docker inspect --format "{{range .Mounts}}{{if eq .Destination \"$2\"}}{{.Name}}{{end}}{{end}}" "$cid"
}
pgvolume=$(volume_of postgres /var/lib/postgresql/data)
chvolume=$(volume_of clickhouse /var/lib/clickhouse)
[ -n "$pgvolume" ] && [ -n "$chvolume" ]
docker image inspect alpine:3.22 >/dev/null 2>&1 || docker pull alpine:3.22 >/dev/null
resume() { docker compose up -d >/dev/null; }
trap resume EXIT
trap 'exit 1' HUP INT TERM
docker compose stop
for database in pgdata chdata; do
  case "$database" in pgdata) volume=$pgvolume ;; chdata) volume=$chvolume ;; esac
  docker run --rm -v "$volume:/data:ro" -v "$destination:/backup" alpine:3.22 \
    sh -c 'umask 077; tar -C /data -czf "/backup/$1.tar.gz" .' sh "$database"
done
cp .env "$destination/.env"
cp SOURCE.json "$destination/SOURCE.json"
docker run --rm -v "$destination:/backup" -w /backup alpine:3.22 \
  sh -c 'umask 077; sha256sum pgdata.tar.gz chdata.tar.gz .env SOURCE.json > SHA256SUMS'
echo "Backup saved in $destination; it includes credentials and must remain private."
