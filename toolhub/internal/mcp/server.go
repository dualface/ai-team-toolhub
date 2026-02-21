package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"

	"github.com/toolhub/toolhub/internal/core"
	gh "github.com/toolhub/toolhub/internal/github"
)

type Server struct {
	runs   *core.RunService
	audit  *core.AuditService
	policy *core.Policy
	gh     *gh.Client
	addr   string
	logger *slog.Logger
	mode   core.BatchMode

	ln     net.Listener
	mu     sync.Mutex
	closed bool
}

func NewServer(addr string, runs *core.RunService, audit *core.AuditService, policy *core.Policy, ghClient *gh.Client, logger *slog.Logger, mode core.BatchMode) *Server {
	return &Server{
		runs:   runs,
		audit:  audit,
		policy: policy,
		gh:     ghClient,
		addr:   addr,
		logger: logger,
		mode:   mode,
	}
}

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()

	s.logger.Info("mcp server starting", "addr", s.addr)

	for {
		conn, err := ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return nil
			}
			s.logger.Error("mcp accept error", "err", err)
			continue
		}
		go s.handleConn(conn)
	}
}

func (s *Server) Shutdown(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.ln != nil {
		return s.ln.Close()
	}
	return nil
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			s.writeResponse(conn, jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      nil,
				Error:   &rpcError{Code: -32700, Message: "parse error"},
			})
			continue
		}

		resp := s.dispatch(context.Background(), req)
		s.writeResponse(conn, resp)
	}
}

func (s *Server) writeResponse(w io.Writer, resp jsonRPCResponse) {
	data, _ := json.Marshal(resp)
	data = append(data, '\n')
	w.Write(data)
}

func (s *Server) dispatch(ctx context.Context, req jsonRPCRequest) jsonRPCResponse {
	base := jsonRPCResponse{JSONRPC: "2.0", ID: req.ID}

	switch req.Method {
	case "initialize":
		base.Result = map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "toolhub", "version": "0.1.0"},
		}
		return base

	case "tools/list":
		base.Result = map[string]any{"tools": s.toolDefinitions()}
		return base

	case "tools/call":
		return s.handleToolCall(ctx, req, base)

	default:
		base.Error = &rpcError{Code: -32601, Message: fmt.Sprintf("method not found: %s", req.Method)}
		return base
	}
}

func (s *Server) toolDefinitions() []map[string]any {
	return []map[string]any{
		{
			"name":        "runs_create",
			"description": "Create a new ToolHub run for a repository",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repo":    map[string]string{"type": "string", "description": "owner/repo"},
					"purpose": map[string]string{"type": "string", "description": "Why this run exists"},
				},
				"required": []string{"repo", "purpose"},
			},
		},
		{
			"name":        "github_issues_create",
			"description": "Create a GitHub issue within a run",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id": map[string]string{"type": "string"},
					"title":  map[string]string{"type": "string"},
					"body":   map[string]string{"type": "string"},
					"labels": map[string]any{"type": "array", "items": map[string]string{"type": "string"}},
				},
				"required": []string{"run_id", "title", "body"},
			},
		},
		{
			"name":        "github_issues_batch_create",
			"description": "Create multiple GitHub issues within a run",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id": map[string]string{"type": "string"},
					"issues": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"title":  map[string]string{"type": "string"},
								"body":   map[string]string{"type": "string"},
								"labels": map[string]any{"type": "array", "items": map[string]string{"type": "string"}},
							},
							"required": []string{"title", "body"},
						},
					},
				},
				"required": []string{"run_id", "issues"},
			},
		},
	}
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (s *Server) handleToolCall(ctx context.Context, req jsonRPCRequest, base jsonRPCResponse) jsonRPCResponse {
	var params toolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		base.Error = &rpcError{Code: -32602, Message: "invalid params: " + err.Error()}
		return base
	}

	switch params.Name {
	case "runs_create":
		return s.toolRunsCreate(ctx, params.Arguments, base)
	case "github_issues_create":
		return s.toolIssuesCreate(ctx, params.Arguments, base)
	case "github_issues_batch_create":
		return s.toolIssuesBatchCreate(ctx, params.Arguments, base)
	default:
		base.Error = &rpcError{Code: -32602, Message: fmt.Sprintf("unknown tool: %s", params.Name)}
		return base
	}
}

