#!/usr/bin/env bash
set -euo pipefail

if [ $# -ne 2 ]; then
  echo "Usage: $0 <pg_dump.sql> <artifacts.tar.gz>"
  exit 1
fi

pg_sql="$1"
art_tgz="$2"

if [ -f .env ]; then
  set -a
  source .env
  set +a
fi

docker compose exec -T postgres psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" < "$pg_sql"
tar -xzf "$art_tgz" -C data

echo "Restore complete."
