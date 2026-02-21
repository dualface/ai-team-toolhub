# ToolHub

ToolHub is a controlled tool gateway for AI workflows.
It exposes tools via HTTP and MCP, enforces server-side policy, and stores full evidence for every tool call.

## Current Status

- Phase A complete: run + issue create + batch issue create
- Phase A.5 complete: `/version`, structured logs, dry-run, OpenAPI/MCP docs, CI smoke
- Phase B complete: PR comment, PR read, PR files list
- Phase C baseline complete: `qa.test` and `qa.lint` (configured commands, audited, dry-run)
- Phase D (code change automation) intentionally not implemented

## Core Guarantees

- Server-enforced `REPO_ALLOWLIST` and `TOOL_ALLOWLIST`
- Every tool call writes request/response artifacts and `evidence_hash`
- PostgreSQL is source of truth (`runs`, `tool_calls`, `artifacts`)
- Local artifact store keeps auditable payload snapshots

## Quick Start

1. Copy env:

```bash
cp .env.example .env
```

2. Put GitHub App private key at `./secrets/github_app_private_key.pem`
3. Fill `.env` values (allowlists, GitHub app IDs, DB password)
4. Start:

```bash
docker compose up -d --build
```

5. Verify:

```bash
curl -s http://localhost:${TOOLHUB_HTTP_PORT}/healthz
curl -s http://localhost:${TOOLHUB_HTTP_PORT}/version
```

## Main HTTP Endpoints

- `POST /api/v1/runs`
- `GET /api/v1/runs/{runID}`
- `POST /api/v1/runs/{runID}/issues`
- `POST /api/v1/runs/{runID}/issues/batch`
- `POST /api/v1/runs/{runID}/prs/{prNumber}/comment`
- `GET /api/v1/runs/{runID}/prs/{prNumber}`
- `GET /api/v1/runs/{runID}/prs/{prNumber}/files`
- `POST /api/v1/runs/{runID}/qa/test`
- `POST /api/v1/runs/{runID}/qa/lint`

See full schema in `openapi.yaml`.

## MCP Tools

- `runs_create`
- `github_issues_create`
- `github_issues_batch_create`
- `github_pr_comment_create`
- `github_pr_get`
- `github_pr_files_list`
- `qa_test`
- `qa_lint`

See tool schemas in `docs/mcp-tools.md`.

## Smoke Test

Run end-to-end dry-run checks for HTTP + MCP:

```bash
./scripts/smoke_phase_a5_b.sh
```

Useful flags:

- `SMOKE_AUTO_START=1` (default): rebuild/start stack first
- `SMOKE_AUTO_START=0`: run against already-running services
- `SMOKE_PR_NUMBER=<num>`: force a specific PR for PR read checks

## CI

Workflow: `.github/workflows/ci.yml`

It runs:

- `go -C toolhub test ./...`
- `make build`
- `./scripts/smoke_phase_a5_b.sh`

## Configuration Notes

Key env vars:

- `REPO_ALLOWLIST`
- `TOOL_ALLOWLIST`
- `GITHUB_APP_ID`, `GITHUB_INSTALLATION_ID`, `GITHUB_PRIVATE_KEY_PATH`
- `QA_WORKDIR`, `QA_TEST_CMD`, `QA_LINT_CMD`, `QA_TIMEOUT_SECONDS`
- `QA_MAX_OUTPUT_BYTES`, `QA_ALLOWED_EXECUTABLES`

QA safety notes:

- QA commands are server-configured (not user-supplied)
- shell operators (`;`, `|`, `&&`, `||`, redirection) are blocked
- executable must be in `QA_ALLOWED_EXECUTABLES`
- stdout/stderr are truncated using `QA_MAX_OUTPUT_BYTES`

Reference defaults are in `.env.example`.

## Operations

- Logs: `docker compose logs -f toolhub`
- Backup: `./scripts/backup.sh`
- Restore: `./scripts/restore.sh <pg_dump.sql> <artifacts.tar.gz>`

## Security

- GitHub App only (no PAT)
- private key mounted read-only
- tokens and secrets must never be logged
- no repository code write tools are exposed

## License

MIT. See `LICENSE`.
