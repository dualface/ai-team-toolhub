package qa

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/toolhub/toolhub/internal/telemetry"
)

type Kind string

const (
	KindTest Kind = "qa.test"
	KindLint Kind = "qa.lint"
)

type Config struct {
	WorkDir            string
	TestCmd            string
	LintCmd            string
	Timeout            time.Duration
	MaxOutputBytes     int
	MaxConcurrency     int
	Backend            string
	SandboxImage       string
	SandboxDockerBin   string
	SandboxContainerWD string
	AllowedExecutables []string
}

type Report struct {
	Command          string `json:"command"`
	WorkDir          string `json:"work_dir"`
	ExitCode         int    `json:"exit_code"`
	DurationMS       int64  `json:"duration_ms"`
	Stdout           string `json:"stdout"`
	Stderr           string `json:"stderr"`
	StdoutTruncated  bool   `json:"stdout_truncated"`
	StderrTruncated  bool   `json:"stderr_truncated"`
	OutputLimitBytes int    `json:"output_limit_bytes"`
}

// Status represents the outcome of a QA command execution.
type Status string

const (
	StatusPass    Status = "pass"
	StatusFail    Status = "fail"
	StatusTimeout Status = "timeout"
	StatusError   Status = "error"
	StatusDryRun  Status = "dry_run"
)

const (
	ErrCodeCommandEmpty        = "qa_command_empty"
	ErrCodeCommandNotAllowed   = "qa_command_not_allowed"
	ErrCodeCommandInvalid      = "qa_command_invalid"
	ErrCodeWorkdirInvalid      = "qa_workdir_invalid"
	ErrCodeToolUnsupported     = "qa_tool_unsupported"
	ErrCodeTimeout             = "qa_timeout"
	ErrCodeExecFailed          = "qa_execution_failed"
	ErrCodeConcurrencyExceeded = "qa_concurrency_exceeded"
	ErrCodeBackendInvalid      = "qa_backend_invalid"
)

// QAError represents a typed QA error with a machine-readable code.
type QAError struct {
	ErrCode string
	Detail  string
}

func (e *QAError) Error() string {
	return e.Detail
}

func (e *QAError) ErrorCode() string { return e.ErrCode }

// DeriveStatus determines the QA execution status from the report, error, and dry_run flag.
func DeriveStatus(report Report, err error, dryRun bool) Status {
	if dryRun {
		return StatusDryRun
	}
	if err == nil {
		return StatusPass
	}
	var qaErr *QAError
	if errors.As(err, &qaErr) {
		switch qaErr.ErrCode {
		case ErrCodeTimeout:
			return StatusTimeout
		case ErrCodeExecFailed:
			return StatusFail
		default:
			return StatusError
		}
	}

	if report.ExitCode > 0 {
		return StatusFail
	}
	return StatusError
}

type Runner struct {
	cfg                Config
	allowedExecutables map[string]bool
	semaphore          chan struct{}
	sandbox            *SandboxRunner
}

func NewRunner(cfg Config) (*Runner, error) {
	if cfg.WorkDir == "" {
		cfg.WorkDir = "."
	}
	if cfg.TestCmd == "" {
		cfg.TestCmd = "go -C toolhub test ./..."
	}
	if cfg.LintCmd == "" {
		cfg.LintCmd = "go -C toolhub test ./..."
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Minute
	}
	if cfg.MaxOutputBytes <= 0 {
		cfg.MaxOutputBytes = 256 * 1024
	}
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = 2
	}
	if strings.TrimSpace(cfg.Backend) == "" {
		cfg.Backend = "local"
	}
	if cfg.Backend != "local" && cfg.Backend != "sandbox" {
		return nil, &QAError{ErrCode: ErrCodeBackendInvalid, Detail: fmt.Sprintf("unsupported qa backend: %s", cfg.Backend)}
	}
	if len(cfg.AllowedExecutables) == 0 {
		cfg.AllowedExecutables = []string{
			"go", "make", "pytest", "python", "python3", "npm", "npx", "yarn", "pnpm", "ruff", "eslint", "golangci-lint",
		}
	}
	allowed := make(map[string]bool, len(cfg.AllowedExecutables))
	for _, exe := range cfg.AllowedExecutables {
		exe = strings.TrimSpace(exe)
		if exe != "" {
			allowed[exe] = true
		}
	}
	if err := validateConfiguredCommand(cfg.TestCmd, allowed); err != nil {
		return nil, err
	}
	if err := validateConfiguredCommand(cfg.LintCmd, allowed); err != nil {
		return nil, err
	}
	runner := &Runner{cfg: cfg, allowedExecutables: allowed, semaphore: make(chan struct{}, cfg.MaxConcurrency)}
	if cfg.Backend == "sandbox" {
		runner.sandbox = NewSandboxRunner(SandboxConfig{
			Image:            cfg.SandboxImage,
			DockerBinary:     cfg.SandboxDockerBin,
			ContainerWorkDir: cfg.SandboxContainerWD,
			Timeout:          cfg.Timeout,
			MaxOutputBytes:   cfg.MaxOutputBytes,
		})
	}
	return runner, nil
}

func validateConfiguredCommand(cmdline string, allowedExecutables map[string]bool) error {
	if err := validateCommandLine(cmdline); err != nil {
		return err
	}
	args, err := splitCommandLine(cmdline)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return &QAError{ErrCode: ErrCodeCommandEmpty, Detail: "qa command is empty"}
	}
	if !allowedExecutables[args[0]] {
		return &QAError{ErrCode: ErrCodeCommandNotAllowed, Detail: fmt.Sprintf("qa executable %q is not in allowlist", args[0])}
	}
	return nil
}

