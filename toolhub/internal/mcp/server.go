package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/toolhub/toolhub/internal/codeops"
	"github.com/toolhub/toolhub/internal/core"
	gh "github.com/toolhub/toolhub/internal/github"
	"github.com/toolhub/toolhub/internal/qa"
	"github.com/toolhub/toolhub/internal/telemetry"
)

type ctxKey string

const ctxKeyTraceID ctxKey = "trace_id"

type Server struct {
	runs   *core.RunService
	audit  *core.AuditService
	policy *core.Policy
	gh     *gh.Client
	qa     *qa.Runner
	code   *codeops.Runner
	addr   string
	logger *slog.Logger
	mode   core.BatchMode

	ln     net.Listener
	mu     sync.Mutex
	closed bool
}

func NewServer(addr string, runs *core.RunService, audit *core.AuditService, policy *core.Policy, ghClient *gh.Client, qaRunner *qa.Runner, codeRunner *codeops.Runner, logger *slog.Logger, mode core.BatchMode) *Server {
	return &Server{
		runs:   runs,
		audit:  audit,
		policy: policy,
		gh:     ghClient,
		qa:     qaRunner,
		code:   codeRunner,
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

		traceID := uuid.New().String()
		ctx := context.WithValue(context.Background(), ctxKeyTraceID, traceID)
		resp := s.dispatch(ctx, req)
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
			"serverInfo":      map[string]any{"name": "toolhub", "version": "0.1.0", "contract_version": core.ContractVersion},
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
	return ToolDefinitions()
}

func ToolDefinitions() []map[string]any {
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
					"run_id":  map[string]string{"type": "string"},
					"title":   map[string]string{"type": "string"},
					"body":    map[string]string{"type": "string"},
					"labels":  map[string]any{"type": "array", "items": map[string]string{"type": "string"}},
					"dry_run": map[string]string{"type": "boolean"},
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
					"run_id":  map[string]string{"type": "string"},
					"dry_run": map[string]string{"type": "boolean"},
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
		{
			"name":        "github_pr_comment_create",
			"description": "Create a PR summary comment within a run",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id":    map[string]string{"type": "string"},
					"pr_number": map[string]string{"type": "integer"},
					"body":      map[string]string{"type": "string"},
					"dry_run":   map[string]string{"type": "boolean"},
				},
				"required": []string{"run_id", "pr_number", "body"},
			},
		},
		{
			"name":        "github_pr_get",
			"description": "Get pull request metadata within a run",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id":    map[string]string{"type": "string"},
					"pr_number": map[string]string{"type": "integer"},
				},
				"required": []string{"run_id", "pr_number"},
			},
		},
		{
			"name":        "github_pr_files_list",
			"description": "List pull request files within a run",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id":    map[string]string{"type": "string"},
					"pr_number": map[string]string{"type": "integer"},
				},
				"required": []string{"run_id", "pr_number"},
			},
		},
		{
			"name":        "qa_test",
			"description": "Execute configured test command and capture output",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id":  map[string]string{"type": "string"},
					"dry_run": map[string]string{"type": "boolean"},
				},
				"required": []string{"run_id"},
			},
		},
		{
			"name":        "qa_lint",
			"description": "Execute configured lint command and capture output",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id":  map[string]string{"type": "string"},
					"dry_run": map[string]string{"type": "boolean"},
				},
				"required": []string{"run_id"},
			},
		},
		{
			"name":        "code_patch_generate",
			"description": "Generate unified patch/diff without modifying repository",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id":           map[string]string{"type": "string"},
					"path":             map[string]string{"type": "string"},
					"original_content": map[string]string{"type": "string"},
					"modified_content": map[string]string{"type": "string"},
					"dry_run":          map[string]string{"type": "boolean"},
				},
				"required": []string{"run_id", "path", "original_content", "modified_content"},
			},
		},
		{
			"name":        "code_branch_pr_create",
			"description": "Create branch, commit changes, push branch, and open PR (requires approved approval_id)",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id":         map[string]string{"type": "string"},
					"approval_id":    map[string]string{"type": "string"},
					"base_branch":    map[string]string{"type": "string"},
					"head_branch":    map[string]string{"type": "string"},
					"commit_message": map[string]string{"type": "string"},
					"pr_title":       map[string]string{"type": "string"},
					"pr_body":        map[string]string{"type": "string"},
					"dry_run":        map[string]string{"type": "boolean"},
					"files": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"path":             map[string]string{"type": "string"},
								"original_content": map[string]string{"type": "string"},
								"modified_content": map[string]string{"type": "string"},
							},
							"required": []string{"path", "modified_content"},
						},
					},
				},
				"required": []string{"run_id", "approval_id", "base_branch", "head_branch", "commit_message", "pr_title", "files"},
			},
		},
		{
			"name":        "code_repair_loop",
			"description": "Run controlled repair loop: branch/commit, QA retries, rollback on QA failure, and PR on success",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id":         map[string]string{"type": "string"},
					"approval_id":    map[string]string{"type": "string"},
					"base_branch":    map[string]string{"type": "string"},
					"head_branch":    map[string]string{"type": "string"},
					"commit_message": map[string]string{"type": "string"},
					"pr_title":       map[string]string{"type": "string"},
					"pr_body":        map[string]string{"type": "string"},
					"max_iterations": map[string]string{"type": "integer"},
					"dry_run":        map[string]string{"type": "boolean"},
					"files": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"path":             map[string]string{"type": "string"},
								"original_content": map[string]string{"type": "string"},
								"modified_content": map[string]string{"type": "string"},
							},
							"required": []string{"path", "modified_content"},
						},
					},
				},
				"required": []string{"run_id", "approval_id", "base_branch", "head_branch", "commit_message", "pr_title", "files"},
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

	start := time.Now()
	defer func() { telemetry.ObserveToolDuration(params.Name, time.Since(start)) }()

	switch params.Name {
	case "runs_create":
		return s.toolRunsCreate(ctx, params.Arguments, base)
	case "github_issues_create":
		return s.toolIssuesCreate(ctx, params.Arguments, base)
	case "github_issues_batch_create":
		return s.toolIssuesBatchCreate(ctx, params.Arguments, base)
	case "github_pr_comment_create":
		return s.toolPRCommentCreate(ctx, params.Arguments, base)
	case "github_pr_get":
		return s.toolPRGet(ctx, params.Arguments, base)
	case "github_pr_files_list":
		return s.toolPRFilesList(ctx, params.Arguments, base)
	case "qa_test":
		return s.toolQA(ctx, params.Arguments, base, qa.KindTest)
	case "qa_lint":
		return s.toolQA(ctx, params.Arguments, base, qa.KindLint)
	case "code_patch_generate":
		return s.toolCodePatchGenerate(ctx, params.Arguments, base)
	case "code_branch_pr_create":
		return s.toolCodeBranchPRCreate(ctx, params.Arguments, base)
	case "code_repair_loop":
		return s.toolCodeRepairLoop(ctx, params.Arguments, base)
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
	DryRun bool     `json:"dry_run,omitempty"`
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

func (s *Server) toolIssuesCreate(ctx context.Context, raw json.RawMessage, base jsonRPCResponse) jsonRPCResponse {
	traceID, _ := ctx.Value(ctxKeyTraceID).(string)

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
	tc, replayed, err := s.audit.ReplayResponse(ctx, args.RunID, toolName, idemKey, &replayIssue)
	if err != nil {
		mapped := core.MapError(err, 500)
		base.Error = &rpcError{Code: -32603, Message: mapped.Code + ": " + mapped.Message}
		return base
	}
	if replayed {
		base.Result = core.ToolEnvelope{OK: true, Meta: core.ToolMeta{RunID: args.RunID, ToolCallID: tc.ToolCallID, EvidenceHash: tc.EvidenceHash, DryRun: false, Replayed: true}, Result: &replayIssue}
		return base
	}

	owner, repo := splitRepo(run.Repo)
	var issue *gh.Issue
	var ghErr error
	if !args.DryRun {
		issue, ghErr = s.gh.CreateIssue(ctx, owner, repo, gh.CreateIssueInput{
			Title: args.Title, Body: args.Body, Labels: args.Labels,
		})
	}

	tc, _, auditErr := s.audit.Record(ctx, core.RecordInput{
		RunID: args.RunID, ToolName: toolName, IdemKey: &idemKey, Request: args, Response: issue, Err: ghErr,
	})
	if auditErr != nil {
		base.Error = &rpcError{Code: -32603, Message: "audit record failed: " + auditErr.Error()}
		return base
	}

	s.logger.Info("tool call completed",
		"trace_id", traceID,
		"run_id", args.RunID,
		"tool_call_id", tc.ToolCallID,
		"tool_name", toolName,
		"repo", run.Repo,
		"dry_run", args.DryRun,
	)

	if ghErr != nil {
		mapped := core.MapError(ghErr, 502)
		base.Error = &rpcError{Code: -32603, Message: mapped.Code + ": " + mapped.Message}
		return base
	}

	result := any(issue)
	if args.DryRun {
		result = map[string]any{"would_create": map[string]any{"repo": run.Repo, "title": args.Title, "body": args.Body, "labels": args.Labels}}
	}
	base.Result = core.ToolEnvelope{OK: true, Meta: core.ToolMeta{RunID: args.RunID, ToolCallID: tc.ToolCallID, EvidenceHash: tc.EvidenceHash, DryRun: args.DryRun}, Result: result}
	return base
}

type issuesBatchCreateArgs struct {
	RunID  string `json:"run_id"`
	DryRun bool   `json:"dry_run,omitempty"`
	Issues []struct {
		Title  string   `json:"title"`
		Body   string   `json:"body"`
		Labels []string `json:"labels,omitempty"`
	} `json:"issues"`
}

func (s *Server) toolIssuesBatchCreate(ctx context.Context, raw json.RawMessage, base jsonRPCResponse) jsonRPCResponse {
	traceID, _ := ctx.Value(ctxKeyTraceID).(string)

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
		tc, replayed, err := s.audit.ReplayResponse(ctx, args.RunID, "github.issues.batch_create", idemKey, &replayIssue)
		if err != nil {
			base.Error = &rpcError{Code: -32603, Message: err.Error()}
			return base
		}
		if replayed {
			s.logger.Info("tool call replayed",
				"trace_id", traceID,
				"run_id", args.RunID,
				"tool_call_id", tc.ToolCallID,
				"tool_name", "github.issues.batch_create",
				"repo", run.Repo,
				"index", i,
			)
			replayedCount++
			out[i] = batchResultJSON{Index: i, Issue: &replayIssue, Replayed: true}
			continue
		}

		var issue *gh.Issue
		var ghErr error
		if !args.DryRun {
			issue, ghErr = s.gh.CreateIssue(ctx, owner, repo, gh.CreateIssueInput{Title: in.Title, Body: in.Body, Labels: in.Labels})
		}

		tc, _, auditErr := s.audit.Record(ctx, core.RecordInput{
			RunID: args.RunID, ToolName: "github.issues.batch_create", IdemKey: &idemKey,
			Request: in, Response: issue, Err: ghErr,
		})
		if auditErr != nil {
			base.Error = &rpcError{Code: -32603, Message: fmt.Sprintf("audit record failed at index %d: %s", i, auditErr.Error())}
			return base
		}

		s.logger.Info("tool call completed",
			"trace_id", traceID,
			"run_id", args.RunID,
			"tool_call_id", tc.ToolCallID,
			"tool_name", "github.issues.batch_create",
			"repo", run.Repo,
			"index", i,
			"dry_run", args.DryRun,
		)

		if ghErr != nil {
			errCount++
			out[i] = batchResultJSON{Index: i, Issue: issue, Error: ghErr.Error()}
			if s.mode == core.BatchModeStrict {
				stoppedAt := i
				status := core.DeriveBatchStatus(processed, replayedCount, errCount)
				base.Result = core.ToolEnvelope{OK: false, Meta: core.ToolMeta{RunID: args.RunID, DryRun: args.DryRun}, Result: batchResponseJSON{
					Status:       status,
					Mode:         s.mode,
					Total:        len(args.Issues),
					Processed:    processed,
					Errors:       errCount,
					Replayed:     replayedCount,
					CreatedFresh: processed - replayedCount,
					StoppedAt:    &stoppedAt,
					FailedReason: ghErr.Error(),
					Results:      out[:processed],
				}}
				return base
			}
		} else {
			out[i] = batchResultJSON{Index: i, Issue: issue}
		}
	}

	status := core.DeriveBatchStatus(len(args.Issues), replayedCount, errCount)

	base.Result = core.ToolEnvelope{OK: errCount == 0, Meta: core.ToolMeta{RunID: args.RunID, DryRun: args.DryRun}, Result: batchResponseJSON{
		Status:       status,
		Mode:         s.mode,
		Total:        len(args.Issues),
		Processed:    len(args.Issues),
		Errors:       errCount,
		Replayed:     replayedCount,
		CreatedFresh: len(args.Issues) - replayedCount,
		Results:      out,
	}}
	return base
}

