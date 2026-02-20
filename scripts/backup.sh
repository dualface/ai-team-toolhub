#!/usr/bin/env bash
set -euo pipefail

ts=$(date +"%Y%m%d_%H%M%S")
mkdir -p backups

if [ -f .env ]; then
  set -a
  source .env
  set +a
fi

docker compose exec -T postgres pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" > "backups/pg_${ts}.sql"
tar -czf "backups/artifacts_${ts}.tar.gz" -C data artifacts

echo "Backup complete:"
echo "  backups/pg_${ts}.sql"
echo "  backups/artifacts_${ts}.tar.gz"
