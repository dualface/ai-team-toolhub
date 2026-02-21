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

- `github_pr_get`
  - Input:
    - `run_id` (string, required)
    - `pr_number` (integer, required)
  - Output:
    - `ok`
    - `meta.run_id`
    - `meta.tool_call_id`
    - `meta.evidence_hash`
    - `result` (PR metadata)

- `github_pr_files_list`
  - Input:
    - `run_id` (string, required)
    - `pr_number` (integer, required)
  - Output:
    - `ok`
    - `meta.run_id`
    - `meta.tool_call_id`
    - `meta.evidence_hash`
    - `result.files[]`
    - `result.count`

- `qa_test`
  - Input:
    - `run_id` (string, required)
    - `dry_run` (boolean, optional)
  - Output:
    - `ok`
    - `meta.run_id`
    - `meta.tool_call_id`
    - `meta.evidence_hash`
    - `meta.dry_run`
    - `result.status`
    - `result.report`

- `qa_lint`
  - Input:
    - `run_id` (string, required)
    - `dry_run` (boolean, optional)
  - Output:
    - `ok`
    - `meta.run_id`
    - `meta.tool_call_id`
    - `meta.evidence_hash`
    - `meta.dry_run`
    - `result.status`
    - `result.report`

## Mapping to internal policy tool names

- `github_issues_create` -> `github.issues.create`
- `github_issues_batch_create` -> `github.issues.batch_create`
- `github_pr_comment_create` -> `github.pr.comment.create`
- `github_pr_get` -> `github.pr.get`
- `github_pr_files_list` -> `github.pr.files.list`
- `qa_test` -> `qa.test`
- `qa_lint` -> `qa.lint`

These internal names are what `TOOL_ALLOWLIST` enforces server-side.
