# Path Policy

ToolHub enforces path policy for D-flow write operations before any code tool executes.

## Built-in forbidden prefixes

These prefixes are always blocked and cannot be removed by environment configuration:

- `.github/`: protects CI/CD, workflow, and repository automation controls.
- `.git/`: blocks direct VCS metadata mutation.
- `secrets/`: protects local key material and secret mounts used by runtime tooling.
- `.env`: blocks environment files and variants like `.env.local` and `.env.production`.

## Environment configuration behavior

`PATH_POLICY_FORBIDDEN_PREFIXES` extends the built-in list; it does not replace it.

- Built-in values are always included.
- Environment values are normalized and merged.
- Duplicate prefixes are removed.

`PATH_POLICY_APPROVAL_PREFIXES` is separate and controls approval requirements only.

- Matching these prefixes does not hard-block a path.
- Matching these prefixes requires `scope=path_change` for manual approval creation.

## Structured policy violations

Path checks return structured `PolicyViolation` errors with these codes:

- `path_policy_forbidden`: path matched a forbidden prefix.
- `path_policy_approval_required`: reserved code for approval-gated path workflows.
- `path_policy_traversal`: traversal or root-escape style path detected.
- `path_policy_empty`: empty path input.

HTTP and MCP handlers surface these codes in `ToolEnvelope.error.code` for policy-blocked D-flow requests.
