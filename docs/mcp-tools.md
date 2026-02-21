# MCP Tools

ToolHub MCP server exposes JSON-RPC tools over TCP (`TOOLHUB_MCP_LISTEN`).

## Tools

- `runs_create`
  - Input:
    - `repo` (string, required)
    - `purpose` (string, required)

- `github_issues_create`
  - Input:
    - `run_id` (string, required)
    - `title` (string, required)
    - `body` (string, required)
    - `labels` (string[], optional)
    - `dry_run` (boolean, optional)
  - Output:
    - `ok`
    - `meta.run_id`
    - `meta.tool_call_id`
    - `meta.evidence_hash`
    - `meta.dry_run`
    - `result`

- `github_issues_batch_create`
  - Input:
    - `run_id` (string, required)
    - `issues` (array, required)
      - `title` (string, required)
      - `body` (string, required)
      - `labels` (string[], optional)
    - `dry_run` (boolean, optional)
  - Output:
    - `ok`
    - `meta.run_id`
    - `meta.dry_run`
    - `result.status` (`ok|fail|partial`)
    - `result.results[]` per-item outcome

- `github_pr_comment_create`
  - Input:
    - `run_id` (string, required)
    - `pr_number` (integer, required)
    - `body` (string, required)
    - `dry_run` (boolean, optional)
  - Output:
    - `ok`
    - `meta.run_id`
    - `meta.tool_call_id`
    - `meta.evidence_hash`
    - `meta.dry_run`
    - `result`

## Mapping to internal policy tool names

- `github_issues_create` -> `github.issues.create`
- `github_issues_batch_create` -> `github.issues.batch_create`
- `github_pr_comment_create` -> `github.pr.comment.create`

These internal names are what `TOOL_ALLOWLIST` enforces server-side.
