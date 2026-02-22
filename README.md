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
- Audit `tool_calls.status` records binary `ok`/`fail`; batch `partial` status is derived at the response layer

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
- `GET /metrics`
- `GET /api/v1/runs/{runID}`
- `POST /api/v1/runs/{runID}/approvals`
- `GET /api/v1/runs/{runID}/approvals`
- `GET /api/v1/runs/{runID}/approvals/{approvalID}`
- `POST /api/v1/runs/{runID}/approvals/{approvalID}/approve`
- `POST /api/v1/runs/{runID}/approvals/{approvalID}/reject`
- `POST /api/v1/runs/{runID}/code/patch`
- `POST /api/v1/runs/{runID}/code/branch-pr`
- `GET /api/v1/runs/{runID}/tool-calls`
- `GET /api/v1/runs/{runID}/artifacts`
- `GET /api/v1/runs/{runID}/artifacts/{artifactID}`
- `GET /api/v1/runs/{runID}/artifacts/{artifactID}/content`
- `POST /api/v1/runs/{runID}/issues`
- `POST /api/v1/runs/{runID}/issues/batch`
- `POST /api/v1/runs/{runID}/prs/{prNumber}/comment`
- `GET /api/v1/runs/{runID}/prs/{prNumber}`
- `GET /api/v1/runs/{runID}/prs/{prNumber}/files`
- `POST /api/v1/runs/{runID}/qa/test`
- `POST /api/v1/runs/{runID}/qa/lint`

See full schema in `openapi.yaml`.

Idempotency notes:

- `POST /api/v1/runs/{runID}/issues` and `POST /api/v1/runs/{runID}/prs/{prNumber}/comment`
  accept optional `Idempotency-Key` header.
- Replayed responses include `Idempotency-Replayed: true` and `meta.replayed=true`.

## MCP Tools

- `runs_create`
- `github_issues_create`
- `github_issues_batch_create`
- `github_pr_comment_create`
- `github_pr_get`
- `github_pr_files_list`
- `qa_test`
- `qa_lint`
- `code_patch_generate`
- `code_branch_pr_create`

See tool schemas in `docs/mcp-tools.md`.
Generated MCP tool snapshot is in `docs/mcp-tools.generated.md`.

Idempotency design notes for batch behavior are documented in `docs/C4_BATCH_IDEMPOTENCY.md`.

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

### Common CI smoke failures

- `permission denied` for `/run/secrets/github_app_private_key.pem`
  - Ensure the generated key file is world-readable in CI setup (`chmod 0644 secrets/github_app_private_key.pem`).
- `x509: failed to parse private key ... ParsePKCS8PrivateKey`
  - ToolHub now supports PKCS#1 and PKCS#8 RSA keys; keep using standard OpenSSL-generated RSA keys.
- HTTP `500` on dry-run issue calls during smoke
  - Dry-run still writes audit artifacts; ensure `data/artifacts` is writable by container user (CI currently sets `chmod 0777 data/artifacts`).
- `curl: (56) Recv failure` right after `docker compose up`
  - Service can be up before it is ready; smoke script now waits for `/healthz` before running checks.

## Configuration Notes

Key env vars:

- `REPO_ALLOWLIST`
- `TOOL_ALLOWLIST`
- `PATH_POLICY_FORBIDDEN_PREFIXES`, `PATH_POLICY_APPROVAL_PREFIXES`
- `GITHUB_APP_ID`, `GITHUB_INSTALLATION_ID`, `GITHUB_PRIVATE_KEY_PATH`
- `QA_WORKDIR`, `QA_TEST_CMD`, `QA_LINT_CMD`, `QA_TIMEOUT_SECONDS`
- `QA_MAX_OUTPUT_BYTES`, `QA_ALLOWED_EXECUTABLES`, `QA_MAX_CONCURRENCY`
- `QA_BACKEND`, `QA_SANDBOX_IMAGE`, `QA_SANDBOX_DOCKER_BIN`, `QA_SANDBOX_CONTAINER_WORKDIR`
- `CODE_WORKDIR`, `CODE_GIT_REMOTE`

QA safety notes:

- QA commands are server-configured (not user-supplied)
- shell operators (`;`, `|`, `&&`, `||`, redirection) are blocked
- executable must be in `QA_ALLOWED_EXECUTABLES`
- stdout/stderr are truncated using `QA_MAX_OUTPUT_BYTES`

Path policy notes:

- `PATH_POLICY_FORBIDDEN_PREFIXES`: paths that are always blocked by policy checks.
- `PATH_POLICY_APPROVAL_PREFIXES`: paths that require `scope=path_change` when creating manual approval requests.

Reference defaults are in `.env.example`.

## Operations

- Logs: `docker compose logs -f toolhub`
- Backup: `./scripts/backup.sh`
- Restore: `./scripts/restore.sh <pg_dump.sql> <artifacts.tar.gz>`

## Database Migrations

- ToolHub now applies embedded SQL migrations on startup.
- Migration files live in `toolhub/internal/db/migrations/` and run in filename order.
- `db/init/` remains available for first-time PostgreSQL bootstrap in Docker.
- D3 audit model extension details: `docs/D3_AUDIT_MODEL.md`.

## Sandbox PoC

- Internal QA sandbox PoC is documented in `docs/D1_SANDBOX_POC.md`.
- Current release keeps local runner as default and does not expose sandbox as external API yet.

## Security

- GitHub App only (no PAT)
- private key mounted read-only
- tokens and secrets must never be logged
- no repository code write tools are exposed

## License

MIT. See `LICENSE`.
