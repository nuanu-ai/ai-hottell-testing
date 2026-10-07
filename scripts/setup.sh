#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
umask 077
if [ -e .env ] || [ -L .env ]; then
  echo '.env already exists; edit it or reuse it. Nothing was overwritten.'
  exit 0
fi
command -v openssl >/dev/null 2>&1 || { echo 'openssl is required' >&2; exit 1; }
random_hex() { openssl rand -hex 24; }
staging=$(mktemp .env.setup.XXXXXX)
trap 'rm -f "$staging"' EXIT HUP INT TERM
sed \
  -e "s/^HT_POSTGRES_PASSWORD=$/HT_POSTGRES_PASSWORD=$(random_hex)/" \
  -e "s/^HT_CLICKHOUSE_PASSWORD=$/HT_CLICKHOUSE_PASSWORD=$(random_hex)/" \
  -e "s/^HT_CLICKHOUSE_READER_PASSWORD=$/HT_CLICKHOUSE_READER_PASSWORD=$(random_hex)/" \
  .env.example > "$staging"
# Link instead of overwriting: two simultaneous setup runs must keep the first secrets.
ln "$staging" .env || { echo '.env was created by another process; it was left as is.' >&2; exit 1; }
echo 'Created .env with unique database passwords. Set HT_BOOTSTRAP_EMAIL, HT_BOOTSTRAP_NAME and HT_BOOTSTRAP_PASSWORD before creating the first user.'
