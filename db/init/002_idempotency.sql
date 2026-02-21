ALTER TABLE tool_calls
  ADD COLUMN IF NOT EXISTS idempotency_key TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_tool_calls_idem_ok
  ON tool_calls(run_id, tool_name, idempotency_key)
  WHERE idempotency_key IS NOT NULL AND status = 'ok';
