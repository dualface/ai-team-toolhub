package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/toolhub/toolhub/internal/core"
	gh "github.com/toolhub/toolhub/internal/github"
)

type Server struct {
	runs   *core.RunService
	audit  *core.AuditService
	policy *core.Policy
	gh     *gh.Client
	srv    *http.Server
	logger *slog.Logger
	mode   core.BatchMode
}

const maxRequestBodyBytes = 1 << 20

func NewServer(addr string, runs *core.RunService, audit *core.AuditService, policy *core.Policy, ghClient *gh.Client, logger *slog.Logger, mode core.BatchMode) *Server {
	s := &Server{
		runs:   runs,
		audit:  audit,
		policy: policy,
		gh:     ghClient,
		logger: logger,
		mode:   mode,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("POST /api/v1/runs", s.handleCreateRun)
	mux.HandleFunc("GET /api/v1/runs/{runID}", s.handleGetRun)
	mux.HandleFunc("POST /api/v1/runs/{runID}/issues", s.handleCreateIssue)
	mux.HandleFunc("POST /api/v1/runs/{runID}/issues/batch", s.handleBatchCreateIssues)

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
	replayed, err := s.audit.ReplayResponse(r.Context(), runID, toolName, idemKey, &replayIssue)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if replayed {
		writeJSON(w, http.StatusOK, &replayIssue)
		return
	}

	owner, repo := splitRepo(run.Repo)

	issue, ghErr := s.gh.CreateIssue(r.Context(), owner, repo, gh.CreateIssueInput{
		Title:  body.Title,
		Body:   body.Body,
		Labels: body.Labels,
	})

	if _, auditErr := s.audit.Record(r.Context(), core.RecordInput{
		RunID:    runID,
		ToolName: toolName,
		IdemKey:  &idemKey,
		Request:  body,
		Response: issue,
		Err:      ghErr,
	}); auditErr != nil {
		writeErr(w, http.StatusInternalServerError, "audit record failed: "+auditErr.Error())
		return
	}

	if ghErr != nil {
		writeErr(w, http.StatusBadGateway, ghErr.Error())
		return
	}

	writeJSON(w, http.StatusCreated, issue)
}

type batchCreateBody struct {
	Issues []createIssueBody `json:"issues"`
}

type batchResultJSON struct {
	Index    int       `json:"index"`
	Issue    *gh.Issue `json:"issue,omitempty"`
	Replayed bool      `json:"replayed,omitempty"`
	Error    string    `json:"error,omitempty"`
}

type batchResponseJSON struct {
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
		replayed, err := s.audit.ReplayResponse(r.Context(), runID, "github.issues.batch_create", idemKey, &replayIssue)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}

		if replayed {
			out[i] = batchResultJSON{Index: i, Issue: &replayIssue, Replayed: true}
			replayedCount++
			continue
		}

		issue, ghErr := s.gh.CreateIssue(r.Context(), owner, repo, gh.CreateIssueInput{
			Title: in.Title, Body: in.Body, Labels: in.Labels,
		})

		if _, auditErr := s.audit.Record(r.Context(), core.RecordInput{
			RunID:    runID,
			ToolName: "github.issues.batch_create",
			IdemKey:  &idemKey,
			Request:  in,
			Response: issue,
			Err:      ghErr,
		}); auditErr != nil {
			writeErr(w, http.StatusInternalServerError, fmt.Sprintf("audit record failed at index %d: %s", i, auditErr.Error()))
			return
		}

		out[i] = batchResultJSON{Index: i, Issue: issue}
		if ghErr != nil {
			out[i].Error = ghErr.Error()
			errCount++
			if s.mode == core.BatchModeStrict {
				stoppedAt := i
				writeJSON(w, http.StatusBadGateway, batchResponseJSON{
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

	writeJSON(w, http.StatusOK, batchResponseJSON{
		Mode:         s.mode,
		Total:        len(body.Issues),
		Processed:    len(body.Issues),
		Errors:       errCount,
		Replayed:     replayedCount,
		CreatedFresh: len(body.Issues) - replayedCount,
		Results:      out,
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
	writeJSON(w, status, map[string]string{"error": msg})
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