type prCommentArgs struct {
	RunID    string `json:"run_id"`
	PRNumber int    `json:"pr_number"`
	Body     string `json:"body"`
	DryRun   bool   `json:"dry_run,omitempty"`
}

type prReadArgs struct {
	RunID    string `json:"run_id"`
	PRNumber int    `json:"pr_number"`
}

type qaArgs struct {
	RunID  string `json:"run_id"`
	DryRun bool   `json:"dry_run,omitempty"`
}

type codePatchArgs struct {
	RunID           string `json:"run_id"`
	Path            string `json:"path"`
	OriginalContent string `json:"original_content"`
	ModifiedContent string `json:"modified_content"`
	DryRun          bool   `json:"dry_run,omitempty"`
}

type codeBranchPRArgs struct {
	RunID         string               `json:"run_id"`
	ApprovalID    string               `json:"approval_id"`
	BaseBranch    string               `json:"base_branch"`
	HeadBranch    string               `json:"head_branch"`
	CommitMessage string               `json:"commit_message"`
	PRTitle       string               `json:"pr_title"`
	PRBody        string               `json:"pr_body,omitempty"`
	Files         []codeops.FileChange `json:"files"`
	DryRun        bool                 `json:"dry_run,omitempty"`
}

type codeRepairLoopArgs struct {
	RunID         string               `json:"run_id"`
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

func (s *Server) toolCodePatchGenerate(ctx context.Context, raw json.RawMessage, base jsonRPCResponse) jsonRPCResponse {
	traceID, _ := ctx.Value(ctxKeyTraceID).(string)

	var args codePatchArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		base.Error = &rpcError{Code: -32602, Message: err.Error()}
		return base
	}
	if strings.TrimSpace(args.RunID) == "" {
		base.Error = &rpcError{Code: -32602, Message: "run_id is required"}
		return base
	}
	if strings.TrimSpace(args.Path) == "" {
		base.Error = &rpcError{Code: -32602, Message: "path is required"}
		return base
	}

	run, err := s.runs.GetRun(ctx, args.RunID)
	if err != nil || run == nil {
		base.Error = &rpcError{Code: -32602, Message: "run not found"}
		return base
	}
	if err := s.policy.CheckTool("code.patch.generate"); err != nil {
		base.Error = &rpcError{Code: -32602, Message: err.Error()}
		return base
	}

	patchText := core.GenerateUnifiedDiff(args.Path, args.OriginalContent, args.ModifiedContent)
	lineDelta := core.CountContentLines(args.ModifiedContent) - core.CountContentLines(args.OriginalContent)

	response := map[string]any{
		"path":       args.Path,
		"patch":      patchText,
		"line_delta": lineDelta,
	}

	tc, extraIDs, auditErr := s.audit.Record(ctx, core.RecordInput{
		RunID:    args.RunID,
		ToolName: "code.patch.generate",
		Request:  args,
		Response: response,
		Err:      nil,
		ExtraArtifacts: []core.ExtraArtifact{
			{Name: "code.patch.generate.patch.diff", ContentType: "text/x-diff", Body: []byte(patchText)},
		},
	})
	if auditErr != nil {
		base.Error = &rpcError{Code: -32603, Message: "audit record failed: " + auditErr.Error()}
		return base
	}

	s.logger.Info("tool call completed",
		"trace_id", traceID,
		"run_id", args.RunID,
		"tool_call_id", tc.ToolCallID,
		"tool_name", "code.patch.generate",
		"repo", run.Repo,
	)

	result := map[string]any{
		"path":       args.Path,
		"patch":      patchText,
		"line_delta": lineDelta,
	}
	if len(extraIDs) > 0 {
		result["patch_artifact_id"] = extraIDs[0]
	}

	base.Result = core.ToolEnvelope{
		OK:     true,
		Meta:   core.ToolMeta{RunID: args.RunID, ToolCallID: tc.ToolCallID, EvidenceHash: tc.EvidenceHash, DryRun: args.DryRun},
		Result: result,
	}
	return base
}

