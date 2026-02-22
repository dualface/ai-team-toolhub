package qa

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSandboxRunnerDefaults(t *testing.T) {
	r := NewSandboxRunner(SandboxConfig{})
	if r.cfg.Image == "" || r.cfg.DockerBinary == "" || r.cfg.ContainerWorkDir == "" {
		t.Fatalf("unexpected defaults: %#v", r.cfg)
	}
	if r.cfg.Timeout <= 0 || r.cfg.MaxOutputBytes <= 0 {
		t.Fatalf("invalid defaults: %#v", r.cfg)
	}
}

func TestSandboxRunnerDryRun(t *testing.T) {
	r := NewSandboxRunner(SandboxConfig{})
	report, err := r.RunCommand(context.Background(), "go test ./...", ".", true)
	if err != nil {
		t.Fatalf("dry run failed: %v", err)
	}
	if report.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", report.ExitCode)
	}
	if !strings.Contains(report.Command, "docker run") {
		t.Fatalf("expected docker command, got %q", report.Command)
	}
}

func TestSandboxRunnerRunWithFakeDocker(t *testing.T) {
	tmp := t.TempDir()
	fake := filepath.Join(tmp, "fake-docker.sh")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}

	r := NewSandboxRunner(SandboxConfig{DockerBinary: fake, Timeout: 2 * time.Second})
	report, err := r.RunCommand(context.Background(), "echo hello", ".", false)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if report.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", report.ExitCode)
	}
	if !strings.Contains(report.Stdout, "run") || !strings.Contains(report.Stdout, "echo") {
		t.Fatalf("unexpected stdout: %q", report.Stdout)
	}
}
