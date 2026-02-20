# ToolHub Deploy Template (Phase A: PRD -> Issues)

This template provides a production-friendly baseline deployment using **docker-compose** with:
- PostgreSQL (audit/state)
- ToolHub service (HTTP + MCP)
- Local filesystem artifacts store (mounted volume)

## Quick start

1) Copy `.env.example` to `.env` and fill values.
2) Place your GitHub App private key at `./secrets/github_app_private_key.pem` (do not commit).
3) Start services:
   - `docker compose up -d --build`
4) Verify:
   - Postgres healthy: `docker compose ps`
   - ToolHub health endpoint (implement): `curl http://localhost:$TOOLHUB_HTTP_PORT/healthz`

## Backups
- `./scripts/backup.sh`
- `./scripts/restore.sh <pg_dump.sql> <artifacts.tar.gz>`
