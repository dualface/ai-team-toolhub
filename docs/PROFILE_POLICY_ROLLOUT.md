# Profile and Path Policy Rollout Playbook

This playbook describes a safe process for changing environment profiles and path-policy settings in ToolHub.

## Scope

- `TOOLHUB_PROFILE` (`dev`, `staging`, `prod`)
- Path-policy env vars:
  - `PATH_POLICY_FORBIDDEN_PREFIXES`
  - `PATH_POLICY_APPROVAL_PREFIXES`
- Optional repair-loop cap override:
  - `REPAIR_MAX_ITERATIONS` (allowed range: `1..10`)

Built-in hardened forbidden prefixes (`.github/`, `.git/`, `secrets/`, `.env`) are always enforced and cannot be removed.

## Pre-Change Checks

1. Confirm target behavior and blast radius.
   - Which repos and tools are affected?
   - Which paths are expected to be newly forbidden or approval-gated?
2. Validate config inputs locally.
   - Ensure profile is one of `dev|staging|prod`.
   - Ensure `REPAIR_MAX_ITERATIONS` is within `1..10` if set.
3. Run baseline verification before making changes:

```bash
go -C toolhub test ./...
make build
npx @redocly/cli lint openapi.yaml --skip-rule security-defined --skip-rule info-license --skip-rule info-contact --skip-rule no-server-example.com
```

## Rollout Procedure

1. Prepare env changes in `.env` (or deployment env source).
2. Restart ToolHub service.
3. Confirm startup logs include:
   - `profile loaded`
   - `effective config`
4. Validate policy behavior with representative dry-run requests:
   - one expected allowed path
   - one expected forbidden path
   - one expected approval-required path
5. Validate repair-loop cap behavior:
   - request with `max_iterations` at configured limit
   - request with `max_iterations` above configured limit (expect rejection)

## Smoke Verification

Run smoke after rollout:

```bash
./scripts/smoke_phase_a5_b.sh
```

If services are already running:

```bash
SMOKE_AUTO_START=0 ./scripts/smoke_phase_a5_b.sh
```

For local troubleshooting, see `docs/LOCAL_SMOKE_RUNBOOK.md`.

## Rollback Procedure

1. Revert the env changes to previous known-good values.
2. Restart ToolHub.
3. Re-run the smoke script.
4. Confirm policy behavior returns to expected baseline with dry-run checks.

## Recommended Profile Baselines

- `dev`
  - broadest iteration cap (`RepairMaxIterations=3` default)
  - minimal approval prefixes
- `staging`
  - production-like policy with moderate constraints
  - same default iteration cap as `dev` unless overridden
- `prod`
  - strictest defaults
  - lower repair-loop cap (`RepairMaxIterations=2` default)

Always prefer profile defaults first, then add explicit env overrides only when required.