func (s *Server) toolCodeBranchPRCreate(ctx context.Context, raw json.RawMessage, base jsonRPCResponse) jsonRPCResponse {
	traceID, _ := ctx.Value(ctxKeyTraceID).(string)

	var args codeBranchPRArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		base.Error = &rpcError{Code: -32602, Message: err.Error()}
		return base
	}
	if strings.TrimSpace(args.RunID) == "" || strings.TrimSpace(args.ApprovalID) == "" {
		base.Error = &rpcError{Code: -32602, Message: "run_id and approval_id are required"}
		return base
	}

	run, err := s.runs.GetRun(ctx, args.RunID)
	if err != nil || run == nil {
		base.Error = &rpcError{Code: -32602, Message: "run not found"}
		return base
	}
	if err := s.policy.CheckTool("code.branch_pr.create"); err != nil {
		base.Error = &rpcError{Code: -32602, Message: err.Error()}
		return base
	}
	if s.code == nil {
		base.Error = &rpcError{Code: -32603, Message: "code runner is not configured"}
		return base
	}

	approval, err := s.audit.GetApproval(ctx, args.ApprovalID)
	if err != nil {
		base.Error = &rpcError{Code: -32603, Message: err.Error()}
		return base
	}
	if approval == nil || approval.RunID != args.RunID {
		base.Error = &rpcError{Code: -32602, Message: "approval not found"}
		return base
	}
	if approval.Status != "approved" {
		base.Error = &rpcError{Code: -32602, Message: "approval is not approved"}
		return base
	}

	paths := make([]string, 0, len(args.Files))
	for _, f := range args.Files {
		paths = append(paths, f.Path)
	}
	if err := s.policy.CheckPaths(paths); err != nil {
		base.Error = &rpcError{Code: -32602, Message: err.Error()}
		return base
	}

	patches := make([]string, 0, len(args.Files))
	for _, f := range args.Files {
		patches = append(patches, core.GenerateUnifiedDiff(f.Path, f.OriginalContent, f.ModifiedContent))
	}
	combinedPatch := strings.Join(patches, "\n")

	codeResult, runErr := s.code.Execute(ctx, codeops.Request{
		BaseBranch:    args.BaseBranch,
		HeadBranch:    args.HeadBranch,
		CommitMessage: args.CommitMessage,
		Files:         args.Files,
		DryRun:        args.DryRun,
	})
	if codeResult == nil {
		codeResult = &codeops.Result{}
	}

	result := map[string]any{
		"base_branch":      args.BaseBranch,
		"head_branch":      args.HeadBranch,
		"planned_commands": codeResult.PlannedCommands,
		"commit_hash":      codeResult.CommitHash,
	}

	if runErr == nil && !args.DryRun {
		owner, repo := splitRepo(run.Repo)
		pr, prErr := s.gh.CreatePullRequest(ctx, owner, repo, gh.CreatePullRequestInput{
			Title: args.PRTitle,
			Head:  args.HeadBranch,
			Base:  args.BaseBranch,
			Body:  args.PRBody,
		})
		if prErr != nil {
			runErr = prErr
		} else {
			result["pull_request"] = pr
		}
	}

	tc, extraIDs, auditErr := s.audit.Record(ctx, core.RecordInput{
		RunID:    args.RunID,
		ToolName: "code.branch_pr.create",
		Request:  args,
		Response: result,
		Err:      runErr,
		ExtraArtifacts: []core.ExtraArtifact{
			{Name: "code.branch_pr.create.patch.diff", ContentType: "text/x-diff", Body: []byte(combinedPatch)},
		},
	})
	if auditErr != nil {
		base.Error = &rpcError{Code: -32603, Message: "audit record failed: " + auditErr.Error()}
		return base
	}
	if len(extraIDs) > 0 {
		result["patch_artifact_id"] = extraIDs[0]
	}

	s.logger.Info("tool call completed",
		"trace_id", traceID,
		"run_id", args.RunID,
		"tool_call_id", tc.ToolCallID,
		"tool_name", "code.branch_pr.create",
		"repo", run.Repo,
	)

	if runErr != nil {
		mapped := core.MapError(runErr, 502)
		base.Error = &rpcError{Code: -32603, Message: mapped.Code + ": " + mapped.Message}
		return base
	}

	base.Result = core.ToolEnvelope{
		OK:     true,
		Meta:   core.ToolMeta{RunID: args.RunID, ToolCallID: tc.ToolCallID, EvidenceHash: tc.EvidenceHash, DryRun: args.DryRun},
		Result: result,
	}
	return base
}

