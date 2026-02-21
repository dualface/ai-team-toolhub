# AGENTS Guide (ToolHub)

This file defines how AI coding agents should work in this repository.

## Mission

Maintain ToolHub as a controlled, auditable tool gateway.

Priorities:

1. Policy enforcement (`REPO_ALLOWLIST`, `TOOL_ALLOWLIST`)
2. Audit evidence integrity (`tool_calls`, artifacts, `evidence_hash`)
3. HTTP/MCP contract consistency
4. Small, reversible changes

## Architecture Rules

- Tool flow must remain: `policy check -> tool execution -> artifact write -> audit DB write`.
- Any GitHub write/read capability must go through existing audit pipeline.
- Avoid bypass paths that call GitHub client directly from ad-hoc code.
- Keep HTTP and MCP behavior aligned for equivalent tools.

## Security Rules

- Never log tokens, private keys, or auth headers.
- Never introduce PAT-based auth; GitHub App is required.
- Never relax allowlist enforcement for convenience.
- For QA tools, do not accept arbitrary user-provided shell commands.
  - Commands are server-configured env vars.
  - Enforce timeout and controlled workdir.

## Change Scope Rules

- Prefer one task per PR/commit group.
- Do not mix refactors with feature work unless required.
- Keep compatibility impact explicit in docs when API shape changes.
- Update docs whenever you change:
  - HTTP endpoints (`openapi.yaml`, `README.md`)
  - MCP tools (`docs/mcp-tools.md`, `README.md`)
  - env vars (`.env.example`, `README.md`)

## Testing and Verification

Before finalizing changes, run:

```bash
go -C toolhub test ./...
make build
./scripts/smoke_phase_a5_b.sh
```

If smoke requires existing services, use:

```bash
SMOKE_AUTO_START=0 ./scripts/smoke_phase_a5_b.sh
```

For DB/audit integration checks, provide `TOOLHUB_TEST_DATABASE_URL` when needed.

## Coding Conventions

- Keep Go code simple, explicit, and idiomatic.
- Reuse existing patterns in:
  - `toolhub/internal/http/server.go`
  - `toolhub/internal/mcp/server.go`
  - `toolhub/internal/core/audit.go`
  - `toolhub/internal/core/policy.go`
- Prefer additive changes over broad rewrites.

## Operational Boundaries

- Phase D (automatic code modification workflows) is not implemented.
- Do not add arbitrary shell execution endpoints.
- Do not add direct merge/push automation beyond current scope.