type runsCreateArgs struct {
	Repo    string `json:"repo"`
	Purpose string `json:"purpose"`
}

func (s *Server) toolRunsCreate(ctx context.Context, raw json.RawMessage, base jsonRPCResponse) jsonRPCResponse {
	var args runsCreateArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		base.Error = &rpcError{Code: -32602, Message: err.Error()}
		return base
	}

	if err := s.policy.CheckRepo(args.Repo); err != nil {
		base.Error = &rpcError{Code: -32602, Message: err.Error()}
		return base
	}

	run, err := s.runs.CreateRun(ctx, core.CreateRunRequest{Repo: args.Repo, Purpose: args.Purpose})
	if err != nil {
		base.Error = &rpcError{Code: -32603, Message: err.Error()}
		return base
	}

	base.Result = run
	return base
}

type issuesCreateArgs struct {
	RunID  string   `json:"run_id"`
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	Labels []string `json:"labels,omitempty"`
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

func (s *Server) toolIssuesCreate(ctx context.Context, raw json.RawMessage, base jsonRPCResponse) jsonRPCResponse {
	var args issuesCreateArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		base.Error = &rpcError{Code: -32602, Message: err.Error()}
		return base
	}
	if err := core.ValidateIssueInput(args.Title, args.Body, args.Labels); err != nil {
		base.Error = &rpcError{Code: -32602, Message: err.Error()}
		return base
	}

	run, err := s.runs.GetRun(ctx, args.RunID)
	if err != nil || run == nil {
		base.Error = &rpcError{Code: -32602, Message: "run not found"}
		return base
	}

	toolName := "github.issues.create"
	idemKey, err := core.MakeIssueIdempotencyKey(args.RunID, toolName, args.Title, args.Body, args.Labels, nil)
	if err != nil {
		base.Error = &rpcError{Code: -32603, Message: err.Error()}
		return base
	}

	var replayIssue gh.Issue
	replayed, err := s.audit.ReplayResponse(ctx, args.RunID, toolName, idemKey, &replayIssue)
	if err != nil {
		base.Error = &rpcError{Code: -32603, Message: err.Error()}
		return base
	}
	if replayed {
		base.Result = &replayIssue
		return base
	}

	owner, repo := splitRepo(run.Repo)
	issue, ghErr := s.gh.CreateIssue(ctx, owner, repo, gh.CreateIssueInput{
		Title: args.Title, Body: args.Body, Labels: args.Labels,
	})

	if _, auditErr := s.audit.Record(ctx, core.RecordInput{
		RunID: args.RunID, ToolName: toolName, IdemKey: &idemKey, Request: args, Response: issue, Err: ghErr,
	}); auditErr != nil {
		base.Error = &rpcError{Code: -32603, Message: "audit record failed: " + auditErr.Error()}
		return base
	}

	if ghErr != nil {
		base.Error = &rpcError{Code: -32603, Message: ghErr.Error()}
		return base
	}

	base.Result = issue
	return base
}

type issuesBatchCreateArgs struct {
	RunID  string `json:"run_id"`
	Issues []struct {
		Title  string   `json:"title"`
		Body   string   `json:"body"`
		Labels []string `json:"labels,omitempty"`
	} `json:"issues"`
}