func (s *Server) toolCodeRepairLoop(ctx context.Context, raw json.RawMessage, base jsonRPCResponse) jsonRPCResponse {
	traceID, _ := ctx.Value(ctxKeyTraceID).(string)

	var args codeRepairLoopArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		base.Error = &rpcError{Code: -32602, Message: err.Error()}
		return base
	}
	if strings.TrimSpace(args.RunID) == "" || strings.TrimSpace(args.ApprovalID) == "" {
		base.Error = &rpcError{Code: -32602, Message: "run_id and approval_id are required"}
		return base
	}
	if args.MaxIterations <= 0 {
		args.MaxIterations = 1
	}
	if args.MaxIterations > 3 {
		base.Error = &rpcError{Code: -32602, Message: "max_iterations cannot exceed 3"}
		return base
	}

	run, err := s.runs.GetRun(ctx, args.RunID)
	if err != nil || run == nil {
		base.Error = &rpcError{Code: -32602, Message: "run not found"}
		return base
	}
	if err := s.policy.CheckTool("code.repair_loop"); err != nil {
		base.Error = &rpcError{Code: -32602, Message: err.Error()}
		return base
	}
	if s.code == nil {
		base.Error = &rpcError{Code: -32603, Message: "code runner is not configured"}
		return base
	}
	if s.qa == nil {
		base.Error = &rpcError{Code: -32603, Message: "qa runner is not configured"}
		return base
	}

	approval, err := s.audit.GetApproval(ctx, args.ApprovalID)
	if err != nil {
		base.Error = &rpcError{Code: -32603, Message: err.Error()}
		return base
	}
	if approval == nil || approval.RunID != args.RunID {
		base.Error = &rpcError{Code: -32602, Message: "approval not found"}
		return base
	}
	if approval.Status != "approved" {
		base.Error = &rpcError{Code: -32602, Message: "approval is not approved"}
		return base
	}

	paths := make([]string, 0, len(args.Files))
	for _, f := range args.Files {
		paths = append(paths, f.Path)
	}
	if err := s.policy.CheckPaths(paths); err != nil {
		base.Error = &rpcError{Code: -32602, Message: err.Error()}
		return base
	}

	step, err := s.audit.StartStep(ctx, args.RunID, "code_repair_loop", "repair_loop")
	if err != nil {
		base.Error = &rpcError{Code: -32603, Message: err.Error()}
		return base
	}
	_ = s.audit.RecordDecision(ctx, args.RunID, &step.StepID, "system", "repair_loop_started", map[string]any{"max_iterations": args.MaxIterations})

	codeResult, runErr := s.code.Execute(ctx, codeops.Request{
		BaseBranch:    args.BaseBranch,
		HeadBranch:    args.HeadBranch,
		CommitMessage: args.CommitMessage,
		Files:         args.Files,
		DryRun:        args.DryRun,
	})
	if codeResult == nil {
		codeResult = &codeops.Result{}
	}

	iterationsRun := 0
	qaPassed := false
	qaAttempts := make([]map[string]any, 0, args.MaxIterations)

	result := map[string]any{
		"iterations_requested": args.MaxIterations,
		"iterations_run":       iterationsRun,
		"base_branch":          args.BaseBranch,
		"head_branch":          args.HeadBranch,
		"planned_commands":     codeResult.PlannedCommands,
		"commit_hash":          codeResult.CommitHash,
		"qa_passed":            qaPassed,
		"status":               "completed",
	}

	if runErr == nil && !args.DryRun {
		for i := 1; i <= args.MaxIterations; i++ {
			iterationsRun = i

			testReport, testErr := s.qa.Run(ctx, qa.KindTest, false)
			lintReport, lintErr := s.qa.Run(ctx, qa.KindLint, false)

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
			_ = s.audit.RecordDecision(ctx, args.RunID, &step.StepID, "system", "repair_loop_iteration", attempt)

			if testErr == nil && lintErr == nil {
				qaPassed = true
				break
			}
		}

		if !qaPassed {
			result["status"] = "failed"
			result["qa_failure_reason"] = fmt.Sprintf("qa checks failed after %d iteration(s)", iterationsRun)

			rollback, rollbackErr := s.code.RollbackBranch(ctx, args.BaseBranch, args.HeadBranch, false)
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

	if runErr == nil && !args.DryRun && qaPassed {
		owner, repo := splitRepo(run.Repo)
		pr, prErr := s.gh.CreatePullRequest(ctx, owner, repo, gh.CreatePullRequestInput{
			Title: args.PRTitle,
			Head:  args.HeadBranch,
			Base:  args.BaseBranch,
			Body:  args.PRBody,
		})
		if prErr != nil {
			runErr = prErr
		} else {
			result["pull_request"] = pr
		}
	}

	if runErr != nil {
		result["status"] = "failed"
	} else if args.DryRun {
		result["status"] = "dry_run"
	}

	tc, _, auditErr := s.audit.Record(ctx, core.RecordInput{
		RunID:    args.RunID,
		ToolName: "code.repair_loop",
		Request:  args,
		Response: result,
		Err:      runErr,
	})
	if auditErr != nil {
		base.Error = &rpcError{Code: -32603, Message: "audit record failed: " + auditErr.Error()}
		return base
	}

	decisionType := "repair_loop_completed"
	stepStatus := "completed"
	if runErr != nil {
		decisionType = "repair_loop_failed"
		stepStatus = "failed"
	}
	_ = s.audit.RecordDecision(ctx, args.RunID, &step.StepID, "system", decisionType, result)
	_ = s.audit.FinishStep(ctx, step.StepID, stepStatus)

	s.logger.Info("tool call completed",
		"trace_id", traceID,
		"run_id", args.RunID,
		"tool_call_id", tc.ToolCallID,
		"tool_name", "code.repair_loop",
		"repo", run.Repo,
	)

	if runErr != nil {
		mapped := core.MapError(runErr, 502)
		base.Error = &rpcError{Code: -32603, Message: mapped.Code + ": " + mapped.Message}
		return base
	}

	base.Result = core.ToolEnvelope{
		OK:     true,
		Meta:   core.ToolMeta{RunID: args.RunID, ToolCallID: tc.ToolCallID, EvidenceHash: tc.EvidenceHash, DryRun: args.DryRun},
		Result: result,
	}
	return base
}

func (s *Server) toolPRCommentCreate(ctx context.Context, raw json.RawMessage, base jsonRPCResponse) jsonRPCResponse {
	traceID, _ := ctx.Value(ctxKeyTraceID).(string)

	var args prCommentArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		base.Error = &rpcError{Code: -32602, Message: err.Error()}
		return base
	}
	if args.PRNumber <= 0 {
		base.Error = &rpcError{Code: -32602, Message: "pr_number must be positive"}
		return base
	}
	if strings.TrimSpace(args.Body) == "" {
		base.Error = &rpcError{Code: -32602, Message: "body is required"}
		return base
	}

	run, err := s.runs.GetRun(ctx, args.RunID)
	if err != nil || run == nil {
		base.Error = &rpcError{Code: -32602, Message: "run not found"}
		return base
	}
	if err := s.policy.CheckTool("github.pr.get"); err != nil {
		base.Error = &rpcError{Code: -32602, Message: err.Error()}
		return base
	}

	toolName := "github.pr.comment.create"
	labels := []string{"pr:" + strconv.Itoa(args.PRNumber)}
	idemKey, err := core.MakeIssueIdempotencyKey(args.RunID, toolName, fmt.Sprintf("pr-%d", args.PRNumber), args.Body, labels, nil)
	if err != nil {
		base.Error = &rpcError{Code: -32603, Message: err.Error()}
		return base
	}

	var replay any
	tcReplay, replayed, err := s.audit.ReplayResponse(ctx, args.RunID, toolName, idemKey, &replay)
	if err != nil {
		base.Error = &rpcError{Code: -32603, Message: err.Error()}
		return base
	}
	if replayed {
		base.Result = core.ToolEnvelope{OK: true, Meta: core.ToolMeta{RunID: args.RunID, ToolCallID: tcReplay.ToolCallID, EvidenceHash: tcReplay.EvidenceHash, DryRun: false, Replayed: true}, Result: replay}
		return base
	}

	owner, repo := splitRepo(run.Repo)
	var comment *gh.Comment
	var ghErr error
	if !args.DryRun {
		comment, ghErr = s.gh.CreatePRComment(ctx, owner, repo, args.PRNumber, args.Body)
	}

	tc, _, auditErr := s.audit.Record(ctx, core.RecordInput{
		RunID: args.RunID, ToolName: toolName, IdemKey: &idemKey,
		Request: args,
		Response: map[string]any{
			"comment": comment,
			"preview": map[string]any{"repo": run.Repo, "pr_number": args.PRNumber, "body": args.Body},
		},
		Err: ghErr,
	})
	if auditErr != nil {
		base.Error = &rpcError{Code: -32603, Message: "audit record failed: " + auditErr.Error()}
		return base
	}

	s.logger.Info("tool call completed",
		"trace_id", traceID,
		"run_id", args.RunID,
		"tool_call_id", tc.ToolCallID,
		"tool_name", toolName,
		"repo", run.Repo,
		"dry_run", args.DryRun,
		"pr_number", args.PRNumber,
	)

	if ghErr != nil {
		mapped := core.MapError(ghErr, 502)
		base.Error = &rpcError{Code: -32603, Message: mapped.Code + ": " + mapped.Message}
		return base
	}

	result := any(comment)
	if args.DryRun {
		result = map[string]any{"would_comment": map[string]any{"repo": run.Repo, "pr_number": args.PRNumber, "body": args.Body}}
	}
	base.Result = core.ToolEnvelope{OK: true, Meta: core.ToolMeta{RunID: args.RunID, ToolCallID: tc.ToolCallID, EvidenceHash: tc.EvidenceHash, DryRun: args.DryRun}, Result: result}
	return base
}

