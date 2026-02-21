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
	"strings"
	"time"

	"github.com/toolhub/toolhub/internal/core"
	gh "github.com/toolhub/toolhub/internal/github"
	"github.com/toolhub/toolhub/internal/qa"
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
}

type BuildInfo struct {
	Version   string
	GitCommit string
	BuildTime string
}

const maxRequestBodyBytes = 1 << 20

func NewServer(addr string, runs *core.RunService, audit *core.AuditService, policy *core.Policy, ghClient *gh.Client, qaRunner *qa.Runner, logger *slog.Logger, mode core.BatchMode, build BuildInfo) *Server {
	s := &Server{
		runs:   runs,
		audit:  audit,
		policy: policy,
		gh:     ghClient,
		qa:     qaRunner,
		logger: logger,
		mode:   mode,
		build:  build,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /version", s.handleVersion)
	mux.HandleFunc("POST /api/v1/runs", s.handleCreateRun)
	mux.HandleFunc("GET /api/v1/runs/{runID}", s.handleGetRun)
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

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"version":    s.build.Version,
		"git_commit": s.build.GitCommit,
		"build_time": s.build.BuildTime,
	})
}

type createRunBody struct {
	Repo    string `json:"repo"`
	Purpose string `json:"purpose"`
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

type createIssueBody struct {
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	Labels []string `json:"labels,omitempty"`
	DryRun bool     `json:"dry_run,omitempty"`
}

type toolResponseMeta struct {
	RunID        string `json:"run_id"`
	ToolCallID   string `json:"tool_call_id"`
	EvidenceHash string `json:"evidence_hash"`
	DryRun       bool   `json:"dry_run"`
}

type toolResponse struct {
	OK     bool             `json:"ok"`
	Meta   toolResponseMeta `json:"meta"`
	Result any              `json:"result"`
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

func (s *Server) handleQATest(w http.ResponseWriter, r *http.Request) {
	s.handleQA(w, r, qa.KindTest)
}

func (s *Server) handleQALint(w http.ResponseWriter, r *http.Request) {
	s.handleQA(w, r, qa.KindLint)
}

func (s *Server) handleQA(w http.ResponseWriter, r *http.Request, kind qa.Kind) {
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
	tc, auditErr := s.audit.Record(r.Context(), core.RecordInput{
		RunID:    runID,
		ToolName: string(kind),
		Request:  body,
		Response: map[string]any{"report": report},
		Err:      runErr,
	})
	if auditErr != nil {
		writeErr(w, http.StatusInternalServerError, "audit record failed: "+auditErr.Error())
		return
	}

	status := "ok"
	if runErr != nil {
		status = "fail"
		s.logger.Error("tool call failed",
			"run_id", runID,
			"tool_call_id", tc.ToolCallID,
			"tool_name", string(kind),
			"repo", run.Repo,
			"err", runErr,
		)
	} else {
		s.logger.Info("tool call completed",
			"run_id", runID,
			"tool_call_id", tc.ToolCallID,
			"tool_name", string(kind),
			"repo", run.Repo,
			"dry_run", body.DryRun,
		)
	}

	writeJSON(w, http.StatusOK, toolResponse{
		OK: runErr == nil,
		Meta: toolResponseMeta{
			RunID:        runID,
			ToolCallID:   tc.ToolCallID,
			EvidenceHash: tc.EvidenceHash,
			DryRun:       body.DryRun,
		},
		Result: map[string]any{"status": status, "report": report},
	})
}

func (s *Server) handleGetPR(w http.ResponseWriter, r *http.Request) {
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

	tc, auditErr := s.audit.Record(r.Context(), core.RecordInput{
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

	writeJSON(w, http.StatusOK, toolResponse{
		OK: true,
		Meta: toolResponseMeta{
			RunID:        runID,
			ToolCallID:   tc.ToolCallID,
			EvidenceHash: tc.EvidenceHash,
			DryRun:       false,
		},
		Result: pr,
	})
}

func (s *Server) handleListPRFiles(w http.ResponseWriter, r *http.Request) {
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

	tc, auditErr := s.audit.Record(r.Context(), core.RecordInput{
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

	writeJSON(w, http.StatusOK, toolResponse{
		OK: true,
		Meta: toolResponseMeta{
			RunID:        runID,
			ToolCallID:   tc.ToolCallID,
			EvidenceHash: tc.EvidenceHash,
			DryRun:       false,
		},
		Result: map[string]any{"files": files, "count": len(files)},
	})
}

func (s *Server) handleCreateIssue(w http.ResponseWriter, r *http.Request) {
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
	idemKey, err := core.MakeIssueIdempotencyKey(runID, toolName, body.Title, body.Body, body.Labels, nil)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	var replayIssue gh.Issue
	tc, replayed, err := s.audit.ReplayResponse(r.Context(), runID, toolName, idemKey, &replayIssue)
	if err != nil {
		writeMappedErr(w, err, http.StatusInternalServerError)
		return
	}
	if replayed {
		writeJSON(w, http.StatusOK, toolResponse{
			OK: true,
			Meta: toolResponseMeta{
				RunID:        runID,
				ToolCallID:   tc.ToolCallID,
				EvidenceHash: tc.EvidenceHash,
				DryRun:       false,
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

	tc, auditErr := s.audit.Record(r.Context(), core.RecordInput{
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

	writeJSON(w, http.StatusOK, toolResponse{
		OK: ghErr == nil,
		Meta: toolResponseMeta{
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

		tc, auditErr := s.audit.Record(r.Context(), core.RecordInput{
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

	writeJSON(w, http.StatusOK, toolResponse{
		OK: errCount == 0,
		Meta: toolResponseMeta{
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
	idemKey, err := core.MakeIssueIdempotencyKey(runID, toolName, fmt.Sprintf("pr-%d", prNumber), body.Body, keyLabels, nil)
	if err != nil {
		writeMappedErr(w, err, http.StatusInternalServerError)
		return
	}

	var replay map[string]any
	tcReplay, replayed, err := s.audit.ReplayResponse(r.Context(), runID, toolName, idemKey, &replay)
	if err != nil {
		writeMappedErr(w, err, http.StatusInternalServerError)
		return
	}
	if replayed {
		writeJSON(w, http.StatusOK, toolResponse{
			OK:     true,
			Meta:   toolResponseMeta{RunID: runID, ToolCallID: tcReplay.ToolCallID, EvidenceHash: tcReplay.EvidenceHash, DryRun: false},
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

	tc, auditErr := s.audit.Record(r.Context(), core.RecordInput{
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
	writeJSON(w, http.StatusOK, toolResponse{
		OK:     true,
		Meta:   toolResponseMeta{RunID: runID, ToolCallID: tc.ToolCallID, EvidenceHash: tc.EvidenceHash, DryRun: body.DryRun},
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
		writeErr(w, mapped.HTTPStatus, mapped.Message)
		return
	}
	mapped := core.MapError(err, fallbackStatus)
	writeErr(w, mapped.HTTPStatus, mapped.Message)
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
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		logger.Info("http request",
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
