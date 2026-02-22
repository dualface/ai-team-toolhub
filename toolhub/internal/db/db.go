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
	if err := ApplyMigrations(ctx, conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("db migrate: %w", err)
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

// GetArtifactByRunAndID retrieves an artifact by run ID and artifact ID.
func (d *DB) GetArtifactByRunAndID(ctx context.Context, runID, artifactID string) (*Artifact, error) {
	a := &Artifact{}
	err := d.conn.QueryRowContext(ctx,
		`SELECT artifact_id, run_id, name, uri, sha256, size_bytes, content_type, created_at
		 FROM artifacts WHERE run_id = $1 AND artifact_id = $2`, runID, artifactID,
	).Scan(&a.ArtifactID, &a.RunID, &a.Name, &a.URI, &a.SHA256, &a.SizeBytes, &a.ContentType, &a.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get artifact by run and id: %w", err)
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
	IdempotencyKey     *string   `json:"idempotency_key,omitempty"`
	Status             string    `json:"status"`
	RequestArtifactID  *string   `json:"request_artifact_id,omitempty"`
	ResponseArtifactID *string   `json:"response_artifact_id,omitempty"`
	EvidenceHash       string    `json:"evidence_hash"`
	CreatedAt          time.Time `json:"created_at"`
}

// InsertToolCall creates a new tool call record.
func (d *DB) InsertToolCall(ctx context.Context, tc *ToolCall) error {
	_, err := d.conn.ExecContext(ctx,
		`INSERT INTO tool_calls (tool_call_id, run_id, tool_name, idempotency_key, status, request_artifact_id, response_artifact_id, evidence_hash, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		tc.ToolCallID, tc.RunID, tc.ToolName, tc.IdempotencyKey, tc.Status, tc.RequestArtifactID, tc.ResponseArtifactID, tc.EvidenceHash, tc.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert tool_call: %w", err)
	}
	return nil
}

func (d *DB) GetSuccessfulToolCallByIdempotency(ctx context.Context, runID, toolName, idempotencyKey string) (*ToolCall, error) {
	tc := &ToolCall{}
	err := d.conn.QueryRowContext(ctx,
		`SELECT tool_call_id, run_id, tool_name, idempotency_key, status, request_artifact_id, response_artifact_id, evidence_hash, created_at
		 FROM tool_calls
		 WHERE run_id = $1 AND tool_name = $2 AND idempotency_key = $3 AND status = 'ok'
		 ORDER BY created_at DESC
		 LIMIT 1`,
		runID, toolName, idempotencyKey,
	).Scan(&tc.ToolCallID, &tc.RunID, &tc.ToolName, &tc.IdempotencyKey, &tc.Status, &tc.RequestArtifactID, &tc.ResponseArtifactID, &tc.EvidenceHash, &tc.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get tool_call by idempotency: %w", err)
	}
	return tc, nil
}

// ListToolCallsByRun returns all tool calls for a given run.
func (d *DB) ListToolCallsByRun(ctx context.Context, runID string) ([]*ToolCall, error) {
	rows, err := d.conn.QueryContext(ctx,
		`SELECT tool_call_id, run_id, tool_name, idempotency_key, status, request_artifact_id, response_artifact_id, evidence_hash, created_at
		 FROM tool_calls WHERE run_id = $1 ORDER BY created_at`, runID,
	)
	if err != nil {
		return nil, fmt.Errorf("list tool_calls: %w", err)
	}
	defer rows.Close()

	var tcs []*ToolCall
	for rows.Next() {
		tc := &ToolCall{}
		if err := rows.Scan(&tc.ToolCallID, &tc.RunID, &tc.ToolName, &tc.IdempotencyKey, &tc.Status, &tc.RequestArtifactID, &tc.ResponseArtifactID, &tc.EvidenceHash, &tc.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan tool_call: %w", err)
		}
		tcs = append(tcs, tc)
	}
	return tcs, rows.Err()
}

type Step struct {
	StepID     string     `json:"step_id"`
	RunID      string     `json:"run_id"`
	Name       string     `json:"name"`
	Type       string     `json:"type"`
	Status     string     `json:"status"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

func (d *DB) InsertStep(ctx context.Context, s *Step) error {
	_, err := d.conn.ExecContext(ctx,
		`INSERT INTO steps (step_id, run_id, name, type, status, started_at, finished_at, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		s.StepID, s.RunID, s.Name, s.Type, s.Status, s.StartedAt, s.FinishedAt, s.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert step: %w", err)
	}
	return nil
}

func (d *DB) ListStepsByRun(ctx context.Context, runID string) ([]*Step, error) {
	rows, err := d.conn.QueryContext(ctx,
		`SELECT step_id, run_id, name, type, status, started_at, finished_at, created_at
		 FROM steps WHERE run_id = $1 ORDER BY created_at`, runID,
	)
	if err != nil {
		return nil, fmt.Errorf("list steps: %w", err)
	}
	defer rows.Close()

	out := make([]*Step, 0)
	for rows.Next() {
		s := &Step{}
		var started sql.NullTime
		var finished sql.NullTime
		if err := rows.Scan(&s.StepID, &s.RunID, &s.Name, &s.Type, &s.Status, &started, &finished, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan step: %w", err)
		}
		if started.Valid {
			t := started.Time
			s.StartedAt = &t
		}
		if finished.Valid {
			t := finished.Time
			s.FinishedAt = &t
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (d *DB) UpdateStepStatus(ctx context.Context, stepID, status string, finishedAt *time.Time) error {
	_, err := d.conn.ExecContext(ctx,
		`UPDATE steps SET status = $2, finished_at = $3 WHERE step_id = $1`,
		stepID, status, finishedAt,
	)
	if err != nil {
		return fmt.Errorf("update step status: %w", err)
	}
	return nil
}

type Decision struct {
	DecisionID        string    `json:"decision_id"`
	RunID             string    `json:"run_id"`
	StepID            *string   `json:"step_id,omitempty"`
	Actor             string    `json:"actor"`
	DecisionType      string    `json:"decision_type"`
	PayloadArtifactID *string   `json:"payload_artifact_id,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

func (d *DB) InsertDecision(ctx context.Context, in *Decision) error {
	_, err := d.conn.ExecContext(ctx,
		`INSERT INTO decisions (decision_id, run_id, step_id, actor, decision_type, payload_artifact_id, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		in.DecisionID, in.RunID, in.StepID, in.Actor, in.DecisionType, in.PayloadArtifactID, in.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert decision: %w", err)
	}
	return nil
}

func (d *DB) ListDecisionsByRun(ctx context.Context, runID string) ([]*Decision, error) {
	rows, err := d.conn.QueryContext(ctx,
		`SELECT decision_id, run_id, step_id, actor, decision_type, payload_artifact_id, created_at
		 FROM decisions WHERE run_id = $1 ORDER BY created_at`, runID,
	)
	if err != nil {
		return nil, fmt.Errorf("list decisions: %w", err)
	}
	defer rows.Close()

	out := make([]*Decision, 0)
	for rows.Next() {
		item := &Decision{}
		var stepID sql.NullString
		var payloadID sql.NullString
		if err := rows.Scan(&item.DecisionID, &item.RunID, &stepID, &item.Actor, &item.DecisionType, &payloadID, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan decision: %w", err)
		}
		if stepID.Valid {
			s := stepID.String
			item.StepID = &s
		}
		if payloadID.Valid {
			s := payloadID.String
			item.PayloadArtifactID = &s
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

type Approval struct {
	ApprovalID  string     `json:"approval_id"`
	RunID       string     `json:"run_id"`
	Scope       string     `json:"scope"`
	Status      string     `json:"status"`
	RequestedAt time.Time  `json:"requested_at"`
	ApprovedAt  *time.Time `json:"approved_at,omitempty"`
	Approver    *string    `json:"approver,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

func (d *DB) InsertApproval(ctx context.Context, in *Approval) error {
	_, err := d.conn.ExecContext(ctx,
		`INSERT INTO approvals (approval_id, run_id, scope, status, requested_at, approved_at, approver, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		in.ApprovalID, in.RunID, in.Scope, in.Status, in.RequestedAt, in.ApprovedAt, in.Approver, in.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert approval: %w", err)
	}
	return nil
}

func (d *DB) UpdateApprovalDecision(ctx context.Context, approvalID, status string, approvedAt *time.Time, approver *string) error {
	res, err := d.conn.ExecContext(ctx,
		`UPDATE approvals
		 SET status = $2, approved_at = $3, approver = $4
		 WHERE approval_id = $1`,
		approvalID, status, approvedAt, approver,
	)
	if err != nil {
		return fmt.Errorf("update approval: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected approval update: %w", err)
	}
	if rows == 0 {
		return nil
	}
	return nil
}

func (d *DB) GetApproval(ctx context.Context, approvalID string) (*Approval, error) {
	item := &Approval{}
	var approved sql.NullTime
	var approver sql.NullString
	err := d.conn.QueryRowContext(ctx,
		`SELECT approval_id, run_id, scope, status, requested_at, approved_at, approver, created_at
		 FROM approvals WHERE approval_id = $1`, approvalID,
	).Scan(&item.ApprovalID, &item.RunID, &item.Scope, &item.Status, &item.RequestedAt, &approved, &approver, &item.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get approval: %w", err)
	}
	if approved.Valid {
		t := approved.Time
		item.ApprovedAt = &t
	}
	if approver.Valid {
		s := approver.String
		item.Approver = &s
	}
	return item, nil
}

func (d *DB) ListApprovalsByRun(ctx context.Context, runID string) ([]*Approval, error) {
	rows, err := d.conn.QueryContext(ctx,
		`SELECT approval_id, run_id, scope, status, requested_at, approved_at, approver, created_at
		 FROM approvals WHERE run_id = $1 ORDER BY created_at`, runID,
	)
	if err != nil {
		return nil, fmt.Errorf("list approvals: %w", err)
	}
	defer rows.Close()

	out := make([]*Approval, 0)
	for rows.Next() {
		item := &Approval{}
		var approved sql.NullTime
		var approver sql.NullString
		if err := rows.Scan(&item.ApprovalID, &item.RunID, &item.Scope, &item.Status, &item.RequestedAt, &approved, &approver, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan approval: %w", err)
		}
		if approved.Valid {
			t := approved.Time
			item.ApprovedAt = &t
		}
		if approver.Valid {
			s := approver.String
			item.Approver = &s
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
