# ToolHub (HTTP + MCP) — Phase A (PRD → GitHub Issues)

ToolHub is a production-oriented tool gateway for AI orchestration. It exposes a controlled set of tools via **HTTP** and **MCP**, records **auditable evidence** of every tool call into **PostgreSQL**, and persists artifacts (request/response/logs) to a local filesystem **Artifact Store**.

**Phase A scope (current):**
- ✅ Create a `run`
- ✅ Create single GitHub Issue
- ✅ Batch create GitHub Issues
- ✅ Persist evidence to PostgreSQL (`runs`, `tool_calls`, `artifacts`)
- ✅ Persist artifacts to local directory (mounted volume)
- ✅ Enforce `REPO_ALLOWLIST` + `TOOL_ALLOWLIST`
- ✅ GitHub App auth (JWT → installation token)

> This repo is intended to be a long-lived infrastructure base. Later phases can add PR commenting, QA execution, sandboxed code changes, etc.

---

## Table of Contents

- [Architecture](#architecture)
- [Requirements](#requirements)
- [Quick Start](#quick-start)
- [Configuration](#configuration)
- [GitHub App Setup](#github-app-setup)
- [API Overview (HTTP)](#api-overview-http)
- [MCP Tools](#mcp-tools)
- [OpenAPI](#openapi)
- [Smoke Validation](#smoke-validation)
- [Artifact & Audit Model](#artifact--audit-model)
- [Backups](#backups)
- [Operational Notes](#operational-notes)
- [Security Model](#security-model)
- [Troubleshooting](#troubleshooting)
- [Development](#development)

---

## Architecture

At runtime ToolHub consists of:

- **ToolHub service**
  - HTTP API (e.g. `:8080`)
  - MCP server (e.g. `:8090`)
  - Core modules: policy (allowlists), GitHub client (GitHub App), audit store (Postgres), artifact store (filesystem)

- **PostgreSQL**
  - System-of-record for runs/tool calls/artifacts
  - Enables replayability and audit trails

- **Artifact Store**
  - Local directory mounted into container
  - Stores evidence such as request/response JSON, generated task lists, logs

```
Agents/Orchestrator
   |  (HTTP or MCP)
   v
ToolHub (Core: policy+audit+artifact)
   |--> Postgres (runs/tool_calls/artifacts)
   `--> Local Artifact Store (files)
   `--> GitHub API (via GitHub App installation token)
```

---

## Requirements

- Docker + docker-compose v2
- Go **1.25.x** (required for local build and development)
- A GitHub account (personal)
- A GitHub App installed on selected repositories
- PostgreSQL (provided via docker-compose)

### Go and dependency policy

- `toolhub/go.mod` uses `go 1.25`.
- All Go dependencies should track the latest stable versions compatible with Go 1.25.
- When upgrading dependencies, run updates from the `toolhub/` module and commit the matching `go.sum` changes.

---

## Quick Start

1) Copy env file:

```bash
cp .env.example .env
```

2) Create a GitHub App and download its private key:
- Place the private key at:

```
./secrets/github_app_private_key.pem
```

> Do **not** commit the `.pem` file.

3) Edit `.env` values (ports, database, GitHub App IDs, allowlists).

4) Start services:

```bash
docker compose up -d --build
docker compose ps
```

5) Verify health and version metadata:

```bash
curl -s http://localhost:${TOOLHUB_HTTP_PORT}/healthz
curl -s http://localhost:${TOOLHUB_HTTP_PORT}/version
```

---

## Configuration

ToolHub reads configuration via environment variables (see `.env.example`).

### Core

- `TOOLHUB_HTTP_LISTEN`  
  Example: `0.0.0.0:8080`

- `TOOLHUB_MCP_LISTEN`  
  Example: `0.0.0.0:8090`

- `BATCH_MODE`
  Batch issue creation behavior:
  - `partial` (default): continue and return per-item mixed outcomes
  - `strict`: stop on first GitHub error (no rollback for already-created items)

- `DATABASE_URL`  
  Example:
  `postgres://toolhub:password@postgres:5432/toolhub?sslmode=disable`

- `ARTIFACTS_DIR`  
  Example: `/var/lib/toolhub/artifacts`  
  (mapped from `./data/artifacts` in docker-compose)

### GitHub App Auth

- `GITHUB_APP_ID`  
  The numeric GitHub App ID

- `GITHUB_INSTALLATION_ID`  
  The numeric installation ID for the repo set. Optional only when your App has exactly one installation.

- `GITHUB_PRIVATE_KEY_PATH`  
  Path inside container, e.g. `/run/secrets/github_app_private_key.pem`

### Safety Controls (Strongly Recommended)

- `REPO_ALLOWLIST`  
  Comma-separated `owner/repo` values.
  Example: `yourname/zos-server-go`

- `TOOL_ALLOWLIST`  
  Comma-separated tool names allowed in this deployment phase.
  Example: `github.issues.create,github.issues.batch_create,github.pr.comment.create,github.pr.get,github.pr.files.list,qa.test,qa.lint,runs.create`

- `QA_WORKDIR`
  Working directory used by `qa.test` and `qa.lint`.

- `QA_TEST_CMD`
  Server-configured command executed by `qa.test`.

- `QA_LINT_CMD`
  Server-configured command executed by `qa.lint`.

- `QA_TIMEOUT_SECONDS`
  Timeout for QA command execution.

> ToolHub **must enforce** allowlists server-side. Do not rely on client discipline.

---

## GitHub App Setup

### Why GitHub App (instead of PAT)

- Short-lived installation tokens
- Fine-grained permissions
- Can be installed only on selected repositories
- Safer for automation

### Steps (Personal Account)

1) GitHub → **Settings** → **Developer settings** → **GitHub Apps** → **New GitHub App**
   - **GitHub App name**: any unique name (e.g. `toolhub-issues-bot`)
   - **Homepage URL**: your repo URL or any valid URL (required by GitHub UI)
   - **Webhook**: disable for Phase A (not required)
   - **Where can this GitHub App be installed?**: **Only on this account**
2) Permissions:
   - **Metadata**: Read-only
   - **Issues**: Read & Write
   - (Optional) **Pull requests**: Read-only (not required for Phase A)
   - Keep all other permissions at **No access** unless you explicitly need them
3) Install App:
   - Choose **Selected repositories**
   - Install only on repos you allow ToolHub to touch
4) Generate private key:
   - Download `.pem`
   - Place it at `./secrets/github_app_private_key.pem`
   - Ensure file exists before startup:

```bash
ls -l ./secrets/github_app_private_key.pem
```

5) Put IDs into `.env`:
   - `GITHUB_APP_ID=<App ID>`
   - `GITHUB_INSTALLATION_ID=<Installation ID>` (optional only if there is exactly one installation)
   - `GITHUB_PRIVATE_KEY_PATH=/run/secrets/github_app_private_key.pem`

### Getting IDs

- `GITHUB_APP_ID`: shown on the App settings page
- `GITHUB_INSTALLATION_ID`: visible after installation; you can also obtain it via GitHub API.
  (Many implementations provide a helper script/endpoint—if your ToolHub has one, document it here.)

If `GITHUB_INSTALLATION_ID` is not set, ToolHub attempts auto-discovery from GitHub App installations.
Auto-discovery succeeds only when exactly one installation exists; otherwise startup requests explicit configuration.

Example (using GitHub CLI):

```bash
gh api /repos/<owner>/<repo>/installation --jq .id
```

### Common GitHub App Errors (Quick Checklist)

- `401 Unauthorized`
  - `GITHUB_APP_ID` / `GITHUB_INSTALLATION_ID` mismatch
  - Private key path wrong or file unreadable in container
  - System time drift too large (JWT `iat/exp` invalid)

- `403 Forbidden`
  - App missing `Issues: Read & Write` permission
  - App installed, but not on the target repository
  - Repository is outside your `REPO_ALLOWLIST`

- `404 Not Found` (GitHub API)
  - `owner/repo` is wrong
  - App installation does not include this repository

- `422 Unprocessable Entity`
  - Issue `title` is empty or invalid
  - Label names do not exist in repo (depending on repo settings)

Useful checks:

```bash
# Verify private key mounted in container
docker compose exec toolhub ls -l /run/secrets/

# Verify app installation id for target repo
gh api /repos/<owner>/<repo>/installation --jq .id
```

---

## API Overview (HTTP)

> Endpoint paths may vary by implementation. Adjust the examples to match your server routes.

Base URL:

```
http://localhost:${TOOLHUB_HTTP_PORT}
```

### 0) Service Version

**GET** `/version`

Response:

```json
{
  "version": "",
  "git_commit": "",
  "build_time": ""
}
```

Notes:
- Values are build metadata and may be empty when not injected at build time.

### 1) Create Run

**POST** `/api/v1/runs`

Request:

```json
{
  "repo": "yourname/zos-server-go",
  "purpose": "prd_to_issues",
  "inputs": {
    "prd_ref": "artifact://prd.md"
  }
}
```

Response:

```json
{ "ok": true, "run_id": "run_01J..." }
```

### 2) Create Single Issue

**POST** `/api/v1/runs/{runID}/issues`

Request:

```json
{
  "title": "T-001: Add endpoint X",
  "body": "## Goal\n...\n\n## DoD\n- ...\n\n## Test Plan\n- ...\n\n## Rollback\n- ...",
  "labels": ["agent", "backend"],
  "dry_run": false
}
```

Response (example envelope):

```json
{
  "ok": true,
  "meta": {
    "run_id": "run_01J...",
    "tool_call_id": "tc_01J...",
    "evidence_hash": "...",
    "dry_run": false
  },
  "result": {
    "number": 123,
    "html_url": "https://github.com/yourname/zos-server-go/issues/123"
  }
}
```

Notes:
- ToolHub applies request validation (title/body/labels limits).
- ToolHub enforces idempotent behavior for repeated equivalent requests within the same `run_id`.
- `dry_run=true` performs validation + audit but does not call GitHub write API.

### 3) Batch Create Issues

**POST** `/api/v1/runs/{runID}/issues/batch`

Request:

```json
{
  "dry_run": false,
  "issues": [
    { "title": "T-001: ...", "body": "...", "labels": ["agent"] },
    { "title": "T-002: ...", "body": "...", "labels": ["agent"] }
  ]
}
```

Response:

```json
{
  "ok": true,
  "meta": {
    "run_id": "run_01J...",
    "tool_call_id": "",
    "evidence_hash": "",
    "dry_run": false
  },
  "result": {
    "status": "partial",
    "mode": "partial",
    "total": 2,
    "processed": 2,
    "errors": 1,
    "replayed": 0,
    "created_fresh": 2,
    "results": [
      { "index": 0, "issue": { "number": 123, "html_url": "..." } },
      { "index": 1, "error": "..." }
    ]
  }
}
```

Notes:
- Maximum batch size is limited (see service constants).
- Response schema is standardized with: `mode`, `total`, `processed`, `errors`, `replayed`, `created_fresh`, `results`.
- Result is per-item and can contain mixed outcomes (`issue` or `error`).
- Replayed idempotent items are returned without creating duplicate issues.
- In `strict` mode, processing stops at first GitHub error and returns `stopped_at`/`failed_reason`.

### 4) PR Summary Comment

**POST** `/api/v1/runs/{runID}/prs/{prNumber}/comment`

Request:

```json
{
  "body": "Summary comment for this PR",
  "dry_run": false
}
```

Response:

```json
{
  "ok": true,
  "meta": {
    "run_id": "run_01J...",
    "tool_call_id": "tc_01J...",
    "evidence_hash": "...",
    "dry_run": false
  },
  "result": {
    "id": 123456,
    "html_url": "https://github.com/owner/repo/pull/12#issuecomment-..."
  }
}
```

### 5) Get PR Metadata

**GET** `/api/v1/runs/{runID}/prs/{prNumber}`

Response:

```json
{
  "ok": true,
  "meta": {
    "run_id": "run_01J...",
    "tool_call_id": "tc_01J...",
    "evidence_hash": "...",
    "dry_run": false
  },
  "result": {
    "number": 12,
    "title": "...",
    "state": "open",
    "html_url": "https://github.com/owner/repo/pull/12"
  }
}
```

### 6) List PR Files

**GET** `/api/v1/runs/{runID}/prs/{prNumber}/files`

Response:

```json
{
  "ok": true,
  "meta": {
    "run_id": "run_01J...",
    "tool_call_id": "tc_01J...",
    "evidence_hash": "...",
    "dry_run": false
  },
  "result": {
    "count": 2,
    "files": [
      { "filename": "README.md", "status": "modified", "changes": 10 }
    ]
  }
}
```

### 7) QA Test Command

**POST** `/api/v1/runs/{runID}/qa/test`

Request:

```json
{
  "dry_run": true
}
```

Response:

```json
{
  "ok": true,
  "meta": {
    "run_id": "run_01J...",
    "tool_call_id": "tc_01J...",
    "evidence_hash": "...",
    "dry_run": true
  },
  "result": {
    "status": "ok",
    "report": {
      "command": "go -C toolhub test ./...",
      "work_dir": "/workspace",
      "exit_code": 0,
      "duration_ms": 0,
      "stdout": "",
      "stderr": ""
    }
  }
}
```

### 8) QA Lint Command

**POST** `/api/v1/runs/{runID}/qa/lint`

Request/response shape is the same as `qa/test`.

---

## MCP Tools

ToolHub also exposes tools via MCP, typically mapping 1:1 to HTTP actions.

Current tool names:
- `runs_create`
- `github_issues_create`
- `github_issues_batch_create`
- `github_pr_comment_create`
- `github_pr_get`
- `github_pr_files_list`
- `qa_test`
- `qa_lint`

Detailed MCP schema: `docs/mcp-tools.md`.

## OpenAPI

- HTTP API contract file: `openapi.yaml`

> How to connect depends on your agent framework. If ToolHub runs MCP on `${TOOLHUB_MCP_PORT}`, your orchestrator should connect to `localhost:${TOOLHUB_MCP_PORT}`.

## Smoke Validation

Run an end-to-end smoke check for HTTP + MCP dry-run paths:

```bash
./scripts/smoke_phase_a5_b.sh
```

Optional flags:
- `SMOKE_AUTO_START=1` (default) will run `docker compose up -d --build` before checks.
- `SMOKE_AUTO_START=0` expects services already running.

The script validates:
- `GET /healthz` and `GET /version`
- `POST /api/v1/runs`
- HTTP dry-run for issues, batch issues, and PR summary comment
- HTTP PR metadata/file-list reads
- HTTP QA tool dry-run checks
- MCP tools list and dry-run/read calls for the same flows

---

## Artifact & Audit Model

### Artifact Store

Artifacts are persisted under `${ARTIFACTS_DIR}` (mounted volume). Typical contents:

- PRD input copies (optional)
- Generated task lists (`tasks.json`)
- Tool call request/response JSON
- GitHub API response snapshots
- Logs for debugging

Recommended directory convention:

```
${ARTIFACTS_DIR}/run_<run_id>/
  inputs/
  tool_calls/
  github/
  logs/
```

### PostgreSQL tables

Phase A baseline schema:

- `runs` — one workflow execution
- `tool_calls` — each tool invocation
- `artifacts` — files persisted to Artifact Store, with sha256

A GitHub write operation **must**:
- Persist request artifact
- Persist response artifact
- Record `evidence_hash`
- Record tool call row with status `ok/fail`

---

## Backups

This repo includes scripts to back up both PostgreSQL and local artifacts.

### Backup

```bash
./scripts/backup.sh
```

Outputs:
- `backups/pg_<timestamp>.sql`
- `backups/artifacts_<timestamp>.tar.gz`

### Restore

```bash
./scripts/restore.sh backups/pg_YYYYmmdd_HHMMSS.sql backups/artifacts_YYYYmmdd_HHMMSS.tar.gz
```

---

## Operational Notes

### Restart / Update

```bash
docker compose up -d --build
```

### Logs

```bash
docker compose logs -f toolhub
docker compose logs -f postgres
```

### Data persistence

- Postgres data: `./data/postgres`
- Artifacts: `./data/artifacts`

Do not commit `data/` to git.

---

## Security Model

### Safety invariants (Phase A)

- ToolHub only performs **Issues** operations (no code write operations).
- `REPO_ALLOWLIST` is enforced for every request.
- `TOOL_ALLOWLIST` is enforced for every tool call.
- GitHub App key is mounted read-only; never logged or persisted.
- Installation tokens are short-lived and must never be logged.

### Recommended GitHub branch protections

Even though Phase A does not push code:
- Protect `main/master`
- Require PR review + status checks (future-proofing)

---

## Troubleshooting

### Postgres not healthy
- Check container logs:

```bash
docker compose logs postgres
```

- Ensure `POSTGRES_PASSWORD` is set.
- If you changed schema init files after first boot, note that `docker-entrypoint-initdb.d` runs only on first initialization.
  For a clean re-init (dev only):
  - Stop services, delete `./data/postgres`, restart.

### ToolHub cannot create issues
- Verify allowlist includes the repo:
  - `REPO_ALLOWLIST=yourname/your-repo`
- Verify GitHub App is installed on the repo.
- Verify `GITHUB_APP_ID` and `GITHUB_INSTALLATION_ID` are correct.
- Verify private key file exists and is readable in container:
  - `docker compose exec toolhub ls -l /run/secrets/`

### 401/403 from GitHub API
- App permissions missing:
  - Ensure **Issues: Read & write**
- Installation scope:
  - Ensure App installed on the target repo (Selected repositories)

### Artifacts not written
- Verify `ARTIFACTS_DIR` mount exists:
  - `docker compose exec toolhub ls -l ${ARTIFACTS_DIR}`
- Ensure container user has write permissions to mounted directory (compose volume paths usually fine on Linux; on macOS consider Docker Desktop file sharing settings).

---

## Development

### CI checks

GitHub Actions workflow: `.github/workflows/ci.yml`

It runs:
- `go -C toolhub test ./...`
- `make build`
- `./scripts/smoke_phase_a5_b.sh` (dry-run over HTTP + MCP)

### Recommended repo layout (example)

```
cmd/toolhub/
internal/core/        # policy + audit + artifact store
internal/github/      # GitHub App auth, API client
internal/http/        # HTTP handlers/routes
internal/mcp/         # MCP adapter (thin mapping)
```

### Local run (without docker)
If supported by your implementation:

```bash
make build

export DATABASE_URL=...
export ARTIFACTS_DIR=...
export GITHUB_APP_ID=...
export GITHUB_INSTALLATION_ID=...
export GITHUB_PRIVATE_KEY_PATH=...
./bin/toolhub
```

Or run directly from the module directory:

```bash
export DATABASE_URL=...
export ARTIFACTS_DIR=...
export GITHUB_APP_ID=...
export GITHUB_INSTALLATION_ID=...
export GITHUB_PRIVATE_KEY_PATH=...
go -C toolhub run ./cmd/toolhub
```

---

## License

(Add your license here.)
