package core

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/toolhub/toolhub/internal/db"
)

// ---------------------------------------------------------------------------
// Category A: Artifact write failure (no real DB required)
// ---------------------------------------------------------------------------

// TestRecord_ArtifactWriteFailure proves that when the artifact store cannot
// write (e.g. read-only directory), Record() returns an error mentioning the
// artifact layer.
func TestRecord_ArtifactWriteFailure(t *testing.T) {
	dir := t.TempDir()


	store, err := NewArtifactStore(nil, dir)
	if err != nil {
		t.Fatalf("NewArtifactStore: %v", err)
	}


	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	policy := NewPolicy("owner/repo", "test.tool")
	audit := &AuditService{db: nil, store: store, policy: policy}

	_, _, err = audit.Record(context.Background(), RecordInput{
		RunID:    "run-artifact-fail",
		ToolName: "test.tool",
		Request:  map[string]any{"key": "value"},
		Response: map[string]any{"ok": true},
	})
	if err == nil {
		t.Fatal("expected error from Record when artifact store is read-only")
	}
	if !strings.Contains(err.Error(), "artifact") && !strings.Contains(err.Error(), "save") && !strings.Contains(err.Error(), "mkdir") {
		t.Fatalf("expected artifact-related error, got: %v", err)
	}
}

// TestArtifactStore_Save_CleansUpFileOnWriteFailure proves that Save()
// removes the partially-written file when the io.Copy / file close fails.
// We simulate this by providing a reader that errors partway through.
func TestArtifactStore_Save_CleansUpFileOnWriteFailure(t *testing.T) {
	dir := t.TempDir()
	store, err := NewArtifactStore(nil, dir)
	if err != nil {
		t.Fatalf("NewArtifactStore: %v", err)
	}


	_, err = store.Save(context.Background(), SaveInput{
		RunID:       "run-save-fail",
		Name:        "test.artifact.json",
		ContentType: "application/json",
		Body:        &failingReader{},
	})
	if err == nil {
		t.Fatal("expected error from Save with failing reader")
	}


	runDir := filepath.Join(dir, "run-save-fail")
	entries, _ := os.ReadDir(runDir)
	if len(entries) > 0 {
		t.Fatalf("expected no artifact files after failed write, found %d", len(entries))
	}
}

// failingReader is an io.Reader that always returns an error.
type failingReader struct{}

func (r *failingReader) Read(p []byte) (int, error) {
	return 0, &os.PathError{Op: "read", Path: "injected", Err: os.ErrClosed}
}

// ---------------------------------------------------------------------------
// Category B: DB insert failure after artifacts succeed (requires test DB)
// ---------------------------------------------------------------------------

// TestRecord_DBToolCallInsertFailure proves Scenario F2: artifacts are written
// but the tool_call INSERT fails, leaving orphan artifacts.
func TestRecord_DBToolCallInsertFailure(t *testing.T) {
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

	dir := t.TempDir()
	store, err := NewArtifactStore(database, dir)
	if err != nil {
		t.Fatalf("NewArtifactStore: %v", err)
	}

	policy := NewPolicy("owner/repo", "github.issues.create")
	runs := NewRunService(database)
	audit := NewAuditService(database, store, policy)

	run, err := runs.CreateRun(ctx, CreateRunRequest{Repo: "owner/repo", Purpose: "db_fail_test"})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}


	if _, err := database.Conn().ExecContext(ctx, `DROP TABLE IF EXISTS tool_calls CASCADE`); err != nil {
		t.Fatalf("drop tool_calls: %v", err)
	}

	t.Cleanup(func() {
		ensureSchema(ctx, database)
	})

	_, _, err = audit.Record(ctx, RecordInput{
		RunID:    run.RunID,
		ToolName: "github.issues.create",
		Request:  map[string]any{"title": "orphan test"},
		Response: map[string]any{"number": 999},
	})
	if err == nil {
		t.Fatal("expected error from Record when tool_calls table is missing")
	}
	if !strings.Contains(err.Error(), "tool_call") && !strings.Contains(err.Error(), "insert") {
		t.Logf("unexpected error wording (may still be valid): %v", err)
	}


	runDir := filepath.Join(dir, run.RunID)
	entries, readErr := os.ReadDir(runDir)
	if readErr != nil {
		t.Fatalf("expected run directory to exist: %v", readErr)
	}
	if len(entries) < 2 {
		t.Fatalf("expected at least 2 orphan artifact files (req+resp), found %d", len(entries))
	}


	arts, err := database.ListArtifactsByRun(ctx, run.RunID)
	if err != nil {
		t.Fatalf("list artifacts: %v", err)
	}
	if len(arts) < 2 {
		t.Fatalf("expected at least 2 artifact DB records, found %d", len(arts))
	}
}

// TestArtifactStore_Save_CleansUpFileOnDBFailure proves that when the artifact
// file is written successfully but the DB INSERT fails, Save() removes the file.
func TestArtifactStore_Save_CleansUpFileOnDBFailure(t *testing.T) {
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

	runs := NewRunService(database)
	run, err := runs.CreateRun(ctx, CreateRunRequest{Repo: "owner/repo", Purpose: "save_cleanup_test"})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	if _, err := database.Conn().ExecContext(ctx, `DROP TABLE IF EXISTS tool_calls CASCADE`); err != nil {
		t.Fatalf("drop tool_calls: %v", err)
	}
	if _, err := database.Conn().ExecContext(ctx, `DROP TABLE IF EXISTS artifacts CASCADE`); err != nil {
		t.Fatalf("drop artifacts: %v", err)
	}
	t.Cleanup(func() {
		ensureSchema(ctx, database)
	})

	dir := t.TempDir()
	store, err := NewArtifactStore(database, dir)
	if err != nil {
		t.Fatalf("NewArtifactStore: %v", err)
	}

	_, err = store.Save(ctx, SaveInput{
		RunID:       run.RunID,
		Name:        "cleanup-test.json",
		ContentType: "application/json",
		Body:        bytes.NewReader([]byte(`{"test": true}`)),
	})
	if err == nil {
		t.Fatal("expected error from Save when artifacts table is missing")
	}


	runDir := filepath.Join(dir, run.RunID)
	entries, _ := os.ReadDir(runDir)
	if len(entries) != 0 {
		t.Fatalf("expected no artifact files after DB failure cleanup, found %d", len(entries))
	}
}
