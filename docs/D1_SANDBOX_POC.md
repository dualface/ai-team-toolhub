# D1 Sandbox PoC

This PoC introduces an internal sandbox execution runner for QA commands.

## Scope

- Adds `qa.SandboxRunner` in `toolhub/internal/qa/sandbox_runner.go`.
- Executes commands through Docker with restricted defaults:
  - `--network none`
  - `--cpus 1`
  - `--memory 512m`
  - bind mount host workdir to container workdir
- Supports dry-run, timeout, output truncation, and structured report output.

## Non-goals

- No external API changes (HTTP/MCP contract unchanged).
- Not wired as the default QA backend yet.
- No approval gate integration yet.

## Next Step

D2 will abstract QA backend selection (`local` vs `sandbox`) and wire this runner behind configuration.
