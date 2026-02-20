// Package db provides PostgreSQL persistence for ToolHub's audit trail:
// runs, artifacts, and tool_calls.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

// DB wraps the underlying *sql.DB and provides typed query methods.
type DB struct {
	conn *sql.DB
}

// New opens a PostgreSQL connection and verifies connectivity.
func New(databaseURL string) (*DB, error) {
	conn, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("db open: %w", err)
	}
	conn.SetMaxOpenConns(25)
	conn.SetMaxIdleConns(5)
	conn.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.PingContext(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("db ping: %w", err)
	}
	return &DB{conn: conn}, nil
}

// Close closes the database connection pool.
func (d *DB) Close() error {
	return d.conn.Close()
}

// Conn returns the underlying *sql.DB for use by other layers (e.g., transactions).
func (d *DB) Conn() *sql.DB {
	return d.conn
}

// Run represents a single ToolHub execution run.
type Run struct {
	RunID     string    `json:"run_id"`
	Repo      string    `json:"repo"`
	Purpose   string    `json:"purpose"`
	CreatedAt time.Time `json:"created_at"`
}

// InsertRun creates a new run record.
func (d *DB) InsertRun(ctx context.Context, r *Run) error {
	_, err := d.conn.ExecContext(ctx,
		`INSERT INTO runs (run_id, repo, purpose, created_at) VALUES ($1, $2, $3, $4)`,
		r.RunID, r.Repo, r.Purpose, r.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert run: %w", err)
	}
	return nil
}

// GetRun retrieves a run by ID.
func (d *DB) GetRun(ctx context.Context, runID string) (*Run, error) {
	r := &Run{}
	err := d.conn.QueryRowContext(ctx,
		`SELECT run_id, repo, purpose, created_at FROM runs WHERE run_id = $1`, runID,
	).Scan(&r.RunID, &r.Repo, &r.Purpose, &r.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get run: %w", err)
	}
	return r, nil
}

// ListRuns returns all runs, most recent first.
func (d *DB) ListRuns(ctx context.Context, limit int) ([]*Run, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := d.conn.QueryContext(ctx,
		`SELECT run_id, repo, purpose, created_at FROM runs ORDER BY created_at DESC LIMIT $1`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	defer rows.Close()

	var runs []*Run
	for rows.Next() {
		r := &Run{}
		if err := rows.Scan(&r.RunID, &r.Repo, &r.Purpose, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan run: %w", err)
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

// Artifact represents a stored file linked to a run.
type Artifact struct {
	ArtifactID  string    `json:"artifact_id"`
	RunID       string    `json:"run_id"`
	Name        string    `json:"name"`
	URI         string    `json:"uri"`
	SHA256      string    `json:"sha256"`
	SizeBytes   int64     `json:"size_bytes"`
	ContentType string    `json:"content_type"`
	CreatedAt   time.Time `json:"created_at"`
}

// InsertArtifact creates a new artifact record.
func (d *DB) InsertArtifact(ctx context.Context, a *Artifact) error {
	_, err := d.conn.ExecContext(ctx,
		`INSERT INTO artifacts (artifact_id, run_id, name, uri, sha256, size_bytes, content_type, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		a.ArtifactID, a.RunID, a.Name, a.URI, a.SHA256, a.SizeBytes, a.ContentType, a.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert artifact: %w", err)
	}
	return nil
}

// GetArtifact retrieves an artifact by ID.
func (d *DB) GetArtifact(ctx context.Context, artifactID string) (*Artifact, error) {
	a := &Artifact{}
	err := d.conn.QueryRowContext(ctx,
		`SELECT artifact_id, run_id, name, uri, sha256, size_bytes, content_type, created_at
		 FROM artifacts WHERE artifact_id = $1`, artifactID,
	).Scan(&a.ArtifactID, &a.RunID, &a.Name, &a.URI, &a.SHA256, &a.SizeBytes, &a.ContentType, &a.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get artifact: %w", err)
	}
	return a, nil
}

// ListArtifactsByRun returns all artifacts for a given run.
func (d *DB) ListArtifactsByRun(ctx context.Context, runID string) ([]*Artifact, error) {
	rows, err := d.conn.QueryContext(ctx,
		`SELECT artifact_id, run_id, name, uri, sha256, size_bytes, content_type, created_at
		 FROM artifacts WHERE run_id = $1 ORDER BY created_at`, runID,
	)
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	defer rows.Close()

	var arts []*Artifact
	for rows.Next() {
		a := &Artifact{}
		if err := rows.Scan(&a.ArtifactID, &a.RunID, &a.Name, &a.URI, &a.SHA256, &a.SizeBytes, &a.ContentType, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan artifact: %w", err)
		}
		arts = append(arts, a)
	}
	return arts, rows.Err()
}

// ToolCall represents a single tool invocation within a run.
type ToolCall struct {
	ToolCallID         string    `json:"tool_call_id"`
	RunID              string    `json:"run_id"`
	ToolName           string    `json:"tool_name"`
	Status             string    `json:"status"`
	RequestArtifactID  *string   `json:"request_artifact_id,omitempty"`
	ResponseArtifactID *string   `json:"response_artifact_id,omitempty"`
	EvidenceHash       string    `json:"evidence_hash"`
	CreatedAt          time.Time `json:"created_at"`
}

// InsertToolCall creates a new tool call record.
func (d *DB) InsertToolCall(ctx context.Context, tc *ToolCall) error {
	_, err := d.conn.ExecContext(ctx,
		`INSERT INTO tool_calls (tool_call_id, run_id, tool_name, status, request_artifact_id, response_artifact_id, evidence_hash, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		tc.ToolCallID, tc.RunID, tc.ToolName, tc.Status, tc.RequestArtifactID, tc.ResponseArtifactID, tc.EvidenceHash, tc.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert tool_call: %w", err)
	}
	return nil
}

// ListToolCallsByRun returns all tool calls for a given run.
func (d *DB) ListToolCallsByRun(ctx context.Context, runID string) ([]*ToolCall, error) {
	rows, err := d.conn.QueryContext(ctx,
		`SELECT tool_call_id, run_id, tool_name, status, request_artifact_id, response_artifact_id, evidence_hash, created_at
		 FROM tool_calls WHERE run_id = $1 ORDER BY created_at`, runID,
	)
	if err != nil {
		return nil, fmt.Errorf("list tool_calls: %w", err)
	}
	defer rows.Close()

	var tcs []*ToolCall
	for rows.Next() {
		tc := &ToolCall{}
		if err := rows.Scan(&tc.ToolCallID, &tc.RunID, &tc.ToolName, &tc.Status, &tc.RequestArtifactID, &tc.ResponseArtifactID, &tc.EvidenceHash, &tc.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan tool_call: %w", err)
		}
		tcs = append(tcs, tc)
	}
	return tcs, rows.Err()
}
