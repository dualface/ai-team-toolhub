package core

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/toolhub/toolhub/internal/db"
)

func TestAuditConsistencyDBVsArtifact(t *testing.T) {
	databaseURL := os.Getenv("TOOLHUB_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TOOLHUB_TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	database, err := db.New(databaseURL)
	if err != nil {
		t.Fatalf("db connect: %v", err)
	}
	defer database.Close()

	if err := ensureSchema(ctx, database); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}

	store, err := NewArtifactStore(database, t.TempDir())
	if err != nil {
		t.Fatalf("new artifact store: %v", err)
	}

	policy := NewPolicy("owner/repo", "github.issues.create,github.issues.batch_create,github.pr.comment.create")
	runs := NewRunService(database)
	audit := NewAuditService(database, store, policy)

	run, err := runs.CreateRun(ctx, CreateRunRequest{Repo: "owner/repo", Purpose: "audit_consistency_test"})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	tcOK, err := audit.Record(ctx, RecordInput{
		RunID:    run.RunID,
		ToolName: "github.issues.create",
		Request:  map[string]any{"title": "ok"},
		Response: map[string]any{"number": 123},
	})
	if err != nil {
		t.Fatalf("record ok call: %v", err)
	}
	if tcOK.EvidenceHash == "" {
		t.Fatal("expected non-empty evidence hash")
	}
	if tcOK.RequestArtifactID == nil || tcOK.ResponseArtifactID == nil {
		t.Fatal("expected both request and response artifact IDs")
	}
	reqArt, err := database.GetArtifact(ctx, *tcOK.RequestArtifactID)
	if err != nil || reqArt == nil {
		t.Fatalf("expected request artifact in DB, got err=%v art=%v", err, reqArt)
	}
	respArt, err := database.GetArtifact(ctx, *tcOK.ResponseArtifactID)
	if err != nil || respArt == nil {
		t.Fatalf("expected response artifact in DB, got err=%v art=%v", err, respArt)
	}
	if _, err := store.Read(ctx, *tcOK.RequestArtifactID); err != nil {
		t.Fatalf("expected readable request artifact file: %v", err)
	}
	if _, err := store.Read(ctx, *tcOK.ResponseArtifactID); err != nil {
		t.Fatalf("expected readable response artifact file: %v", err)
	}

	tcFail, err := audit.Record(ctx, RecordInput{
		RunID:    run.RunID,
		ToolName: "github.issues.create",
		Request:  map[string]any{"title": "fail"},
		Response: map[string]any{"error": "simulated upstream failure"},
		Err:      errors.New("simulated upstream failure"),
	})
	if err != nil {
		t.Fatalf("record fail call: %v", err)
	}
	if tcFail.Status != "fail" {
		t.Fatalf("expected fail status, got %q", tcFail.Status)
	}
	if tcFail.EvidenceHash == "" {
		t.Fatal("expected non-empty evidence hash for fail path")
	}
	if tcFail.RequestArtifactID == nil || tcFail.ResponseArtifactID == nil {
		t.Fatal("expected artifacts for fail path")
	}
}

func ensureSchema(ctx context.Context, database *db.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS runs (run_id TEXT PRIMARY KEY, repo TEXT NOT NULL, purpose TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE IF NOT EXISTS artifacts (artifact_id TEXT PRIMARY KEY, run_id TEXT NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE, name TEXT NOT NULL, uri TEXT NOT NULL, sha256 TEXT NOT NULL, size_bytes BIGINT NOT NULL, content_type TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE IF NOT EXISTS tool_calls (tool_call_id TEXT PRIMARY KEY, run_id TEXT NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE, tool_name TEXT NOT NULL, idempotency_key TEXT, status TEXT NOT NULL CHECK (status IN ('ok','fail')), request_artifact_id TEXT REFERENCES artifacts(artifact_id), response_artifact_id TEXT REFERENCES artifacts(artifact_id), evidence_hash TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE INDEX IF NOT EXISTS idx_tool_calls_run ON tool_calls(run_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_artifacts_run ON artifacts(run_id, created_at)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_tool_calls_idem_ok ON tool_calls(run_id, tool_name, idempotency_key) WHERE idempotency_key IS NOT NULL AND status = 'ok'`,
	}
	for _, stmt := range stmts {
		if _, err := database.Conn().ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}
