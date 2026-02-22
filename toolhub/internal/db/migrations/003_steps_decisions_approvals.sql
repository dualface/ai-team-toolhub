CREATE TABLE IF NOT EXISTS steps (
  step_id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  type TEXT NOT NULL,
  status TEXT NOT NULL,
  started_at TIMESTAMPTZ,
  finished_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_steps_run_created
  ON steps(run_id, created_at);

CREATE TABLE IF NOT EXISTS decisions (
  decision_id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE,
  step_id TEXT REFERENCES steps(step_id) ON DELETE SET NULL,
  actor TEXT NOT NULL,
  decision_type TEXT NOT NULL,
  payload_artifact_id TEXT REFERENCES artifacts(artifact_id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_decisions_run_created
  ON decisions(run_id, created_at);

CREATE TABLE IF NOT EXISTS approvals (
  approval_id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE,
  scope TEXT NOT NULL,
  status TEXT NOT NULL,
  requested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  approved_at TIMESTAMPTZ,
  approver TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_approvals_run_created
  ON approvals(run_id, created_at);