func (s *Server) toolPRGet(ctx context.Context, raw json.RawMessage, base jsonRPCResponse) jsonRPCResponse {
	traceID, _ := ctx.Value(ctxKeyTraceID).(string)

	var args prReadArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		base.Error = &rpcError{Code: -32602, Message: err.Error()}
		return base
	}
	if args.PRNumber <= 0 {
		base.Error = &rpcError{Code: -32602, Message: "pr_number must be positive"}
		return base
	}

	run, err := s.runs.GetRun(ctx, args.RunID)
	if err != nil || run == nil {
		base.Error = &rpcError{Code: -32602, Message: "run not found"}
		return base
	}
	if err := s.policy.CheckTool("github.pr.files.list"); err != nil {
		base.Error = &rpcError{Code: -32602, Message: err.Error()}
		return base
	}

	owner, repo := splitRepo(run.Repo)
	pr, ghErr := s.gh.GetPullRequest(ctx, owner, repo, args.PRNumber)

	tc, _, auditErr := s.audit.Record(ctx, core.RecordInput{
		RunID: args.RunID, ToolName: "github.pr.get",
		Request:  map[string]any{"pr_number": args.PRNumber},
		Response: pr,
		Err:      ghErr,
	})
	if auditErr != nil {
		base.Error = &rpcError{Code: -32603, Message: "audit record failed: " + auditErr.Error()}
		return base
	}

	s.logger.Info("tool call completed",
		"trace_id", traceID,
		"run_id", args.RunID,
		"tool_call_id", tc.ToolCallID,
		"tool_name", "github.pr.get",
		"repo", run.Repo,
		"pr_number", args.PRNumber,
	)

	if ghErr != nil {
		mapped := core.MapError(ghErr, 502)
		base.Error = &rpcError{Code: -32603, Message: mapped.Code + ": " + mapped.Message}
		return base
	}

	base.Result = core.ToolEnvelope{OK: true, Meta: core.ToolMeta{RunID: args.RunID, ToolCallID: tc.ToolCallID, EvidenceHash: tc.EvidenceHash, DryRun: false}, Result: pr}
	return base
}

