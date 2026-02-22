# C4 Batch Idempotency Strategy

## Scope

This document defines idempotency behavior for `POST /api/v1/runs/{runID}/issues/batch`.

## Goals

- Prevent duplicate GitHub resources on network retries.
- Keep behavior deterministic across partial failures.
- Reuse existing `tool_calls.idempotency_key` and replay pipeline.

## Chosen Strategy

Use item-level idempotency with deterministic keys:

- Each batch item uses key material:
  - `run_id`
  - `tool_name` (`github.issues.batch_create`)
  - normalized payload (`title`, `body`, sorted `labels`)
  - item `index`
- Existing successful entries (`status='ok'`) are replayed.
- Failed entries are not replayed and may be retried.

This matches current implementation and gives stable retry semantics without requiring a new batch-level table.

## Conflict Semantics

- Reusing the same idempotency key for a different payload is a conflict.
- For explicit idempotency keys (single-write endpoints), return:
  - HTTP `409`
  - code `idempotency_key_conflict`

For batch endpoint, deterministic keys are generated internally, so payload conflicts are avoided by construction.

## Strict vs Partial Mode

- `partial` mode: continue processing remaining items.
- `strict` mode: stop on first failure.
- Replayed items are counted as processed and included in response with `replayed=true`.

## Future Extension

If external clients require explicit idempotency for the full batch request, add optional `Idempotency-Key` at batch level and persist a batch envelope artifact keyed by `(run_id, tool_name, idempotency_key)`.
