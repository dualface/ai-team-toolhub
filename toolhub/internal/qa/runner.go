package qa

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type Kind string

const (
	KindTest Kind = "qa.test"
	KindLint Kind = "qa.lint"
)

type Config struct {
	WorkDir string
	TestCmd string
	LintCmd string
	Timeout time.Duration
}

type Report struct {
	Command    string `json:"command"`
	WorkDir    string `json:"work_dir"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
}

type Runner struct {
	cfg Config
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
	return &Runner{cfg: cfg}
}

func (r *Runner) Run(ctx context.Context, kind Kind, dryRun bool) (Report, error) {
	cmdline, err := r.commandFor(kind)
	if err != nil {
		return Report{}, err
	}
	wd, err := absWorkDir(r.cfg.WorkDir)
	if err != nil {
		return Report{}, err
	}

	if dryRun {
		return Report{Command: cmdline, WorkDir: wd, ExitCode: 0}, nil
	}

	execCtx, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "sh", "-lc", cmdline)
	cmd.Dir = wd
	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	start := time.Now()
	runErr := cmd.Run()
	duration := time.Since(start)

	report := Report{
		Command:    cmdline,
		WorkDir:    wd,
		ExitCode:   0,
		DurationMS: duration.Milliseconds(),
		Stdout:     stdoutBuf.String(),
		Stderr:     stderrBuf.String(),
	}

	if runErr == nil {
		return report, nil
	}

	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		report.ExitCode = exitErr.ExitCode()
		return report, fmt.Errorf("qa command failed with exit code %d", report.ExitCode)
	}

	if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
		report.ExitCode = -1
		return report, fmt.Errorf("qa command timed out after %s", r.cfg.Timeout)
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