func (r *Runner) Run(ctx context.Context, kind Kind, dryRun bool) (Report, error) {
	select {
	case r.semaphore <- struct{}{}:
		defer func() { <-r.semaphore }()
	case <-ctx.Done():
		return Report{}, &QAError{ErrCode: ErrCodeConcurrencyExceeded, Detail: "qa concurrency limit exceeded: " + ctx.Err().Error()}
	}

	cmdline, err := r.commandFor(kind)
	if err != nil {
		return Report{}, err
	}
	if err := validateCommandLine(cmdline); err != nil {
		return Report{}, err
	}
	args, err := splitCommandLine(cmdline)
	if err != nil {
		return Report{}, err
	}
	if len(args) == 0 {
		return Report{}, &QAError{ErrCode: ErrCodeCommandEmpty, Detail: "qa command is empty"}
	}
	if !r.allowedExecutables[args[0]] {
		return Report{}, &QAError{ErrCode: ErrCodeCommandNotAllowed, Detail: fmt.Sprintf("qa executable %q is not in allowlist", args[0])}
	}
	wd, err := absWorkDir(r.cfg.WorkDir)
	if err != nil {
		return Report{}, err
	}

	if dryRun {
		return Report{Command: cmdline, WorkDir: wd, ExitCode: 0, OutputLimitBytes: r.cfg.MaxOutputBytes}, nil
	}

	if r.cfg.Backend == "sandbox" {
		return r.sandbox.RunCommand(ctx, cmdline, wd, false)
	}

	return r.runLocal(ctx, cmdline, wd)
}

func (r *Runner) runLocal(ctx context.Context, cmdline, wd string) (Report, error) {
	args, err := splitCommandLine(cmdline)
	if err != nil {
		return Report{}, err
	}
	if len(args) == 0 {
		return Report{}, &QAError{ErrCode: ErrCodeCommandEmpty, Detail: "qa command is empty"}
	}

	execCtx, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, args[0], args[1:]...)
	cmd.Dir = wd
	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	start := time.Now()
	runErr := cmd.Run()
	duration := time.Since(start)

	report := Report{
		Command:          cmdline,
		WorkDir:          wd,
		ExitCode:         0,
		DurationMS:       duration.Milliseconds(),
		OutputLimitBytes: r.cfg.MaxOutputBytes,
	}
	report.Stdout, report.StdoutTruncated = truncateOutput(stdoutBuf.String(), r.cfg.MaxOutputBytes)
	report.Stderr, report.StderrTruncated = truncateOutput(stderrBuf.String(), r.cfg.MaxOutputBytes)

	if runErr == nil {
		return report, nil
	}

	if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
		report.ExitCode = -1
		telemetry.IncQATimeout()
		return report, &QAError{ErrCode: ErrCodeTimeout, Detail: fmt.Sprintf("qa command timed out after %s", r.cfg.Timeout)}
	}

	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		report.ExitCode = exitErr.ExitCode()
		return report, &QAError{ErrCode: ErrCodeExecFailed, Detail: fmt.Sprintf("qa command failed with exit code %d", report.ExitCode)}
	}

	report.ExitCode = -1
	return report, runErr
}

func (r *Runner) commandFor(kind Kind) (string, error) {
	switch kind {
	case KindTest:
		return r.cfg.TestCmd, nil
	case KindLint:
		return r.cfg.LintCmd, nil
	default:
		return "", &QAError{ErrCode: ErrCodeToolUnsupported, Detail: fmt.Sprintf("unsupported qa tool: %s", kind)}
	}
}

func absWorkDir(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", &QAError{ErrCode: ErrCodeWorkdirInvalid, Detail: "qa workdir is empty"}
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if dir == "." {
		return wd, nil
	}
	if strings.HasPrefix(dir, "/") {
		return dir, nil
	}
	return wd + "/" + dir, nil
}

func validateCommandLine(cmdline string) error {
	trimmed := strings.TrimSpace(cmdline)
	if trimmed == "" {
		return &QAError{ErrCode: ErrCodeCommandEmpty, Detail: "qa command is empty"}
	}
	dangerous := []string{"&&", "||", ";", "|", "$(", "`", ">", "<", "\n", "\r"}
	for _, token := range dangerous {
		if strings.Contains(trimmed, token) {
			return &QAError{ErrCode: ErrCodeCommandInvalid, Detail: fmt.Sprintf("qa command contains forbidden shell operator %q", token)}
		}
	}
	return nil
}

func splitCommandLine(input string) ([]string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil, nil
	}

	args := make([]string, 0)
	var token strings.Builder
	quote := rune(0)
	escape := false

	flush := func() {
		if token.Len() > 0 {
			args = append(args, token.String())
			token.Reset()
		}
	}

	for _, r := range trimmed {
		if escape {
			token.WriteRune(r)
			escape = false
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				token.WriteRune(r)
			}
			continue
		}

		switch r {
		case '\\':
			escape = true
		case '\'', '"':
			quote = r
		case ' ', '\t':
			flush()
		default:
			token.WriteRune(r)
		}
	}

	if escape {
		return nil, &QAError{ErrCode: ErrCodeCommandInvalid, Detail: "unterminated escape in qa command"}
	}
	if quote != 0 {
		return nil, &QAError{ErrCode: ErrCodeCommandInvalid, Detail: "unterminated quote in qa command"}
	}
	flush()
	return args, nil
}

func truncateOutput(text string, maxBytes int) (string, bool) {
	if maxBytes <= 0 || len(text) <= maxBytes {
		return text, false
	}
	notice := "\n[output truncated]"
	if maxBytes <= len(notice) {
		return notice[:maxBytes], true
	}
	return text[:maxBytes-len(notice)] + notice, true
}

func (r *Runner) AllowedExecutables() []string {
	keys := make([]string, 0, len(r.allowedExecutables))
	for k := range r.allowedExecutables {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
