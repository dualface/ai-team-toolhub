package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/toolhub/toolhub/internal/codeops"
	"github.com/toolhub/toolhub/internal/core"
	"github.com/toolhub/toolhub/internal/db"
	gh "github.com/toolhub/toolhub/internal/github"
	"github.com/toolhub/toolhub/internal/qa"
	"github.com/toolhub/toolhub/internal/telemetry"
)

type Server struct {
	runs   *core.RunService
	audit  *core.AuditService
	policy *core.Policy
	gh     *gh.Client
	srv    *http.Server
	logger *slog.Logger
	mode   core.BatchMode
	build  BuildInfo
	qa     *qa.Runner
	code   *codeops.Runner
}

type BuildInfo struct {
	Version         string
	GitCommit       string
	BuildTime       string
	ContractVersion string
}

const maxRequestBodyBytes = 1 << 20

const maxArtifactContentBytes = 10 * 1024 * 1024

type ctxKey string

const ctxKeyRequestID ctxKey = "request_id"

// RequestIDFromContext extracts the request_id from context, or returns empty string.
func RequestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyRequestID).(string); ok {
		return v
	}
	return ""
}

func NewServer(addr string, runs *core.RunService, audit *core.AuditService, policy *core.Policy, ghClient *gh.Client, qaRunner *qa.Runner, codeRunner *codeops.Runner, logger *slog.Logger, mode core.BatchMode, build BuildInfo) *Server {
	s := &Server{
		runs:   runs,
		audit:  audit,
		policy: policy,
		gh:     ghClient,
		qa:     qaRunner,
		code:   codeRunner,
		logger: logger,
		mode:   mode,
		build:  build,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /metrics", s.handleMetrics)
	mux.HandleFunc("GET /version", s.handleVersion)
	mux.HandleFunc("POST /api/v1/runs", s.handleCreateRun)
	mux.HandleFunc("GET /api/v1/runs/{runID}", s.handleGetRun)
	mux.HandleFunc("POST /api/v1/runs/{runID}/approvals", s.handleCreateApproval)
	mux.HandleFunc("GET /api/v1/runs/{runID}/approvals", s.handleListApprovals)
	mux.HandleFunc("GET /api/v1/runs/{runID}/approvals/{approvalID}", s.handleGetApproval)
	mux.HandleFunc("POST /api/v1/runs/{runID}/approvals/{approvalID}/approve", s.handleApproveApproval)
	mux.HandleFunc("POST /api/v1/runs/{runID}/approvals/{approvalID}/reject", s.handleRejectApproval)
	mux.HandleFunc("POST /api/v1/runs/{runID}/code/patch", s.handleGeneratePatch)
	mux.HandleFunc("POST /api/v1/runs/{runID}/code/branch-pr", s.handleCodeBranchPR)
	mux.HandleFunc("POST /api/v1/runs/{runID}/code/repair-loop", s.handleCodeRepairLoop)
	mux.HandleFunc("GET /api/v1/runs/{runID}/tool-calls", s.handleListToolCalls)
	mux.HandleFunc("GET /api/v1/runs/{runID}/artifacts", s.handleListArtifacts)
	mux.HandleFunc("GET /api/v1/runs/{runID}/artifacts/{artifactID}", s.handleGetArtifact)
	mux.HandleFunc("GET /api/v1/runs/{runID}/artifacts/{artifactID}/content", s.handleGetArtifactContent)
	mux.HandleFunc("POST /api/v1/runs/{runID}/issues", s.handleCreateIssue)
	mux.HandleFunc("POST /api/v1/runs/{runID}/issues/batch", s.handleBatchCreateIssues)
	mux.HandleFunc("POST /api/v1/runs/{runID}/qa/test", s.handleQATest)
	mux.HandleFunc("POST /api/v1/runs/{runID}/qa/lint", s.handleQALint)
	mux.HandleFunc("GET /api/v1/runs/{runID}/prs/{prNumber}", s.handleGetPR)
	mux.HandleFunc("GET /api/v1/runs/{runID}/prs/{prNumber}/files", s.handleListPRFiles)
	mux.HandleFunc("POST /api/v1/runs/{runID}/prs/{prNumber}/comment", s.handleCreatePRComment)

	s.srv = &http.Server{
		Addr:         addr,
		Handler:      withLogging(logger, mux),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	return s
}

func (s *Server) ListenAndServe() error {
	s.logger.Info("http server starting", "addr", s.srv.Addr)
	ln, err := net.Listen("tcp", s.srv.Addr)
	if err != nil {
		return err
	}
	return s.srv.Serve(ln)
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, telemetry.RenderPrometheus())
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"version":          s.build.Version,
		"git_commit":       s.build.GitCommit,
		"build_time":       s.build.BuildTime,
		"contract_version": s.build.ContractVersion,
	})
}

type createRunBody struct {
	Repo    string `json:"repo"`
	Purpose string `json:"purpose"`
}

type createApprovalBody struct {
	Scope   string   `json:"scope"`
	Paths   []string `json:"paths,omitempty"`
	Payload any      `json:"payload,omitempty"`
}

type resolveApprovalBody struct {
	Approver string `json:"approver"`
}