func (s *Server) toolPRFilesList(ctx context.Context, raw json.RawMessage, base jsonRPCResponse) jsonRPCResponse {
	traceID, _ := ctx.Value(ctxKeyTraceID).(string)

	var args prReadArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		base.Error = &rpcError{Code: -32602, Message: err.Error()}
		return base
	}
	if args.PRNumber <= 0 {
		base.Error = &rpcError{Code: -32602, Message: "pr_number must be positive"}
		return base
	}

	run, err := s.runs.GetRun(ctx, args.RunID)
	if err != nil || run == nil {
		base.Error = &rpcError{Code: -32602, Message: "run not found"}
		return base
	}

	owner, repo := splitRepo(run.Repo)
	files, ghErr := s.gh.ListPullRequestFiles(ctx, owner, repo, args.PRNumber)

	tc, _, auditErr := s.audit.Record(ctx, core.RecordInput{
		RunID: args.RunID, ToolName: "github.pr.files.list",
		Request:  map[string]any{"pr_number": args.PRNumber},
		Response: map[string]any{"files": files, "count": len(files)},
		Err:      ghErr,
	})
	if auditErr != nil {
		base.Error = &rpcError{Code: -32603, Message: "audit record failed: " + auditErr.Error()}
		return base
	}

	s.logger.Info("tool call completed",
		"trace_id", traceID,
		"run_id", args.RunID,
		"tool_call_id", tc.ToolCallID,
		"tool_name", "github.pr.files.list",
		"repo", run.Repo,
		"pr_number", args.PRNumber,
	)

	if ghErr != nil {
		mapped := core.MapError(ghErr, 502)
		base.Error = &rpcError{Code: -32603, Message: mapped.Code + ": " + mapped.Message}
		return base
	}

	base.Result = core.ToolEnvelope{OK: true, Meta: core.ToolMeta{RunID: args.RunID, ToolCallID: tc.ToolCallID, EvidenceHash: tc.EvidenceHash, DryRun: false}, Result: map[string]any{"files": files, "count": len(files)}}
	return base
}

