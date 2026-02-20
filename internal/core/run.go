// Package core implements the central business logic for ToolHub:
// run management, artifact storage, policy enforcement, and audit logging.
package core

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/toolhub/toolhub/internal/db"
)

// RunService manages the lifecycle of runs.
type RunService struct {
	db *db.DB
}

// NewRunService creates a RunService backed by the given database.
func NewRunService(database *db.DB) *RunService {
	return &RunService{db: database}
}

// CreateRunRequest contains parameters for creating a new run.
type CreateRunRequest struct {
	Repo    string `json:"repo"`
	Purpose string `json:"purpose"`
}

// CreateRun starts a new run and persists it.
func (s *RunService) CreateRun(ctx context.Context, req CreateRunRequest) (*db.Run, error) {
	if req.Repo == "" {
		return nil, fmt.Errorf("repo is required")
	}
	if req.Purpose == "" {
		return nil, fmt.Errorf("purpose is required")
	}

	run := &db.Run{
		RunID:     uuid.New().String(),
		Repo:      req.Repo,
		Purpose:   req.Purpose,
		CreatedAt: time.Now().UTC(),
	}

	if err := s.db.InsertRun(ctx, run); err != nil {
		return nil, fmt.Errorf("create run: %w", err)
	}
	return run, nil
}

// GetRun retrieves a run by its ID. Returns nil if not found.
func (s *RunService) GetRun(ctx context.Context, runID string) (*db.Run, error) {
	return s.db.GetRun(ctx, runID)
}

// ListRuns returns recent runs.
func (s *RunService) ListRuns(ctx context.Context, limit int) ([]*db.Run, error) {
	return s.db.ListRuns(ctx, limit)
}