func (s *Server) toolIssuesBatchCreate(ctx context.Context, raw json.RawMessage, base jsonRPCResponse) jsonRPCResponse {
	var args issuesBatchCreateArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		base.Error = &rpcError{Code: -32602, Message: err.Error()}
		return base
	}
	if len(args.Issues) == 0 {
		base.Error = &rpcError{Code: -32602, Message: "issues array is empty"}
		return base
	}
	if len(args.Issues) > core.MaxBatchSize {
		base.Error = &rpcError{Code: -32602, Message: fmt.Sprintf("issues exceed %d items", core.MaxBatchSize)}
		return base
	}
	for i, in := range args.Issues {
		if err := core.ValidateIssueInput(in.Title, in.Body, in.Labels); err != nil {
			base.Error = &rpcError{Code: -32602, Message: fmt.Sprintf("issue %d: %s", i, err.Error())}
			return base
		}
	}

	run, err := s.runs.GetRun(ctx, args.RunID)
	if err != nil || run == nil {
		base.Error = &rpcError{Code: -32602, Message: "run not found"}
		return base
	}

	owner, repo := splitRepo(run.Repo)

	out := make([]batchResultJSON, len(args.Issues))
	errCount := 0
	replayedCount := 0
	processed := 0
	for i, in := range args.Issues {
		processed = i + 1
		i2 := i
		idemKey, err := core.MakeIssueIdempotencyKey(args.RunID, "github.issues.batch_create", in.Title, in.Body, in.Labels, &i2)
		if err != nil {
			base.Error = &rpcError{Code: -32603, Message: err.Error()}
			return base
		}

		var replayIssue gh.Issue
		replayed, err := s.audit.ReplayResponse(ctx, args.RunID, "github.issues.batch_create", idemKey, &replayIssue)
		if err != nil {
			base.Error = &rpcError{Code: -32603, Message: err.Error()}
			return base
		}
		if replayed {
			replayedCount++
			out[i] = batchResultJSON{Index: i, Issue: &replayIssue, Replayed: true}
			continue
		}

		issue, ghErr := s.gh.CreateIssue(ctx, owner, repo, gh.CreateIssueInput{Title: in.Title, Body: in.Body, Labels: in.Labels})

		if _, auditErr := s.audit.Record(ctx, core.RecordInput{
			RunID: args.RunID, ToolName: "github.issues.batch_create", IdemKey: &idemKey,
			Request: in, Response: issue, Err: ghErr,
		}); auditErr != nil {
			base.Error = &rpcError{Code: -32603, Message: fmt.Sprintf("audit record failed at index %d: %s", i, auditErr.Error())}
			return base
		}

		if ghErr != nil {
			errCount++
			out[i] = batchResultJSON{Index: i, Issue: issue, Error: ghErr.Error()}
			if s.mode == core.BatchModeStrict {
				stoppedAt := i
				base.Error = &rpcError{Code: -32603, Message: fmt.Sprintf("batch strict mode stopped at index %d: %s", i, ghErr.Error())}
				base.Result = batchResponseJSON{
					Mode:         s.mode,
					Total:        len(args.Issues),
					Processed:    processed,
					Errors:       errCount,
					Replayed:     replayedCount,
					CreatedFresh: processed - replayedCount,
					StoppedAt:    &stoppedAt,
					FailedReason: ghErr.Error(),
					Results:      out[:processed],
				}
				return base
			}
		} else {
			out[i] = batchResultJSON{Index: i, Issue: issue}
		}
	}

	base.Result = batchResponseJSON{
		Mode:         s.mode,
		Total:        len(args.Issues),
		Processed:    len(args.Issues),
		Errors:       errCount,
		Replayed:     replayedCount,
		CreatedFresh: len(args.Issues) - replayedCount,
		Results:      out,
	}
	return base
}

func splitRepo(fullRepo string) (string, string) {
	parts := strings.SplitN(fullRepo, "/", 2)
	if len(parts) != 2 {
		return fullRepo, ""
	}
	return parts[0], parts[1]
}

func mcpContent(text string) map[string]any {
	return map[string]any{
		"content": []map[string]string{
			{"type": "text", "text": text},
		},
	}
}