func (s *Server) handleCreateRun(w http.ResponseWriter, r *http.Request) {
	var body createRunBody
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}

	if err := s.policy.CheckRepo(body.Repo); err != nil {
		writeErr(w, http.StatusForbidden, err.Error())
		return
	}

	run, err := s.runs.CreateRun(r.Context(), core.CreateRunRequest{
		Repo:    body.Repo,
		Purpose: body.Purpose,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, run)
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runID")
	run, err := s.runs.GetRun(r.Context(), runID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if run == nil {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) handleCreateApproval(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runID")
	run, err := s.runs.GetRun(r.Context(), runID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if run == nil {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}

	var body createApprovalBody
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if strings.TrimSpace(body.Scope) == "" {
		writeErr(w, http.StatusBadRequest, "scope is required")
		return
	}
	if err := s.policy.CheckPaths(body.Paths); err != nil {
		writeErr(w, http.StatusForbidden, err.Error())
		return
	}
	if s.policy.RequiresApproval(body.Paths) && body.Scope != "path_change" {
		writeErr(w, http.StatusBadRequest, "scope must be path_change for approval-required paths")
		return
	}

	payload := map[string]any{"payload": body.Payload, "paths": body.Paths}
	item, err := s.audit.CreateApproval(r.Context(), runID, body.Scope, payload)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) handleListApprovals(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runID")
	run, err := s.runs.GetRun(r.Context(), runID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if run == nil {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}

	items, err := s.audit.ListApprovalsByRun(r.Context(), runID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleGetApproval(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runID")
	approvalID := r.PathValue("approvalID")

	run, err := s.runs.GetRun(r.Context(), runID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if run == nil {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}

	item, err := s.audit.GetApproval(r.Context(), approvalID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if item == nil || item.RunID != runID {
		writeErr(w, http.StatusNotFound, "approval not found")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleApproveApproval(w http.ResponseWriter, r *http.Request) {
	s.handleResolveApproval(w, r, "approved")
}

func (s *Server) handleRejectApproval(w http.ResponseWriter, r *http.Request) {
	s.handleResolveApproval(w, r, "rejected")
}

func (s *Server) handleResolveApproval(w http.ResponseWriter, r *http.Request, status string) {
	runID := r.PathValue("runID")
	approvalID := r.PathValue("approvalID")

	run, err := s.runs.GetRun(r.Context(), runID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if run == nil {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}

	current, err := s.audit.GetApproval(r.Context(), approvalID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if current == nil || current.RunID != runID {
		writeErr(w, http.StatusNotFound, "approval not found")
		return
	}

	var body resolveApprovalBody
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if strings.TrimSpace(body.Approver) == "" {
		writeErr(w, http.StatusBadRequest, "approver is required")
		return
	}

	item, err := s.audit.ResolveApproval(r.Context(), approvalID, runID, status, body.Approver)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleListToolCalls(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runID")
	run, err := s.runs.GetRun(r.Context(), runID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if run == nil {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}

	toolCalls, err := s.audit.ListToolCallsByRun(r.Context(), runID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toolCalls)
}

func (s *Server) handleListArtifacts(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runID")
	run, err := s.runs.GetRun(r.Context(), runID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if run == nil {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}

	artifacts, err := s.audit.ListArtifactsByRun(r.Context(), runID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, artifacts)
}

func (s *Server) handleGetArtifact(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runID")
	artifactID := r.PathValue("artifactID")

	run, err := s.runs.GetRun(r.Context(), runID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if run == nil {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}

	art, err := s.audit.GetArtifactByRunAndID(r.Context(), runID, artifactID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if art == nil {
		writeErr(w, http.StatusNotFound, "artifact not found")
		return
	}
	writeJSON(w, http.StatusOK, art)
}

func (s *Server) handleGetArtifactContent(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runID")
	artifactID := r.PathValue("artifactID")

	run, err := s.runs.GetRun(r.Context(), runID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if run == nil {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}

	art, err := s.audit.GetArtifactByRunAndID(r.Context(), runID, artifactID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if art == nil {
		writeErr(w, http.StatusNotFound, "artifact not found")
		return
	}

	if !strings.HasPrefix(art.URI, "file://") {
		writeErr(w, http.StatusInternalServerError, "unsupported artifact uri")
		return
	}

	path := strings.TrimPrefix(art.URI, "file://")
	f, err := os.Open(path)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "read artifact file failed")
		return
	}
	defer f.Close()

	contentType := strings.TrimSpace(art.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)

	if _, err := io.Copy(w, io.LimitReader(f, maxArtifactContentBytes)); err != nil {
		s.logger.Error("stream artifact content failed",
			"request_id", RequestIDFromContext(r.Context()),
			"run_id", runID,
			"artifact_id", artifactID,
			"err", err,
		)
	}
}

type createIssueBody struct {
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	Labels []string `json:"labels,omitempty"`
	DryRun bool     `json:"dry_run,omitempty"`
}

type dryRunIssuePreview struct {
	Repo   string   `json:"repo"`
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	Labels []string `json:"labels,omitempty"`
}

type prCommentBody struct {
	Body   string `json:"body"`
	DryRun bool   `json:"dry_run,omitempty"`
}

type qaBody struct {
	DryRun bool `json:"dry_run,omitempty"`
}

type codePatchBody struct {
	Path            string `json:"path"`
	OriginalContent string `json:"original_content"`
	ModifiedContent string `json:"modified_content"`
	DryRun          bool   `json:"dry_run,omitempty"`
}

type codeBranchPRBody struct {
	ApprovalID    string               `json:"approval_id"`
	BaseBranch    string               `json:"base_branch"`
	HeadBranch    string               `json:"head_branch"`
	CommitMessage string               `json:"commit_message"`
	PRTitle       string               `json:"pr_title"`
	PRBody        string               `json:"pr_body,omitempty"`
	Files         []codeops.FileChange `json:"files"`
	DryRun        bool                 `json:"dry_run,omitempty"`
}

type codeRepairLoopBody struct {
	ApprovalID    string               `json:"approval_id"`
	BaseBranch    string               `json:"base_branch"`
	HeadBranch    string               `json:"head_branch"`
	CommitMessage string               `json:"commit_message"`
	PRTitle       string               `json:"pr_title"`
	PRBody        string               `json:"pr_body,omitempty"`
	Files         []codeops.FileChange `json:"files"`
	MaxIterations int                  `json:"max_iterations,omitempty"`
	DryRun        bool                 `json:"dry_run,omitempty"`
}

func (s *Server) handleGeneratePatch(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() { telemetry.ObserveToolDuration("code.patch.generate", time.Since(start)) }()

	runID := r.PathValue("runID")
	run, err := s.runs.GetRun(r.Context(), runID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if run == nil {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}
	if err := s.policy.CheckTool("code.patch.generate"); err != nil {
		writeErr(w, http.StatusForbidden, err.Error())
		return
	}

	var body codePatchBody
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if strings.TrimSpace(body.Path) == "" {
		writeErr(w, http.StatusBadRequest, "path is required")
		return
	}

	patchText := core.GenerateUnifiedDiff(body.Path, body.OriginalContent, body.ModifiedContent)
	lineDelta := core.CountContentLines(body.ModifiedContent) - core.CountContentLines(body.OriginalContent)

	response := map[string]any{
		"path":       body.Path,
		"patch":      patchText,
		"line_delta": lineDelta,
	}

	tc, extraIDs, auditErr := s.audit.Record(r.Context(), core.RecordInput{
		RunID:    runID,
		ToolName: "code.patch.generate",
		Request:  body,
		Response: response,
		Err:      nil,
		ExtraArtifacts: []core.ExtraArtifact{
			{Name: "code.patch.generate.patch.diff", ContentType: "text/x-diff", Body: []byte(patchText)},
		},
	})
	if auditErr != nil {
		writeErr(w, http.StatusInternalServerError, "audit record failed: "+auditErr.Error())
		return
	}

	result := map[string]any{
		"path":       body.Path,
		"patch":      patchText,
		"line_delta": lineDelta,
	}
	if len(extraIDs) > 0 {
		result["patch_artifact_id"] = extraIDs[0]
	}

	writeJSON(w, http.StatusOK, core.ToolEnvelope{
		OK: true,
		Meta: core.ToolMeta{
			RunID:        runID,
			ToolCallID:   tc.ToolCallID,
			EvidenceHash: tc.EvidenceHash,
			DryRun:       body.DryRun,
		},
		Result: result,
	})
}

func (s *Server) handleCodeBranchPR(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() { telemetry.ObserveToolDuration("code.branch_pr.create", time.Since(start)) }()

	runID := r.PathValue("runID")
	run, err := s.runs.GetRun(r.Context(), runID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if run == nil {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}
	if err := s.policy.CheckTool("code.branch_pr.create"); err != nil {
		writeErr(w, http.StatusForbidden, err.Error())
		return
	}
	if s.code == nil {
		writeErr(w, http.StatusInternalServerError, "code runner is not configured")
		return
	}

	var body codeBranchPRBody
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if strings.TrimSpace(body.ApprovalID) == "" {
		writeErr(w, http.StatusBadRequest, "approval_id is required")
		return
	}
	approval, err := s.audit.GetApproval(r.Context(), body.ApprovalID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if approval == nil || approval.RunID != runID {
		writeErr(w, http.StatusNotFound, "approval not found")
		return
	}
	if approval.Status != "approved" {
		writeErr(w, http.StatusForbidden, "approval is not approved")
		return
	}

	paths := make([]string, 0, len(body.Files))
	for _, f := range body.Files {
		paths = append(paths, f.Path)
	}
	if err := s.policy.CheckPaths(paths); err != nil {
		writeErr(w, http.StatusForbidden, err.Error())
		return
	}

	patches := make([]string, 0, len(body.Files))
	for _, f := range body.Files {
		patches = append(patches, core.GenerateUnifiedDiff(f.Path, f.OriginalContent, f.ModifiedContent))
	}
	combinedPatch := strings.Join(patches, "\n")

	codeResult, runErr := s.code.Execute(r.Context(), codeops.Request{
		BaseBranch:    body.BaseBranch,
		HeadBranch:    body.HeadBranch,
		CommitMessage: body.CommitMessage,
		Files:         body.Files,
		DryRun:        body.DryRun,
	})
	if codeResult == nil {
		codeResult = &codeops.Result{}
	}

	result := map[string]any{
		"base_branch":      body.BaseBranch,
		"head_branch":      body.HeadBranch,
		"planned_commands": codeResult.PlannedCommands,
		"commit_hash":      codeResult.CommitHash,
	}

	if runErr == nil && !body.DryRun {
		owner, repo := splitRepo(run.Repo)
		pr, prErr := s.gh.CreatePullRequest(r.Context(), owner, repo, gh.CreatePullRequestInput{
			Title: body.PRTitle,
			Head:  body.HeadBranch,
			Base:  body.BaseBranch,
			Body:  body.PRBody,
		})
		if prErr != nil {
			runErr = prErr
		} else {
			result["pull_request"] = pr
		}
	}

	tc, extraIDs, auditErr := s.audit.Record(r.Context(), core.RecordInput{
		RunID:    runID,
		ToolName: "code.branch_pr.create",
		Request:  body,
		Response: result,
		Err:      runErr,
		ExtraArtifacts: []core.ExtraArtifact{
			{Name: "code.branch_pr.create.patch.diff", ContentType: "text/x-diff", Body: []byte(combinedPatch)},
		},
	})
	if auditErr != nil {
		writeErr(w, http.StatusInternalServerError, "audit record failed: "+auditErr.Error())
		return
	}
	if len(extraIDs) > 0 {
		result["patch_artifact_id"] = extraIDs[0]
	}

	if runErr != nil {
		writeMappedErr(w, runErr, http.StatusBadGateway)
		return
	}

	writeJSON(w, http.StatusOK, core.ToolEnvelope{
		OK:     true,
		Meta:   core.ToolMeta{RunID: runID, ToolCallID: tc.ToolCallID, EvidenceHash: tc.EvidenceHash, DryRun: body.DryRun},
		Result: result,
	})
}

func (s *Server) handleCodeRepairLoop(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() { telemetry.ObserveToolDuration("code.repair_loop", time.Since(start)) }()

	runID := r.PathValue("runID")
	run, err := s.runs.GetRun(r.Context(), runID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if run == nil {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}
	if err := s.policy.CheckTool("code.repair_loop"); err != nil {
		writeErr(w, http.StatusForbidden, err.Error())
		return
	}
	if s.code == nil {
		writeErr(w, http.StatusInternalServerError, "code runner is not configured")
		return
	}
	if s.qa == nil {
		writeErr(w, http.StatusInternalServerError, "qa runner is not configured")
		return
	}

	var body codeRepairLoopBody
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if body.MaxIterations <= 0 {
		body.MaxIterations = 1
	}
	if body.MaxIterations > 3 {
		writeErr(w, http.StatusBadRequest, "max_iterations cannot exceed 3")
		return
	}
	if strings.TrimSpace(body.ApprovalID) == "" {
		writeErr(w, http.StatusBadRequest, "approval_id is required")
		return
	}

	approval, err := s.audit.GetApproval(r.Context(), body.ApprovalID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if approval == nil || approval.RunID != runID {
		writeErr(w, http.StatusNotFound, "approval not found")
		return
	}
	if approval.Status != "approved" {
		writeErr(w, http.StatusForbidden, "approval is not approved")
		return
	}

	paths := make([]string, 0, len(body.Files))
	for _, f := range body.Files {
		paths = append(paths, f.Path)
	}
	if err := s.policy.CheckPaths(paths); err != nil {
		writeErr(w, http.StatusForbidden, err.Error())
		return
	}

	step, err := s.audit.StartStep(r.Context(), runID, "code_repair_loop", "repair_loop")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.audit.RecordDecision(r.Context(), runID, &step.StepID, "system", "repair_loop_started", map[string]any{"max_iterations": body.MaxIterations})

	codeResult, runErr := s.code.Execute(r.Context(), codeops.Request{
		BaseBranch:    body.BaseBranch,
		HeadBranch:    body.HeadBranch,
		CommitMessage: body.CommitMessage,
		Files:         body.Files,
		DryRun:        body.DryRun,
	})
	if codeResult == nil {
		codeResult = &codeops.Result{}
	}

	iterationsRun := 0
	qaPassed := false
	qaAttempts := make([]map[string]any, 0, body.MaxIterations)

	result := map[string]any{
		"iterations_requested": body.MaxIterations,
		"iterations_run":       iterationsRun,
		"base_branch":          body.BaseBranch,
		"head_branch":          body.HeadBranch,
		"planned_commands":     codeResult.PlannedCommands,
		"commit_hash":          codeResult.CommitHash,
		"qa_passed":            qaPassed,
		"status":               "completed",
	}

	if runErr == nil && !body.DryRun {
		for i := 1; i <= body.MaxIterations; i++ {
			iterationsRun = i

			testReport, testErr := s.qa.Run(r.Context(), qa.KindTest, false)
			lintReport, lintErr := s.qa.Run(r.Context(), qa.KindLint, false)

			attempt := map[string]any{
				"iteration":   i,
				"test_status": string(qa.DeriveStatus(testReport, testErr, false)),
				"lint_status": string(qa.DeriveStatus(lintReport, lintErr, false)),
				"test_report": testReport,
				"lint_report": lintReport,
			}
			if testErr != nil {
				attempt["test_error"] = testErr.Error()
			}
			if lintErr != nil {
				attempt["lint_error"] = lintErr.Error()
			}
			qaAttempts = append(qaAttempts, attempt)
			_ = s.audit.RecordDecision(r.Context(), runID, &step.StepID, "system", "repair_loop_iteration", attempt)

			if testErr == nil && lintErr == nil {
				qaPassed = true
				break
			}
		}

		if !qaPassed {
			result["status"] = "failed"
			result["qa_failure_reason"] = fmt.Sprintf("qa checks failed after %d iteration(s)", iterationsRun)

			rollback, rollbackErr := s.code.RollbackBranch(r.Context(), body.BaseBranch, body.HeadBranch, false)
			if rollback != nil {
				result["rollback_planned_commands"] = rollback.PlannedCommands
			}
			if rollbackErr != nil {
				result["rollback_error"] = rollbackErr.Error()
			}
			runErr = fmt.Errorf("qa checks failed")
		}
	}

	result["iterations_run"] = iterationsRun
	result["qa_passed"] = qaPassed
	if len(qaAttempts) > 0 {
		result["qa_attempts"] = qaAttempts
	}

	if runErr == nil && !body.DryRun && qaPassed {
		owner, repo := splitRepo(run.Repo)
		pr, prErr := s.gh.CreatePullRequest(r.Context(), owner, repo, gh.CreatePullRequestInput{
			Title: body.PRTitle,
			Head:  body.HeadBranch,
			Base:  body.BaseBranch,
			Body:  body.PRBody,
		})
		if prErr != nil {
			runErr = prErr
		} else {
			result["pull_request"] = pr
		}
	}

	if runErr != nil {
		result["status"] = "failed"
	} else if body.DryRun {
		result["status"] = "dry_run"
	}

	tc, _, auditErr := s.audit.Record(r.Context(), core.RecordInput{
		RunID:    runID,
		ToolName: "code.repair_loop",
		Request:  body,
		Response: result,
		Err:      runErr,
	})
	if auditErr != nil {
		writeErr(w, http.StatusInternalServerError, "audit record failed: "+auditErr.Error())
		return
	}

	decisionType := "repair_loop_completed"
	stepStatus := "completed"
	if runErr != nil {
		decisionType = "repair_loop_failed"
		stepStatus = "failed"
	}
	_ = s.audit.RecordDecision(r.Context(), runID, &step.StepID, "system", decisionType, result)
	_ = s.audit.FinishStep(r.Context(), step.StepID, stepStatus)

	if runErr != nil {
		writeMappedErr(w, runErr, http.StatusBadGateway)
		return
	}

	writeJSON(w, http.StatusOK, core.ToolEnvelope{
		OK:     true,
		Meta:   core.ToolMeta{RunID: runID, ToolCallID: tc.ToolCallID, EvidenceHash: tc.EvidenceHash, DryRun: body.DryRun},
		Result: result,
	})
}

func (s *Server) handleQATest(w http.ResponseWriter, r *http.Request) {
	s.handleQA(w, r, qa.KindTest)
}

func (s *Server) handleQALint(w http.ResponseWriter, r *http.Request) {
	s.handleQA(w, r, qa.KindLint)
}

func (s *Server) handleQA(w http.ResponseWriter, r *http.Request, kind qa.Kind) {
	start := time.Now()
	defer func() { telemetry.ObserveToolDuration(string(kind), time.Since(start)) }()

	runID := r.PathValue("runID")
	run, err := s.runs.GetRun(r.Context(), runID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if run == nil {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}
	if err := s.policy.CheckTool(string(kind)); err != nil {
		writeErr(w, http.StatusForbidden, err.Error())
		return
	}

	var body qaBody
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}

	report, runErr := s.qa.Run(r.Context(), kind, body.DryRun)
	if runErr != nil && report.Command == "" {
		_, _, auditErr := s.audit.Record(r.Context(), core.RecordInput{
			RunID:    runID,
			ToolName: string(kind),
			Request:  body,
			Response: nil,
			Err:      runErr,
		})
		if auditErr != nil {
			s.logger.Error("audit record failed",
				"request_id", RequestIDFromContext(r.Context()),
				"err", auditErr,
			)
		}
		s.logger.Error("tool call failed",
			"request_id", RequestIDFromContext(r.Context()),
			"run_id", runID,
			"tool_name", string(kind),
			"err", runErr,
		)
		writeMappedErr(w, runErr, http.StatusBadRequest)
		return
	}

	reportJSON, reportJSONErr := json.Marshal(report)
	if reportJSONErr != nil {
		writeErr(w, http.StatusInternalServerError, "marshal qa report failed: "+reportJSONErr.Error())
		return
	}

	tc, extraArtifactIDs, auditErr := s.audit.Record(r.Context(), core.RecordInput{
		RunID:    runID,
		ToolName: string(kind),
		Request:  body,
		Response: map[string]any{"report": report},
		Err:      runErr,
		ExtraArtifacts: []core.ExtraArtifact{
			{Name: fmt.Sprintf("%s.stdout.txt", kind), ContentType: "text/plain", Body: []byte(report.Stdout)},
			{Name: fmt.Sprintf("%s.stderr.txt", kind), ContentType: "text/plain", Body: []byte(report.Stderr)},
			{Name: fmt.Sprintf("%s.report.json", kind), ContentType: "application/json", Body: reportJSON},
		},
	})
	if auditErr != nil {
		writeErr(w, http.StatusInternalServerError, "audit record failed: "+auditErr.Error())
		return
	}

	qaArtifacts := &core.QAArtifacts{}
	if len(extraArtifactIDs) > 0 {
		qaArtifacts.StdoutArtifactID = extraArtifactIDs[0]
	}
	if len(extraArtifactIDs) > 1 {
		qaArtifacts.StderrArtifactID = extraArtifactIDs[1]
	}
	if len(extraArtifactIDs) > 2 {
		qaArtifacts.ReportArtifactID = extraArtifactIDs[2]
	}

	status := qa.DeriveStatus(report, runErr, body.DryRun)
	if runErr != nil {
		s.logger.Error("tool call failed",
			"request_id", RequestIDFromContext(r.Context()),
			"run_id", runID,
			"tool_call_id", tc.ToolCallID,
			"tool_name", string(kind),
			"repo", run.Repo,
			"err", runErr,
		)
	} else {
		s.logger.Info("tool call completed",
			"request_id", RequestIDFromContext(r.Context()),
			"run_id", runID,
			"tool_call_id", tc.ToolCallID,
			"tool_name", string(kind),
			"repo", run.Repo,
			"dry_run", body.DryRun,
		)
	}

	writeJSON(w, http.StatusOK, core.ToolEnvelope{
		OK: runErr == nil,
		Meta: core.ToolMeta{
			RunID:        runID,
			ToolCallID:   tc.ToolCallID,
			EvidenceHash: tc.EvidenceHash,
			DryRun:       body.DryRun,
			QAArtifacts:  qaArtifacts,
		},
		Result: map[string]any{"status": string(status), "report": report},
		Error: func() *core.ToolError {
			if runErr == nil {
				return nil
			}
			return &core.ToolError{Code: string(status), Message: runErr.Error()}
		}(),
	})
}

func (s *Server) handleGetPR(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() { telemetry.ObserveToolDuration("github.pr.get", time.Since(start)) }()

	runID := r.PathValue("runID")
	prNumberRaw := r.PathValue("prNumber")
	prNumber := 0
	if _, err := fmt.Sscanf(prNumberRaw, "%d", &prNumber); err != nil || prNumber <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid prNumber")
		return
	}

	run, err := s.runs.GetRun(r.Context(), runID)
	if err != nil {
		writeMappedErr(w, err, http.StatusInternalServerError)
		return
	}
	if err := s.policy.CheckTool("github.pr.get"); err != nil {
		writeErr(w, http.StatusForbidden, err.Error())
		return
	}
	if run == nil {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}

	owner, repo := splitRepo(run.Repo)
	pr, ghErr := s.gh.GetPullRequest(r.Context(), owner, repo, prNumber)

	tc, _, auditErr := s.audit.Record(r.Context(), core.RecordInput{
		RunID:    runID,
		ToolName: "github.pr.get",
		Request:  map[string]any{"pr_number": prNumber},
		Response: pr,
		Err:      ghErr,
	})
	if auditErr != nil {
		writeErr(w, http.StatusInternalServerError, "audit record failed: "+auditErr.Error())
		return
	}

	s.logger.Info("tool call completed",
		"request_id", RequestIDFromContext(r.Context()),
		"run_id", runID,
		"tool_call_id", tc.ToolCallID,
		"tool_name", "github.pr.get",
		"repo", run.Repo,
		"pr_number", prNumber,
	)

	if ghErr != nil {
		writeMappedErr(w, ghErr, http.StatusBadGateway)
		return
	}

	writeJSON(w, http.StatusOK, core.ToolEnvelope{
		OK: true,
		Meta: core.ToolMeta{
			RunID:        runID,
			ToolCallID:   tc.ToolCallID,
			EvidenceHash: tc.EvidenceHash,
			DryRun:       false,
		},
		Result: pr,
	})
}

func (s *Server) handleListPRFiles(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() { telemetry.ObserveToolDuration("github.pr.files.list", time.Since(start)) }()

	runID := r.PathValue("runID")
	prNumberRaw := r.PathValue("prNumber")
	prNumber := 0
	if _, err := fmt.Sscanf(prNumberRaw, "%d", &prNumber); err != nil || prNumber <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid prNumber")
		return
	}

	run, err := s.runs.GetRun(r.Context(), runID)
	if err != nil {
		writeMappedErr(w, err, http.StatusInternalServerError)
		return
	}
	if run == nil {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}
	if err := s.policy.CheckTool("github.pr.files.list"); err != nil {
		writeErr(w, http.StatusForbidden, err.Error())
		return
	}

	owner, repo := splitRepo(run.Repo)
	files, ghErr := s.gh.ListPullRequestFiles(r.Context(), owner, repo, prNumber)

	tc, _, auditErr := s.audit.Record(r.Context(), core.RecordInput{
		RunID:    runID,
		ToolName: "github.pr.files.list",
		Request:  map[string]any{"pr_number": prNumber},
		Response: map[string]any{"files": files, "count": len(files)},
		Err:      ghErr,
	})
	if auditErr != nil {
		writeErr(w, http.StatusInternalServerError, "audit record failed: "+auditErr.Error())
		return
	}

	s.logger.Info("tool call completed",
		"request_id", RequestIDFromContext(r.Context()),
		"run_id", runID,
		"tool_call_id", tc.ToolCallID,
		"tool_name", "github.pr.files.list",
		"repo", run.Repo,
		"pr_number", prNumber,
	)

	if ghErr != nil {
		writeMappedErr(w, ghErr, http.StatusBadGateway)
		return
	}

	writeJSON(w, http.StatusOK, core.ToolEnvelope{
		OK: true,
		Meta: core.ToolMeta{
			RunID:        runID,
			ToolCallID:   tc.ToolCallID,
			EvidenceHash: tc.EvidenceHash,
			DryRun:       false,
		},
		Result: map[string]any{"files": files, "count": len(files)},
	})
}

func (s *Server) handleCreateIssue(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() { telemetry.ObserveToolDuration("github.issues.create", time.Since(start)) }()

	runID := r.PathValue("runID")

	run, err := s.runs.GetRun(r.Context(), runID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if run == nil {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}

	var body createIssueBody
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if err := core.ValidateIssueInput(body.Title, body.Body, body.Labels); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	toolName := "github.issues.create"
	headerIdemKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	idemKey := headerIdemKey
	if idemKey == "" {
		idemKey, err = core.MakeIssueIdempotencyKey(runID, toolName, body.Title, body.Body, body.Labels, nil)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	var replayIssue gh.Issue
	var tc *db.ToolCall
	var replayed bool
	if headerIdemKey != "" {
		tc, replayed, err = s.audit.ReplayResponseWithRequestCheck(r.Context(), runID, toolName, idemKey, body, &replayIssue)
	} else {
		tc, replayed, err = s.audit.ReplayResponse(r.Context(), runID, toolName, idemKey, &replayIssue)
	}
	if err != nil {
		writeMappedErr(w, err, http.StatusInternalServerError)
		return
	}
	if replayed {
		w.Header().Set("Idempotency-Replayed", "true")
		writeJSON(w, http.StatusOK, core.ToolEnvelope{
			OK: true,
			Meta: core.ToolMeta{
				RunID:        runID,
				ToolCallID:   tc.ToolCallID,
				EvidenceHash: tc.EvidenceHash,
				DryRun:       false,
				Replayed:     true,
			},
			Result: &replayIssue,
		})
		return
	}

	owner, repo := splitRepo(run.Repo)

	var issue *gh.Issue
	var ghErr error
	if body.DryRun {
		issue = nil
	} else {
		issue, ghErr = s.gh.CreateIssue(r.Context(), owner, repo, gh.CreateIssueInput{
			Title:  body.Title,
			Body:   body.Body,
			Labels: body.Labels,
		})
	}

	tc, _, auditErr := s.audit.Record(r.Context(), core.RecordInput{
		RunID:    runID,
		ToolName: toolName,
		IdemKey:  &idemKey,
		Request:  body,
		Response: map[string]any{
			"issue": issue,
			"preview": dryRunIssuePreview{
				Repo:   run.Repo,
				Title:  body.Title,
				Body:   body.Body,
				Labels: body.Labels,
			},
		},
		Err: ghErr,
	})
	if auditErr != nil {
		writeErr(w, http.StatusInternalServerError, "audit record failed: "+auditErr.Error())
		return
	}

	toolLogger := s.logger.With(
		"request_id", RequestIDFromContext(r.Context()),
		"run_id", runID,
		"tool_call_id", tc.ToolCallID,
		"tool_name", toolName,
		"repo", run.Repo,
		"dry_run", body.DryRun,
	)

	if ghErr != nil {
		toolLogger.Error("tool call failed", "err", ghErr)
		writeMappedErr(w, ghErr, http.StatusBadGateway)
		return
	}

	toolLogger.Info("tool call completed")

	result := any(issue)
	if body.DryRun {
		result = map[string]any{
			"would_create": dryRunIssuePreview{
				Repo:   run.Repo,
				Title:  body.Title,
				Body:   body.Body,
				Labels: body.Labels,
			},
		}
	}

	writeJSON(w, http.StatusOK, core.ToolEnvelope{
		OK: ghErr == nil,
		Meta: core.ToolMeta{
			RunID:        runID,
			ToolCallID:   tc.ToolCallID,
			EvidenceHash: tc.EvidenceHash,
			DryRun:       body.DryRun,
		},
		Result: result,
	})
}

type batchCreateBody struct {
	Issues []createIssueBody `json:"issues"`
	DryRun bool              `json:"dry_run,omitempty"`
}

type batchResultJSON struct {
	Index    int       `json:"index"`
	Issue    *gh.Issue `json:"issue,omitempty"`
	Replayed bool      `json:"replayed,omitempty"`
	Error    string    `json:"error,omitempty"`
}

type batchResponseJSON struct {
	Status       string            `json:"status"`
	Mode         core.BatchMode    `json:"mode"`
	Total        int               `json:"total"`
	Processed    int               `json:"processed"`
	Errors       int               `json:"errors"`
	Replayed     int               `json:"replayed"`
	CreatedFresh int               `json:"created_fresh"`
	StoppedAt    *int              `json:"stopped_at,omitempty"`
	FailedReason string            `json:"failed_reason,omitempty"`
	Results      []batchResultJSON `json:"results"`
}

func (s *Server) handleBatchCreateIssues(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() { telemetry.ObserveToolDuration("github.issues.batch_create", time.Since(start)) }()

	runID := r.PathValue("runID")

	run, err := s.runs.GetRun(r.Context(), runID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if run == nil {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}

	var body batchCreateBody
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}

	if len(body.Issues) == 0 {
		writeErr(w, http.StatusBadRequest, "issues array is empty")
		return
	}
	if len(body.Issues) > core.MaxBatchSize {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("issues exceed %d items", core.MaxBatchSize))
		return
	}
	for i, iss := range body.Issues {
		if err := core.ValidateIssueInput(iss.Title, iss.Body, iss.Labels); err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("issue %d: %s", i, err.Error()))
			return
		}
	}

	owner, repo := splitRepo(run.Repo)

	out := make([]batchResultJSON, len(body.Issues))
	replayedCount := 0
	errCount := 0

	processed := 0
	for i, in := range body.Issues {
		processed = i + 1
		i2 := i
		idemKey, err := core.MakeIssueIdempotencyKey(runID, "github.issues.batch_create", in.Title, in.Body, in.Labels, &i2)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}

		var replayIssue gh.Issue
		tc, replayed, err := s.audit.ReplayResponse(r.Context(), runID, "github.issues.batch_create", idemKey, &replayIssue)
		if err != nil {
			writeMappedErr(w, err, http.StatusInternalServerError)
			return
		}

		if replayed {
			out[i] = batchResultJSON{Index: i, Issue: &replayIssue, Replayed: true}
			s.logger.Info("tool call replayed",
				"request_id", RequestIDFromContext(r.Context()),
				"run_id", runID,
				"tool_call_id", tc.ToolCallID,
				"tool_name", "github.issues.batch_create",
				"repo", run.Repo,
			)
			replayedCount++
			continue
		}

		var issue *gh.Issue
		var ghErr error
		if !body.DryRun {
			issue, ghErr = s.gh.CreateIssue(r.Context(), owner, repo, gh.CreateIssueInput{
				Title: in.Title, Body: in.Body, Labels: in.Labels,
			})
		}

		tc, _, auditErr := s.audit.Record(r.Context(), core.RecordInput{
			RunID:    runID,
			ToolName: "github.issues.batch_create",
			IdemKey:  &idemKey,
			Request:  in,
			Response: map[string]any{
				"issue": issue,
				"preview": dryRunIssuePreview{
					Repo:   run.Repo,
					Title:  in.Title,
					Body:   in.Body,
					Labels: in.Labels,
				},
			},
			Err: ghErr,
		})
		if auditErr != nil {
			writeErr(w, http.StatusInternalServerError, fmt.Sprintf("audit record failed at index %d: %s", i, auditErr.Error()))
			return
		}

		s.logger.Info("tool call completed",
			"request_id", RequestIDFromContext(r.Context()),
			"run_id", runID,
			"tool_call_id", tc.ToolCallID,
			"tool_name", "github.issues.batch_create",
			"repo", run.Repo,
			"index", i,
			"dry_run", body.DryRun,
		)

		out[i] = batchResultJSON{Index: i, Issue: issue}
		if ghErr != nil {
			out[i].Error = ghErr.Error()
			errCount++
			if s.mode == core.BatchModeStrict {
				stoppedAt := i
				status := core.DeriveBatchStatus(processed, replayedCount, errCount)
				writeJSON(w, http.StatusOK, batchResponseJSON{
					Status:       status,
					Mode:         s.mode,
					Total:        len(body.Issues),
					Processed:    processed,
					Errors:       errCount,
					Replayed:     replayedCount,
					CreatedFresh: processed - replayedCount,
					StoppedAt:    &stoppedAt,
					FailedReason: ghErr.Error(),
					Results:      out[:processed],
				})
				return
			}
		}
	}

	status := core.DeriveBatchStatus(len(body.Issues), replayedCount, errCount)

	writeJSON(w, http.StatusOK, core.ToolEnvelope{
		OK: errCount == 0,
		Meta: core.ToolMeta{
			RunID:        runID,
			ToolCallID:   "",
			EvidenceHash: "",
			DryRun:       body.DryRun,
		},
		Result: batchResponseJSON{
			Status:       status,
			Mode:         s.mode,
			Total:        len(body.Issues),
			Processed:    len(body.Issues),
			Errors:       errCount,
			Replayed:     replayedCount,
			CreatedFresh: len(body.Issues) - replayedCount,
			Results:      out,
		},
	})
}

func (s *Server) handleCreatePRComment(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() { telemetry.ObserveToolDuration("github.pr.comment.create", time.Since(start)) }()

	runID := r.PathValue("runID")
	prNumberRaw := r.PathValue("prNumber")
	prNumber := 0
	if _, err := fmt.Sscanf(prNumberRaw, "%d", &prNumber); err != nil || prNumber <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid prNumber")
		return
	}

	run, err := s.runs.GetRun(r.Context(), runID)
	if err != nil {
		writeMappedErr(w, err, http.StatusInternalServerError)
		return
	}
	if run == nil {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}

	var body prCommentBody
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeMappedErr(w, fmt.Errorf("invalid json: %w", err), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.Body) == "" {
		writeErr(w, http.StatusBadRequest, "body is required")
		return
	}

	toolName := "github.pr.comment.create"
	keyLabels := []string{fmt.Sprintf("pr:%d", prNumber)}
	headerIdemKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	idemKey := headerIdemKey
	if idemKey == "" {
		idemKey, err = core.MakeIssueIdempotencyKey(runID, toolName, fmt.Sprintf("pr-%d", prNumber), body.Body, keyLabels, nil)
		if err != nil {
			writeMappedErr(w, err, http.StatusInternalServerError)
			return
		}
	}

	var replay map[string]any
	var tcReplay *db.ToolCall
	var replayed bool
	if headerIdemKey != "" {
		tcReplay, replayed, err = s.audit.ReplayResponseWithRequestCheck(r.Context(), runID, toolName, idemKey, body, &replay)
	} else {
		tcReplay, replayed, err = s.audit.ReplayResponse(r.Context(), runID, toolName, idemKey, &replay)
	}
	if err != nil {
		writeMappedErr(w, err, http.StatusInternalServerError)
		return
	}
	if replayed {
		w.Header().Set("Idempotency-Replayed", "true")
		writeJSON(w, http.StatusOK, core.ToolEnvelope{
			OK:     true,
			Meta:   core.ToolMeta{RunID: runID, ToolCallID: tcReplay.ToolCallID, EvidenceHash: tcReplay.EvidenceHash, DryRun: false, Replayed: true},
			Result: replay,
		})
		return
	}

	owner, repo := splitRepo(run.Repo)
	var comment *gh.Comment
	var ghErr error
	if !body.DryRun {
		comment, ghErr = s.gh.CreatePRComment(r.Context(), owner, repo, prNumber, body.Body)
	}

	tc, _, auditErr := s.audit.Record(r.Context(), core.RecordInput{
		RunID:    runID,
		ToolName: toolName,
		IdemKey:  &idemKey,
		Request:  body,
		Response: map[string]any{
			"comment": comment,
			"preview": map[string]any{"repo": run.Repo, "pr_number": prNumber, "body": body.Body},
		},
		Err: ghErr,
	})
	if auditErr != nil {
		writeErr(w, http.StatusInternalServerError, "audit record failed: "+auditErr.Error())
		return
	}

	s.logger.Info("tool call completed",
		"request_id", RequestIDFromContext(r.Context()),
		"run_id", runID,
		"tool_call_id", tc.ToolCallID,
		"tool_name", toolName,
		"repo", run.Repo,
		"dry_run", body.DryRun,
		"pr_number", prNumber,
	)

	if ghErr != nil {
		writeMappedErr(w, ghErr, http.StatusBadGateway)
		return
	}

	result := any(comment)
	if body.DryRun {
		result = map[string]any{"would_comment": map[string]any{"repo": run.Repo, "pr_number": prNumber, "body": body.Body}}
	}
	writeJSON(w, http.StatusOK, core.ToolEnvelope{
		OK:     true,
		Meta:   core.ToolMeta{RunID: runID, ToolCallID: tc.ToolCallID, EvidenceHash: tc.EvidenceHash, DryRun: body.DryRun},
		Result: result,
	})
}

func splitRepo(fullRepo string) (string, string) {
	parts := strings.SplitN(fullRepo, "/", 2)
	if len(parts) != 2 {
		return fullRepo, ""
	}
	return parts[0], parts[1]
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	code := "internal_error"
	switch {
	case status == http.StatusBadRequest:
		code = "invalid_request_schema"
	case status == http.StatusForbidden:
		code = "forbidden"
	case status == http.StatusNotFound:
		code = "not_found"
	case status >= 500 && status < 600:
		code = "upstream_error"
	}
	writeJSON(w, status, map[string]string{"code": code, "message": msg})
}

func writeMappedErr(w http.ResponseWriter, err error, fallbackStatus int) {
	var apiErr *gh.APIError
	if errors.As(err, &apiErr) {
		mapped := core.MapError(apiErr, fallbackStatus)
		writeJSON(w, mapped.HTTPStatus, map[string]string{"code": mapped.Code, "message": mapped.Message})
		return
	}
	mapped := core.MapError(err, fallbackStatus)
	writeJSON(w, mapped.HTTPStatus, map[string]string{"code": mapped.Code, "message": mapped.Message})
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("request body must contain a single JSON object")
	}
	return nil
}

func withLogging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := uuid.New().String()
		ctx := context.WithValue(r.Context(), ctxKeyRequestID, requestID)
		r = r.WithContext(ctx)

		w.Header().Set("X-Request-ID", requestID)

		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		logger.Info("http request",
			"request_id", requestID,
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"duration", fmt.Sprintf("%dms", time.Since(start).Milliseconds()),
		)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
