package qa

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type SandboxConfig struct {
	Image            string
	DockerBinary     string
	ContainerWorkDir string
	Timeout          time.Duration
	MaxOutputBytes   int
}

type SandboxRunner struct {
	cfg SandboxConfig
}

func NewSandboxRunner(cfg SandboxConfig) *SandboxRunner {
	if strings.TrimSpace(cfg.Image) == "" {
		cfg.Image = "golang:1.25"
	}
	if strings.TrimSpace(cfg.DockerBinary) == "" {
		cfg.DockerBinary = "docker"
	}
	if strings.TrimSpace(cfg.ContainerWorkDir) == "" {
		cfg.ContainerWorkDir = "/workspace"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Minute
	}
	if cfg.MaxOutputBytes <= 0 {
		cfg.MaxOutputBytes = 256 * 1024
	}
	return &SandboxRunner{cfg: cfg}
}

func (r *SandboxRunner) RunCommand(ctx context.Context, commandLine, hostWorkDir string, dryRun bool) (Report, error) {
	if err := validateCommandLine(commandLine); err != nil {
		return Report{}, err
	}
	cmdArgs, err := splitCommandLine(commandLine)
	if err != nil {
		return Report{}, err
	}
	if len(cmdArgs) == 0 {
		return Report{}, &QAError{ErrCode: ErrCodeCommandEmpty, Detail: "qa command is empty"}
	}

	wd, err := absWorkDir(hostWorkDir)
	if err != nil {
		return Report{}, err
	}

	dockerArgs := []string{
		"run", "--rm",
		"--network", "none",
		"--cpus", "1",
		"--memory", "512m",
		"-w", r.cfg.ContainerWorkDir,
		"-v", wd + ":" + r.cfg.ContainerWorkDir + ":rw",
		r.cfg.Image,
	}
	dockerArgs = append(dockerArgs, cmdArgs...)

	report := Report{
		Command:          r.cfg.DockerBinary + " " + strings.Join(dockerArgs, " "),
		WorkDir:          wd,
		ExitCode:         0,
		OutputLimitBytes: r.cfg.MaxOutputBytes,
	}

	if dryRun {
		return report, nil
	}

	execCtx, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, r.cfg.DockerBinary, dockerArgs...)
	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	start := time.Now()
	runErr := cmd.Run()
	report.DurationMS = time.Since(start).Milliseconds()
	report.Stdout, report.StdoutTruncated = truncateOutput(stdoutBuf.String(), r.cfg.MaxOutputBytes)
	report.Stderr, report.StderrTruncated = truncateOutput(stderrBuf.String(), r.cfg.MaxOutputBytes)

	if runErr == nil {
		return report, nil
	}
	if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
		report.ExitCode = -1
		return report, &QAError{ErrCode: ErrCodeTimeout, Detail: fmt.Sprintf("sandbox qa command timed out after %s", r.cfg.Timeout)}
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		report.ExitCode = exitErr.ExitCode()
		return report, &QAError{ErrCode: ErrCodeExecFailed, Detail: fmt.Sprintf("sandbox qa command failed with exit code %d", report.ExitCode)}
	}
	report.ExitCode = -1
	return report, runErr
}
