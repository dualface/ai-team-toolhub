-- ToolHub minimal schema (Phase A: PRD -> Issues)
-- NOTE: This is a baseline. Use migrations if you later evolve the schema.

CREATE TABLE IF NOT EXISTS runs (
  run_id TEXT PRIMARY KEY,
  repo TEXT NOT NULL,
  purpose TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS artifacts (
  artifact_id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  uri TEXT NOT NULL,
  sha256 TEXT NOT NULL,
  size_bytes BIGINT NOT NULL,
  content_type TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS tool_calls (
  tool_call_id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE,
  tool_name TEXT NOT NULL,
  idempotency_key TEXT,
  status TEXT NOT NULL CHECK (status IN ('ok','fail')),
  request_artifact_id TEXT REFERENCES artifacts(artifact_id),
  response_artifact_id TEXT REFERENCES artifacts(artifact_id),
  evidence_hash TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_tool_calls_run ON tool_calls(run_id, created_at);
CREATE INDEX IF NOT EXISTS idx_artifacts_run ON artifacts(run_id, created_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_tool_calls_idem_ok
  ON tool_calls(run_id, tool_name, idempotency_key)
  WHERE idempotency_key IS NOT NULL AND status = 'ok';
