package qa

import (
	"context"
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
	r := NewRunner(Config{WorkDir: ".", TestCmd: "sh -lc 'exit 7'", LintCmd: "go test ./...", Timeout: 5 * time.Second})
	report, err := r.Run(context.Background(), KindTest, false)
	if err == nil {
		t.Fatal("expected command failure")
	}
	if report.ExitCode != 7 {
		t.Fatalf("expected exit code 7, got %d", report.ExitCode)
	}
}
