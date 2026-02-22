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
    - `meta.replayed` (boolean, optional)
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
    - `meta.replayed` (boolean, optional)
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
    - `meta.qa_artifacts.stdout_artifact_id` (string, optional)
    - `meta.qa_artifacts.stderr_artifact_id` (string, optional)
    - `meta.qa_artifacts.report_artifact_id` (string, optional)
    - `result.status` (`pass|fail|timeout|error|dry_run`)
    - `result.report`
    - `result.report.command` (string)
    - `result.report.exit_code` (integer)
    - `result.report.duration_ms` (integer)
    - `result.report.stdout` (string)
    - `result.report.stderr` (string)
    - `result.report.stdout_truncated` (boolean)
    - `result.report.stderr_truncated` (boolean)

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
    - `meta.qa_artifacts.stdout_artifact_id` (string, optional)
    - `meta.qa_artifacts.stderr_artifact_id` (string, optional)
    - `meta.qa_artifacts.report_artifact_id` (string, optional)
    - `result.status` (`pass|fail|timeout|error|dry_run`)
    - `result.report`
    - `result.report.command` (string)
    - `result.report.exit_code` (integer)
    - `result.report.duration_ms` (integer)
    - `result.report.stdout` (string)
    - `result.report.stderr` (string)
    - `result.report.stdout_truncated` (boolean)
    - `result.report.stderr_truncated` (boolean)

- `code_patch_generate`
  - Input:
    - `run_id` (string, required)
    - `path` (string, required)
    - `original_content` (string, required)
    - `modified_content` (string, required)
    - `dry_run` (boolean, optional)
  - Output:
    - `ok`
    - `meta.run_id`
    - `meta.tool_call_id`
    - `meta.evidence_hash`
    - `meta.dry_run`
    - `result.path`
    - `result.patch` (unified diff text)
    - `result.line_delta`
    - `result.patch_artifact_id` (string, optional)

- `code_branch_pr_create`
  - Input:
    - `run_id` (string, required)
    - `approval_id` (string, required)
    - `base_branch` (string, required)
    - `head_branch` (string, required)
    - `commit_message` (string, required)
    - `pr_title` (string, required)
    - `pr_body` (string, optional)
    - `files` (array, required)
      - `path` (string, required)
      - `original_content` (string, optional)
      - `modified_content` (string, required)
    - `dry_run` (boolean, optional)
  - Output:
    - `ok`
    - `meta.run_id`
    - `meta.tool_call_id`
    - `meta.evidence_hash`
    - `meta.dry_run`
    - `result.base_branch`
    - `result.head_branch`
    - `result.planned_commands[]`
    - `result.commit_hash` (string, optional)
    - `result.pull_request` (object, optional)
    - `result.patch_artifact_id` (string, optional)

## Status Semantics

ToolHub uses different status representations at different layers:

### Audit Status

The `tool_calls.status` field in PostgreSQL records binary outcomes:
- `ok` — the tool call succeeded
- `fail` — the tool call failed

This is the ground truth for evidence integrity. See `audit.go` for the logic:
status is "ok" unless an error occurred.

### Batch Status

Batch endpoints return `result.status` with three possible values:
- `ok` — all items in the batch succeeded
- `fail` — all items in the batch failed
- `partial` — some items succeeded, some failed

The `partial` status is derived by business logic at response time
(`DeriveBatchStatus`), not stored in the database. The underlying
`tool_calls.status` for the batch operation itself remains binary ok/fail
(the batch call either completes or errors out).

### QA Status

QA tools (`qa_test`, `qa_lint`) have their own status enum:
- `pass` — command exited 0
- `fail` — command exited non-zero
- `timeout` — command exceeded configured timeout
- `error` — pre-execution failure (invalid command, workdir, etc.)
- `dry_run` — dry-run mode, command was not executed

This is distinct from both audit status and batch status.


## Mapping to internal policy tool names

- `github_issues_create` -> `github.issues.create`
- `github_issues_batch_create` -> `github.issues.batch_create`
- `github_pr_comment_create` -> `github.pr.comment.create`
- `github_pr_get` -> `github.pr.get`
- `github_pr_files_list` -> `github.pr.files.list`
- `qa_test` -> `qa.test`
- `qa_lint` -> `qa.lint`

These internal names are what `TOOL_ALLOWLIST` enforces server-side.
