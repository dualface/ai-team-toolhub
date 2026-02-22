# Local Smoke Test Runbook

A practical troubleshooting guide for running ToolHub's smoke tests locally.

## Prerequisites

Before running smoke tests, ensure you have:

- Docker and Docker Compose installed and running
- `.env` file populated (copy from `.env.example` and edit values)
- GitHub App private key at `./secrets/github_app_private_key.pem`
- At least one valid repo in `REPO_ALLOWLIST` that the GitHub App has access to
- `python3` available (used by smoke script for JSON assertions)

See the main [README.md](../README.md) for GitHub App setup details.

## Quick Start

```bash
# First run (builds and starts services):
./scripts/smoke_phase_a5_b.sh

# Against already-running services:
SMOKE_AUTO_START=0 ./scripts/smoke_phase_a5_b.sh

# Force a specific PR number for PR read checks:
SMOKE_PR_NUMBER=42 ./scripts/smoke_phase_a5_b.sh
```

## Common Failures and Fixes

**Problem: `permission denied` for `/run/secrets/github_app_private_key.pem`**

- Cause: Docker secrets file is not readable by the container process
- Fix: `chmod 0644 secrets/github_app_private_key.pem`

**Problem: `x509: failed to parse private key`**

- Cause: Key file is not a valid RSA private key (PEM format)
- Fix: Generate with `openssl genrsa -out secrets/github_app_private_key.pem 2048`. ToolHub supports both PKCS#1 and PKCS#8 RSA keys.

**Problem: `toolhub did not become healthy in time`**

- Cause: Service failed to start or is taking too long. Could be Docker pull failure, port conflict, DB connection issue.
- Fix:
  1. Check logs: `docker compose logs toolhub postgres`
  2. Verify port availability: `lsof -i :8080` (or whatever `TOOLHUB_HTTP_PORT` is)
  3. Verify DB credentials match between `.env` and what postgres container expects
  4. If behind a proxy, set `TOOLHUB_HTTPS_PROXY` and `TOOLHUB_HTTP_PROXY` in `.env`

**Problem: HTTP `500` on dry-run issue calls**

- Cause: Artifact directory not writable by container user
- Fix: `mkdir -p data/artifacts && chmod 0777 data/artifacts`

**Problem: `curl: (56) Recv failure` right after docker compose up**

- Cause: Service is starting but not ready yet
- Fix: The smoke script waits for `/healthz`, but if it fails, manually verify: `curl -sS http://localhost:8080/healthz`

**Problem: `REPO_ALLOWLIST must contain at least one repo in .env`**

- Cause: `.env` not properly configured
- Fix: Edit `.env` and set `REPO_ALLOWLIST=owner/repo` to a repo your GitHub App can access

**Problem: MCP endpoint checks fail**

- Cause: MCP port not forwarded or different from expected
- Fix: Verify `TOOLHUB_MCP_PORT` in `.env` matches what Docker exposes. Default is 8090.

**Problem: PR read checks skipped**

- Cause: No open or closed PRs found in the first allowlisted repo
- Fix: Either create a PR in the repo, or use `SMOKE_PR_NUMBER=<existing-pr-number>`

**Problem: QA checks fail with `qa_command_empty` or `qa_command_not_allowed`**

- Cause: `QA_TEST_CMD` or `QA_LINT_CMD` not configured, or the executable is not in `QA_ALLOWED_EXECUTABLES`
- Fix: Check `.env` has valid `QA_TEST_CMD`, `QA_LINT_CMD`, and `QA_ALLOWED_EXECUTABLES` values

**Problem: Docker module download timeouts (Go)**

- Cause: Container can't reach module proxy (corporate firewall, proxy required)
- Fix: Set `TOOLHUB_HTTPS_PROXY` and `TOOLHUB_HTTP_PROXY` in `.env` to your proxy (use `host.docker.internal` instead of `localhost` for host-side proxies)

## Environment Variables Reference

| Variable | Purpose | Default |
|----------|---------|---------|
| `SMOKE_AUTO_START` | Build/start Docker stack before tests | `1` |
| `SMOKE_PR_NUMBER` | Force specific PR number for PR checks | auto-detect |
| `TOOLHUB_HTTP_PORT` | HTTP API port | `8080` |
| `TOOLHUB_MCP_PORT` | MCP transport port | `8090` |
| `REPO_ALLOWLIST` | Comma-separated allowed repos | (required) |
| `TOOL_ALLOWLIST` | Comma-separated allowed tools | (required) |

## Full Reset

When things are stuck or you want a clean slate:

```bash
docker compose down -v
rm -rf data/artifacts
# Re-populate .env from .env.example
cp .env.example .env
# Edit .env with your values
./scripts/smoke_phase_a5_b.sh
```
