package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/toolhub/toolhub/internal/db"
)

// AuditService records every tool invocation with full request/response
// artifacts and a SHA-256 evidence hash for tamper detection.
type AuditService struct {
	db     *db.DB
	store  *ArtifactStore
	policy *Policy
}

// NewAuditService wires the audit layer to its dependencies.
func NewAuditService(database *db.DB, store *ArtifactStore, policy *Policy) *AuditService {
	return &AuditService{db: database, store: store, policy: policy}
}

// RecordInput captures what is needed to log a tool call.
type RecordInput struct {
	RunID    string
	ToolName string
	IdemKey  *string
	Request  any
	Response any
	Err      error
}

// Record persists a tool call with its request/response as artifacts.
func (a *AuditService) Record(ctx context.Context, in RecordInput) (*db.ToolCall, error) {
	if err := a.policy.CheckTool(in.ToolName); err != nil {
		return nil, err
	}

	reqJSON, err := json.Marshal(in.Request)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	reqArt, err := a.store.Save(ctx, SaveInput{
		RunID:       in.RunID,
		Name:        in.ToolName + ".request.json",
		ContentType: "application/json",
		Body:        bytes.NewReader(reqJSON),
	})
	if err != nil {
		return nil, fmt.Errorf("save request artifact: %w", err)
	}

	respJSON, err := json.Marshal(in.Response)
	if err != nil {
		return nil, fmt.Errorf("marshal response: %w", err)
	}
	respArt, err := a.store.Save(ctx, SaveInput{
		RunID:       in.RunID,
		Name:        in.ToolName + ".response.json",
		ContentType: "application/json",
		Body:        bytes.NewReader(respJSON),
	})
	if err != nil {
		return nil, fmt.Errorf("save response artifact: %w", err)
	}

	status := "ok"
	if in.Err != nil {
		status = "fail"
	}

	evidence := sha256.Sum256(append(reqJSON, respJSON...))

	tc := &db.ToolCall{
		ToolCallID:         uuid.New().String(),
		RunID:              in.RunID,
		ToolName:           in.ToolName,
		IdempotencyKey:     in.IdemKey,
		Status:             status,
		RequestArtifactID:  &reqArt.ArtifactID,
		ResponseArtifactID: &respArt.ArtifactID,
		EvidenceHash:       hex.EncodeToString(evidence[:]),
		CreatedAt:          time.Now().UTC(),
	}
	if err := a.db.InsertToolCall(ctx, tc); err != nil {
		return nil, fmt.Errorf("insert tool_call: %w", err)
	}
	return tc, nil
}

func (a *AuditService) ReplayResponse(ctx context.Context, runID, toolName, idempotencyKey string, out any) (*db.ToolCall, bool, error) {
	tc, err := a.db.GetSuccessfulToolCallByIdempotency(ctx, runID, toolName, idempotencyKey)
	if err != nil {
		return nil, false, err
	}
	if tc == nil {
		return nil, false, nil
	}
	if tc.ResponseArtifactID == nil {
		return nil, false, fmt.Errorf("response artifact missing for replay")
	}

	b, err := a.store.Read(ctx, *tc.ResponseArtifactID)
	if err != nil {
		return nil, false, err
	}
	if err := json.Unmarshal(b, out); err != nil {
		return nil, false, fmt.Errorf("decode replay response: %w", err)
	}
	return tc, true, nil
}
