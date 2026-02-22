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
	"github.com/toolhub/toolhub/internal/telemetry"
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
	RunID          string
	ToolName       string
	IdemKey        *string
	Request        any
	Response       any
	Err            error
	ExtraArtifacts []ExtraArtifact
}

type ExtraArtifact struct {
	Name        string
	ContentType string
	Body        []byte
}

// Record persists a tool call with its request/response as artifacts.
func (a *AuditService) Record(ctx context.Context, in RecordInput) (*db.ToolCall, []string, error) {
	if err := a.policy.CheckTool(in.ToolName); err != nil {
		return nil, nil, err
	}

	reqJSON, err := json.Marshal(in.Request)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal request: %w", err)
	}
	reqArt, err := a.store.Save(ctx, SaveInput{
		RunID:       in.RunID,
		Name:        in.ToolName + ".request.json",
		ContentType: "application/json",
		Body:        bytes.NewReader(reqJSON),
	})
	if err != nil {
		telemetry.IncArtifactWriteFailure()
		return nil, nil, fmt.Errorf("save request artifact: %w", err)
	}

	respJSON, err := json.Marshal(in.Response)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal response: %w", err)
	}
	respArt, err := a.store.Save(ctx, SaveInput{
		RunID:       in.RunID,
		Name:        in.ToolName + ".response.json",
		ContentType: "application/json",
		Body:        bytes.NewReader(respJSON),
	})
	if err != nil {
		telemetry.IncArtifactWriteFailure()
		return nil, nil, fmt.Errorf("save response artifact: %w", err)
	}

	extraArtifactIDs := make([]string, 0, len(in.ExtraArtifacts))
	for _, extra := range in.ExtraArtifacts {
		extraArt, err := a.store.Save(ctx, SaveInput{
			RunID:       in.RunID,
			Name:        extra.Name,
			ContentType: extra.ContentType,
			Body:        bytes.NewReader(extra.Body),
		})
		if err != nil {
			telemetry.IncArtifactWriteFailure()
			return nil, nil, fmt.Errorf("save extra artifact %q: %w", extra.Name, err)
		}
		extraArtifactIDs = append(extraArtifactIDs, extraArt.ArtifactID)
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
		return nil, nil, fmt.Errorf("insert tool_call: %w", err)
	}
	telemetry.IncToolCall(in.ToolName, status)
	return tc, extraArtifactIDs, nil
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

func (a *AuditService) ReplayResponseWithRequestCheck(ctx context.Context, runID, toolName, idempotencyKey string, request any, out any) (*db.ToolCall, bool, error) {
	tc, err := a.db.GetSuccessfulToolCallByIdempotency(ctx, runID, toolName, idempotencyKey)
	if err != nil {
		return nil, false, err
	}
	if tc == nil {
		return nil, false, nil
	}
	if tc.RequestArtifactID == nil {
		return nil, false, fmt.Errorf("request artifact missing for idempotency check")
	}

	storedReq, err := a.store.Read(ctx, *tc.RequestArtifactID)
	if err != nil {
		return nil, false, err
	}
	currentReq, err := json.Marshal(request)
	if err != nil {
		return nil, false, fmt.Errorf("marshal request for idempotency check: %w", err)
	}

	var normalizedStored bytes.Buffer
	if err := json.Compact(&normalizedStored, storedReq); err != nil {
		return nil, false, fmt.Errorf("normalize stored request: %w", err)
	}
	var normalizedCurrent bytes.Buffer
	if err := json.Compact(&normalizedCurrent, currentReq); err != nil {
		return nil, false, fmt.Errorf("normalize current request: %w", err)
	}

	if !bytes.Equal(normalizedStored.Bytes(), normalizedCurrent.Bytes()) {
		return nil, false, &IdempotencyConflictError{}
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

// ListToolCallsByRun returns all tool calls associated with a run.
func (a *AuditService) ListToolCallsByRun(ctx context.Context, runID string) ([]*db.ToolCall, error) {
	return a.db.ListToolCallsByRun(ctx, runID)
}

// ListArtifactsByRun returns all artifacts associated with a run.
func (a *AuditService) ListArtifactsByRun(ctx context.Context, runID string) ([]*db.Artifact, error) {
	return a.store.ListByRun(ctx, runID)
}

// GetArtifactByRunAndID returns artifact metadata scoped to a run.
func (a *AuditService) GetArtifactByRunAndID(ctx context.Context, runID, artifactID string) (*db.Artifact, error) {
	return a.store.GetByRunAndID(ctx, runID, artifactID)
}

func (a *AuditService) CreateApproval(ctx context.Context, runID, scope string, payload any) (*db.Approval, error) {
	approvalID := uuid.New().String()
	now := time.Now().UTC()

	item := &db.Approval{
		ApprovalID:  approvalID,
		RunID:       runID,
		Scope:       scope,
		Status:      "requested",
		RequestedAt: now,
		CreatedAt:   now,
	}
	if err := a.db.InsertApproval(ctx, item); err != nil {
		return nil, err
	}

	var payloadArtifactID *string
	if payload != nil {
		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal approval payload: %w", err)
		}
		art, err := a.store.Save(ctx, SaveInput{
			RunID:       runID,
			Name:        "approval." + approvalID + ".payload.json",
			ContentType: "application/json",
			Body:        bytes.NewReader(payloadJSON),
		})
		if err != nil {
			telemetry.IncArtifactWriteFailure()
			return nil, fmt.Errorf("save approval payload artifact: %w", err)
		}
		payloadArtifactID = &art.ArtifactID
	}

	decision := &db.Decision{
		DecisionID:        uuid.New().String(),
		RunID:             runID,
		Actor:             "system",
		DecisionType:      "approval_requested",
		PayloadArtifactID: payloadArtifactID,
		CreatedAt:         now,
	}
	if err := a.db.InsertDecision(ctx, decision); err != nil {
		return nil, err
	}

	return item, nil
}

func (a *AuditService) GetApproval(ctx context.Context, approvalID string) (*db.Approval, error) {
	return a.db.GetApproval(ctx, approvalID)
}

func (a *AuditService) ListApprovalsByRun(ctx context.Context, runID string) ([]*db.Approval, error) {
	return a.db.ListApprovalsByRun(ctx, runID)
}

func (a *AuditService) ResolveApproval(ctx context.Context, approvalID, runID, status, approver string) (*db.Approval, error) {
	now := time.Now().UTC()
	if err := a.db.UpdateApprovalDecision(ctx, approvalID, status, &now, &approver); err != nil {
		return nil, err
	}

	decisionType := "approval_rejected"
	if status == "approved" {
		decisionType = "approval_approved"
	}
	if err := a.db.InsertDecision(ctx, &db.Decision{
		DecisionID:   uuid.New().String(),
		RunID:        runID,
		Actor:        approver,
		DecisionType: decisionType,
		CreatedAt:    now,
	}); err != nil {
		return nil, err
	}

	return a.db.GetApproval(ctx, approvalID)
}
