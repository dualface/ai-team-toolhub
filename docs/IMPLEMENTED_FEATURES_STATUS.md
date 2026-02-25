# Implemented Features Status (P0-P2)

Last updated: 2026-02-25

This document summarizes features that are already implemented in ToolHub and provides concrete evidence paths for verification.

## 1. Platform and Contract Baseline (Phase A/A.5/B/C)

- Runs API and MCP entrypoint are implemented.
  - Evidence: `toolhub/internal/http/server.go`, `toolhub/internal/mcp/server.go`
- GitHub issue tools are implemented for single create and batch create.
  - Evidence: `toolhub/internal/http/server.go`, `toolhub/internal/mcp/server.go`
- GitHub PR tools are implemented for comment create, PR get, and PR files list.
  - Evidence: `toolhub/internal/http/server.go`, `toolhub/internal/mcp/server.go`
- QA tools are implemented (`qa.test`, `qa.lint`) with server-controlled command execution.
  - Evidence: `toolhub/internal/qa/runner.go`, `toolhub/internal/http/server.go`, `toolhub/internal/mcp/server.go`
- `/version` exposes contract metadata and build metadata.
  - Evidence: `toolhub/internal/http/server.go`, `toolhub/internal/core/contract.go`, `toolhub/cmd/toolhub/main.go`

## 2. Controlled Write Workflows (Phase D)

- D.1: `code.patch.generate` implemented (artifact generation only, no direct repo write).
  - Evidence: `toolhub/internal/http/server.go`, `toolhub/internal/mcp/server.go`, `toolhub/internal/codeops/runner.go`
- D.2: `code.branch_pr.create` implemented with approval-gated branch/commit/push/PR flow.
  - Evidence: `toolhub/internal/http/server.go`, `toolhub/internal/mcp/server.go`, `toolhub/internal/codeops/runner.go`
- D.3: `code.repair_loop` implemented with QA retry + rollback flow.
  - Evidence: `toolhub/internal/http/server.go`, `toolhub/internal/mcp/server.go`, `toolhub/internal/codeops/runner.go`

## 3. Policy and Security Hardening

- Enforcement of allowlists is active (`REPO_ALLOWLIST`, `TOOL_ALLOWLIST`).
  - Evidence: `toolhub/internal/core/policy.go`, `toolhub/internal/http/server.go`, `toolhub/internal/mcp/server.go`
- Path policy structured violations are implemented via `PolicyViolation` and stable violation codes.
  - Evidence: `toolhub/internal/core/policy_violation.go`, `toolhub/internal/core/policy.go`
- Built-in hardened forbidden path prefixes are enforced and non-removable by env config.
  - Evidence: `toolhub/internal/core/policy.go`, `docs/PATH_POLICY.md`
- GitHub App-only auth model is documented and kept as security baseline.
  - Evidence: `README.md`, `AGENTS.md`

## 4. Audit and Evidence Integrity

- Canonical execution chain is preserved: `policy check -> tool execution -> artifact write -> audit DB write`.
  - Evidence: `AGENTS.md`, `toolhub/internal/http/server.go`, `toolhub/internal/mcp/server.go`, `toolhub/internal/core/audit.go`
- Artifacts and audit records are persisted for tool calls with evidence hashing.
  - Evidence: `toolhub/internal/core/artifact.go`, `toolhub/internal/core/audit.go`
- Audit failure boundary behavior is documented and covered by tests.
  - Evidence: `docs/AUDIT_FAILURE_BOUNDARIES.md`, `toolhub/internal/core/audit_failure_test.go`

## 5. Observability and Reliability Improvements

- Repair-loop observability metrics are implemented (iteration, QA result, completion, rollback).
  - Evidence: `toolhub/internal/telemetry/metrics.go`, `toolhub/internal/http/server.go`, `toolhub/internal/mcp/server.go`
- QA failure category mapping is implemented and exposed in repair-loop outputs/audit data.
  - Evidence: `toolhub/internal/http/server.go`, `toolhub/internal/mcp/server.go`
- Local smoke troubleshooting is documented.
  - Evidence: `docs/LOCAL_SMOKE_RUNBOOK.md`

## 6. Environment Profiles and Configuration Governance (P2)

- `TOOLHUB_PROFILE` is implemented (`dev`, `staging`, `prod`) with safe default profiles.
  - Evidence: `toolhub/internal/core/profile.go`, `toolhub/internal/core/profile_test.go`, `toolhub/cmd/toolhub/main.go`
- Explicit environment variables override profile defaults.
  - Evidence: `toolhub/cmd/toolhub/main.go`, `toolhub/internal/core/profile.go`
- `.env.example` includes profile and listen-related env vars.
  - Evidence: `.env.example`

## 7. Documentation Drift Guardrails

- Doc drift tests validate env vars, HTTP endpoints, and MCP tools against docs.
  - Evidence: `toolhub/internal/core/doc_drift_test.go`
- CI includes doc drift checks in addition to tests/build/smoke.
  - Evidence: `.github/workflows/ci.yml`
- Standalone script exists for drift checking.
  - Evidence: `scripts/check-doc-drift.sh`

## 8. Verification Checklist

Use this checklist to re-verify the implementation status:

1. Run tests: `go -C toolhub test ./...`
2. Build binary: `make build`
3. Validate OpenAPI: `npx @redocly/cli lint openapi.yaml --skip-rule security-defined --skip-rule info-license --skip-rule info-contact --skip-rule no-server-example.com`
4. Run smoke: `./scripts/smoke_phase_a5_b.sh` (or `SMOKE_AUTO_START=0 ./scripts/smoke_phase_a5_b.sh`)