func (s *Server) toolQA(ctx context.Context, raw json.RawMessage, base jsonRPCResponse, kind qa.Kind) jsonRPCResponse {
	traceID, _ := ctx.Value(ctxKeyTraceID).(string)

	var args qaArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		base.Error = &rpcError{Code: -32602, Message: err.Error()}
		return base
	}
	if strings.TrimSpace(args.RunID) == "" {
		base.Error = &rpcError{Code: -32602, Message: "run_id is required"}
		return base
	}

	run, err := s.runs.GetRun(ctx, args.RunID)
	if err != nil || run == nil {
		base.Error = &rpcError{Code: -32602, Message: "run not found"}
		return base
	}
	if err := s.policy.CheckTool(string(kind)); err != nil {
		base.Error = &rpcError{Code: -32602, Message: err.Error()}
		return base
	}

	report, runErr := s.qa.Run(ctx, kind, args.DryRun)
	if runErr != nil && report.Command == "" {
		_, _, auditErr := s.audit.Record(ctx, core.RecordInput{
			RunID:    args.RunID,
			ToolName: string(kind),
			Request:  args,
			Response: nil,
			Err:      runErr,
		})
		if auditErr != nil {
			s.logger.Error("audit record failed",
				"trace_id", traceID,
				"err", auditErr,
			)
		}
		s.logger.Error("tool call failed",
			"trace_id", traceID,
			"run_id", args.RunID,
			"tool_name", string(kind),
			"err", runErr,
		)
		base.Error = &rpcError{Code: -32602, Message: runErr.Error()}
		return base
	}

	reportJSON, reportJSONErr := json.Marshal(report)
	if reportJSONErr != nil {
		base.Error = &rpcError{Code: -32603, Message: "marshal qa report failed: " + reportJSONErr.Error()}
		return base
	}

	tc, extraArtifactIDs, auditErr := s.audit.Record(ctx, core.RecordInput{
		RunID: args.RunID, ToolName: string(kind),
		Request:  args,
		Response: map[string]any{"report": report},
		Err:      runErr,
		ExtraArtifacts: []core.ExtraArtifact{
			{Name: fmt.Sprintf("%s.stdout.txt", kind), ContentType: "text/plain", Body: []byte(report.Stdout)},
			{Name: fmt.Sprintf("%s.stderr.txt", kind), ContentType: "text/plain", Body: []byte(report.Stderr)},
			{Name: fmt.Sprintf("%s.report.json", kind), ContentType: "application/json", Body: reportJSON},
		},
	})
	if auditErr != nil {
		base.Error = &rpcError{Code: -32603, Message: "audit record failed: " + auditErr.Error()}
		return base
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

	if runErr != nil {
		s.logger.Error("tool call failed",
			"trace_id", traceID,
			"run_id", args.RunID,
			"tool_call_id", tc.ToolCallID,
			"tool_name", string(kind),
			"repo", run.Repo,
			"err", runErr,
		)
	} else {
		s.logger.Info("tool call completed",
			"trace_id", traceID,
			"run_id", args.RunID,
			"tool_call_id", tc.ToolCallID,
			"tool_name", string(kind),
			"repo", run.Repo,
			"dry_run", args.DryRun,
		)
	}

	status := qa.DeriveStatus(report, runErr, args.DryRun)
	base.Result = core.ToolEnvelope{
		OK:   runErr == nil,
		Meta: core.ToolMeta{RunID: args.RunID, ToolCallID: tc.ToolCallID, EvidenceHash: tc.EvidenceHash, DryRun: args.DryRun, QAArtifacts: qaArtifacts},
		Result: map[string]any{
			"status": string(status),
			"report": report,
		},
		Error: func() *core.ToolError {
			if runErr == nil {
				return nil
			}
			return &core.ToolError{Code: string(status), Message: runErr.Error()}
		}(),
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

func asAPIError(err error) *gh.APIError {
	var apiErr *gh.APIError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	return nil
}
