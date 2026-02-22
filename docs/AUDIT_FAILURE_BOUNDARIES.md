# Audit Failure Boundaries

This document describes the exact failure-boundary semantics of ToolHub's audit pipeline.

## Section 1: Pipeline Overview

The audit pipeline for every tool call follows this strict order:

1. Policy check (`policy.CheckTool` / `policy.CheckPaths` / `policy.CheckRepo`)
2. Tool execution (GitHub API call, code operation, QA command)
3. Artifact write (request artifact to disk, then response artifact to disk, then optional extra artifacts to disk)
4. Audit DB write (`INSERT INTO tool_calls` with evidence hash and artifact IDs)

## Section 2: Core Failure Scenarios

### Scenario F1: Request artifact write succeeds, response artifact write fails

- **Cause**: Disk full, permission error, or I/O failure after request artifact was written
- **Observable outcome**: `audit.Record()` returns error. Caller receives HTTP 500 / MCP internal error. The request artifact file exists on disk with a DB record, but no tool_call record is created. The tool execution result is lost from the audit trail.
- **Compensation**: Currently none — the orphan request artifact remains on disk and in DB. There is no automatic cleanup of partial artifacts.
- **Metric**: `toolhub_artifact_write_failures_total` is incremented via `telemetry.IncArtifactWriteFailure()`.

### Scenario F2: All artifacts written successfully, DB tool_call INSERT fails

- **Cause**: PostgreSQL connection loss, constraint violation, or timeout during `INSERT INTO tool_calls`
- **Observable outcome**: `audit.Record()` returns error. Caller receives HTTP 500 / MCP internal error. Both request and response artifact files exist on disk with DB records in the `artifacts` table, but no `tool_calls` row links them together. The tool execution actually succeeded upstream, but the evidence chain is broken.
- **Compensation**: Currently none — the orphan artifacts remain. No tool_call row means the invocation is invisible to audit queries, but artifact files can be discovered via filesystem or `artifacts` table scan.
- **Metric**: None specific; the error propagates to the caller.

### Scenario F3: Tool execution succeeds, audit.Record() succeeds, but supplementary audit writes fail (RecordDecision / FinishStep)

- **Applies to**: `code.repair_loop` handlers only (HTTP and MCP)
- **Cause**: DB connection loss or timeout during `INSERT INTO decisions` or `UPDATE steps`
- **Observable outcome**: The tool call completes successfully for the caller. The primary tool_call record with request/response artifacts exists. However, fine-grained step/decision records may be incomplete — e.g., iteration-level QA decisions or the final step status update may be missing.
- **Compensation**: These errors are **logged** (via `s.logger.Error(...)`) but do **not** fail the request. The primary audit record (tool_call + artifacts + evidence_hash) remains intact.
- **Metric**: None specific; the error is logged with structured fields (`err`, `run_id`, `decision_type` or `step_id`).

## Section 3: Artifact Write Cleanup Behavior

`ArtifactStore.Save()` (in `toolhub/internal/core/artifact.go`) has a two-phase cleanup pattern:

1. If the file write (io.Copy) or file close fails, the partial file is deleted via `os.Remove(fpath)` before returning error
2. If the DB INSERT for the artifact metadata fails, the successfully-written file is deleted via `os.Remove(fpath)` before returning error
3. If the file was written and the DB record inserted, but the *subsequent* artifact write or tool_call INSERT fails, no cleanup of the earlier successful artifacts occurs (orphan artifact)

## Section 4: Fatal vs Best-Effort Classification

| Operation | Failure Mode | Fatal? | Caller Impact | Compensation |
|-----------|-------------|--------|---------------|-------------|
| `audit.Record()` | Artifact write failed | Yes | HTTP 500 / MCP -32603 | Partial file cleanup in ArtifactStore.Save |
| `audit.Record()` | DB tool_call INSERT failed | Yes | HTTP 500 / MCP -32603 | None (orphan artifacts remain) |
| `audit.StartStep()` | DB step INSERT failed | Yes | HTTP 500 / MCP -32603 | None |
| `audit.RecordDecision()` | DB decision INSERT failed | No | Request completes normally | Error logged |
| `audit.FinishStep()` | DB step UPDATE failed | No | Request completes normally | Error logged |

## Section 5: Observability

- `toolhub_artifact_write_failures_total` counter is incremented on any artifact write failure
- Structured log entries with `err`, `run_id`, `decision_type`, `step_id` for best-effort audit failures
- `toolhub_tool_calls_total{tool=...,status=...}` counter is only incremented when `audit.Record()` succeeds (which means the full audit chain is complete)

## Section 6: Known Gaps and Future Work

- No automatic orphan artifact cleanup: if F1 or F2 occurs, orphan artifacts accumulate until manual intervention
- No transaction wrapping artifact writes + tool_call INSERT: these are separate operations, so partial failure is possible
- Step/decision records are best-effort: for repair_loop, the fine-grained iteration audit may have gaps if DB is intermittently unavailable
- Consider: periodic orphan artifact scanner, transactional artifact+tool_call write, or WAL-based compensation log
