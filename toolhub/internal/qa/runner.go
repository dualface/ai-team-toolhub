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

type Runner struct {
	cfg                Config
	allowedExecutables map[string]bool
}

func NewRunner(cfg Config) *Runner {
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
	return &Runner{cfg: cfg, allowedExecutables: allowed}
}

func (r *Runner) Run(ctx context.Context, kind Kind, dryRun bool) (Report, error) {
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
		return Report{}, fmt.Errorf("qa command is empty")
	}
	if !r.allowedExecutables[args[0]] {
		return Report{}, fmt.Errorf("qa executable %q is not in allowlist", args[0])
	}
	wd, err := absWorkDir(r.cfg.WorkDir)
	if err != nil {
		return Report{}, err
	}

	if dryRun {
		return Report{Command: cmdline, WorkDir: wd, ExitCode: 0, OutputLimitBytes: r.cfg.MaxOutputBytes}, nil
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
		return report, fmt.Errorf("qa command timed out after %s", r.cfg.Timeout)
	}

	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		report.ExitCode = exitErr.ExitCode()
		return report, fmt.Errorf("qa command failed with exit code %d", report.ExitCode)
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
		return "", fmt.Errorf("unsupported qa tool: %s", kind)
	}
}

func absWorkDir(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("qa workdir is empty")
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
		return fmt.Errorf("qa command is empty")
	}
	dangerous := []string{"&&", "||", ";", "|", "$(", "`", ">", "<", "\n", "\r"}
	for _, token := range dangerous {
		if strings.Contains(trimmed, token) {
			return fmt.Errorf("qa command contains forbidden shell operator %q", token)
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
		return nil, fmt.Errorf("unterminated escape in qa command")
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote in qa command")
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
