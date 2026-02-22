# MCP Tools (Generated)

This file is generated from `toolhub/internal/mcp/server.go`.

- `runs_create`
  - Description: Create a new ToolHub run for a repository
  - Input:
    - `purpose` (required)
    - `repo` (required)

- `github_issues_create`
  - Description: Create a GitHub issue within a run
  - Input:
    - `body` (required)
    - `dry_run` (optional)
    - `labels` (optional)
    - `run_id` (required)
    - `title` (required)

- `github_issues_batch_create`
  - Description: Create multiple GitHub issues within a run
  - Input:
    - `dry_run` (optional)
    - `issues` (required)
    - `run_id` (required)

- `github_pr_comment_create`
  - Description: Create a PR summary comment within a run
  - Input:
    - `body` (required)
    - `dry_run` (optional)
    - `pr_number` (required)
    - `run_id` (required)

- `github_pr_get`
  - Description: Get pull request metadata within a run
  - Input:
    - `pr_number` (required)
    - `run_id` (required)

- `github_pr_files_list`
  - Description: List pull request files within a run
  - Input:
    - `pr_number` (required)
    - `run_id` (required)

- `qa_test`
  - Description: Execute configured test command and capture output
  - Input:
    - `dry_run` (optional)
    - `run_id` (required)

- `qa_lint`
  - Description: Execute configured lint command and capture output
  - Input:
    - `dry_run` (optional)
    - `run_id` (required)

- `code_patch_generate`
  - Description: Generate unified patch/diff without modifying repository
  - Input:
    - `dry_run` (optional)
    - `modified_content` (required)
    - `original_content` (required)
    - `path` (required)
    - `run_id` (required)

