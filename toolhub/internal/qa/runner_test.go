package qa

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunnerDryRun(t *testing.T) {
	r := NewRunner(Config{WorkDir: ".", TestCmd: "go test ./...", LintCmd: "go test ./...", Timeout: 5 * time.Second})
	report, err := r.Run(context.Background(), KindTest, true)
	if err != nil {
		t.Fatalf("dry run should not fail: %v", err)
	}
	if report.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", report.ExitCode)
	}
	if report.Command == "" {
		t.Fatal("expected command to be set")
	}
}

func TestRunnerFailureExitCode(t *testing.T) {
	r := NewRunner(Config{WorkDir: ".", TestCmd: "go test ./nonexistent", LintCmd: "go test ./...", Timeout: 15 * time.Second})
	report, err := r.Run(context.Background(), KindTest, false)
	if err == nil {
		t.Fatal("expected command failure")
	}
	if report.ExitCode == 0 {
		t.Fatalf("expected non-zero exit code, got %d", report.ExitCode)
	}
}

func TestRunnerRejectsShellOperators(t *testing.T) {
	r := NewRunner(Config{WorkDir: ".", TestCmd: "go test ./...; echo nope", LintCmd: "go test ./...", Timeout: 5 * time.Second})
	_, err := r.Run(context.Background(), KindTest, false)
	if err == nil {
		t.Fatal("expected command validation error")
	}
	if !strings.Contains(err.Error(), "forbidden shell operator") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunnerTimeout(t *testing.T) {
	r := NewRunner(Config{WorkDir: ".", TestCmd: "sleep 2", LintCmd: "go test ./...", Timeout: 50 * time.Millisecond, AllowedExecutables: []string{"sleep"}})
	report, err := r.Run(context.Background(), KindTest, false)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if report.ExitCode != -1 {
		t.Fatalf("expected timeout exit code -1, got %d", report.ExitCode)
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("unexpected timeout error: %v", err)
	}
}

func TestRunnerOutputTruncation(t *testing.T) {
	r := NewRunner(Config{WorkDir: ".", TestCmd: "go env GOMOD", LintCmd: "go test ./...", Timeout: 5 * time.Second, MaxOutputBytes: 30})
	report, err := r.Run(context.Background(), KindTest, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !report.StdoutTruncated {
		t.Fatal("expected stdout to be truncated")
	}
	if !strings.Contains(report.Stdout, "truncated") {
		t.Fatalf("expected truncation notice, got %q", report.Stdout)
	}
}
