package qa

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRunnerDryRun(t *testing.T) {
	r, err := NewRunner(Config{WorkDir: ".", TestCmd: "go test ./...", LintCmd: "go test ./...", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("new runner should not fail: %v", err)
	}
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

func TestRunnerDryRunSandboxBackend(t *testing.T) {
	r, err := NewRunner(Config{WorkDir: ".", TestCmd: "go test ./...", LintCmd: "go test ./...", Timeout: 5 * time.Second, Backend: "sandbox", AllowedExecutables: []string{"go"}})
	if err != nil {
		t.Fatalf("new runner should not fail: %v", err)
	}
	report, err := r.Run(context.Background(), KindTest, true)
	if err != nil {
		t.Fatalf("dry run should not fail: %v", err)
	}
	if report.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", report.ExitCode)
	}
}

func TestRunnerFailureExitCode(t *testing.T) {
	r, err := NewRunner(Config{WorkDir: ".", TestCmd: "go test ./nonexistent", LintCmd: "go test ./...", Timeout: 15 * time.Second})
	if err != nil {
		t.Fatalf("new runner should not fail: %v", err)
	}
	report, err := r.Run(context.Background(), KindTest, false)
	if err == nil {
		t.Fatal("expected command failure")
	}
	if report.ExitCode == 0 {
		t.Fatalf("expected non-zero exit code, got %d", report.ExitCode)
	}
}

func TestRunnerRejectsShellOperators(t *testing.T) {
	_, err := NewRunner(Config{WorkDir: ".", TestCmd: "go test ./...; echo nope", LintCmd: "go test ./...", Timeout: 5 * time.Second})
	if err == nil {
		t.Fatal("expected command validation error")
	}
	if !strings.Contains(err.Error(), "forbidden shell operator") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunnerTimeout(t *testing.T) {
	r, err := NewRunner(Config{WorkDir: ".", TestCmd: "sleep 2", LintCmd: "go test ./...", Timeout: 50 * time.Millisecond, AllowedExecutables: []string{"sleep", "go"}})
	if err != nil {
		t.Fatalf("new runner should not fail: %v", err)
	}
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
	r, err := NewRunner(Config{WorkDir: ".", TestCmd: "go env GOMOD", LintCmd: "go test ./...", Timeout: 5 * time.Second, MaxOutputBytes: 30})
	if err != nil {
		t.Fatalf("new runner should not fail: %v", err)
	}
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

func TestQAConcurrencyLimit(t *testing.T) {
	r, err := NewRunner(Config{WorkDir: ".", TestCmd: "sleep 1", LintCmd: "sleep 1", Timeout: 3 * time.Second, MaxConcurrency: 1, AllowedExecutables: []string{"sleep"}})
	if err != nil {
		t.Fatalf("new runner should not fail: %v", err)
	}
	if cap(r.semaphore) != 1 {
		t.Fatalf("expected semaphore capacity 1, got %d", cap(r.semaphore))
	}

	firstDone := make(chan error, 1)
	go func() {
		_, runErr := r.Run(context.Background(), KindTest, false)
		firstDone <- runErr
	}()

	deadline := time.Now().Add(500 * time.Millisecond)
	for len(r.semaphore) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(r.semaphore) == 0 {
		t.Fatal("expected first run to acquire semaphore")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = r.Run(ctx, KindTest, false)
	if err == nil {
		t.Fatal("expected second run to fail while waiting for concurrency slot")
	}
	var qaErr *QAError
	if !errors.As(err, &qaErr) {
		t.Fatalf("expected *QAError, got %T", err)
	}
	if qaErr.ErrCode != ErrCodeConcurrencyExceeded {
		t.Fatalf("expected error code %q, got %q", ErrCodeConcurrencyExceeded, qaErr.ErrCode)
	}

	select {
	case runErr := <-firstDone:
		if runErr != nil {
			t.Fatalf("expected first run to succeed, got %v", runErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first run completion")
	}
}

func TestNewRunnerValidation(t *testing.T) {
	tests := []struct {
		name      string
		cfg       Config
		wantErr   bool
		wantCode  string
		errSubstr string
	}{
		{
			name:    "valid_config",
			cfg:     Config{WorkDir: ".", TestCmd: "go test ./...", LintCmd: "go test ./...", AllowedExecutables: []string{"go"}},
			wantErr: false,
		},
		{
			name:      "test_cmd_shell_operator",
			cfg:       Config{WorkDir: ".", TestCmd: "go test ./... && echo nope", LintCmd: "go test ./...", AllowedExecutables: []string{"go"}},
			wantErr:   true,
			wantCode:  ErrCodeCommandInvalid,
			errSubstr: "forbidden shell operator",
		},
		{
			name:      "lint_cmd_unknown_executable",
			cfg:       Config{WorkDir: ".", TestCmd: "go test ./...", LintCmd: "unknownlint ./...", AllowedExecutables: []string{"go"}},
			wantErr:   true,
			wantCode:  ErrCodeCommandNotAllowed,
			errSubstr: "not in allowlist",
		},
		{
			name:      "empty_test_command",
			cfg:       Config{WorkDir: ".", TestCmd: "   ", LintCmd: "go test ./...", AllowedExecutables: []string{"go"}},
			wantErr:   true,
			wantCode:  ErrCodeCommandEmpty,
			errSubstr: "qa command is empty",
		},
		{
			name:      "invalid_backend",
			cfg:       Config{WorkDir: ".", TestCmd: "go test ./...", LintCmd: "go test ./...", AllowedExecutables: []string{"go"}, Backend: "bad_backend"},
			wantErr:   true,
			wantCode:  ErrCodeBackendInvalid,
			errSubstr: "unsupported qa backend",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewRunner(tt.cfg)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected NewRunner error")
				}
				var qaErr *QAError
				if !errors.As(err, &qaErr) {
					t.Fatalf("expected *QAError, got %T", err)
				}
				if qaErr.ErrCode != tt.wantCode {
					t.Fatalf("expected error code %q, got %q", tt.wantCode, qaErr.ErrCode)
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Fatalf("expected error containing %q, got %v", tt.errSubstr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected NewRunner success, got %v", err)
			}
		})
	}
}

func TestDeriveStatus(t *testing.T) {
	tests := []struct {
		name   string
		report Report
		err    error
		dryRun bool
		want   Status
	}{
		{"dry_run", Report{ExitCode: 0}, nil, true, StatusDryRun},
		{"pass", Report{ExitCode: 0}, nil, false, StatusPass},
		{"fail_exit_code", Report{ExitCode: 1}, &QAError{ErrCode: ErrCodeExecFailed, Detail: "qa command failed with exit code 1"}, false, StatusFail},
		{"timeout", Report{ExitCode: -1}, &QAError{ErrCode: ErrCodeTimeout, Detail: "qa command timed out after 5s"}, false, StatusTimeout},
		{"error_generic", Report{ExitCode: -1}, &QAError{ErrCode: ErrCodeCommandNotAllowed, Detail: "qa executable \"foo\" is not in allowlist"}, false, StatusError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeriveStatus(tt.report, tt.err, tt.dryRun)
			if got != tt.want {
				t.Errorf("DeriveStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestQAErrorType(t *testing.T) {
	qaErr := &QAError{ErrCode: ErrCodeCommandInvalid, Detail: "qa command contains forbidden shell operator \";\""}

	var err error = qaErr
	if err.Error() != qaErr.Detail {
		t.Fatalf("expected error detail %q, got %q", qaErr.Detail, err.Error())
	}
	if qaErr.ErrorCode() != ErrCodeCommandInvalid {
		t.Fatalf("expected error code %q, got %q", ErrCodeCommandInvalid, qaErr.ErrorCode())
	}

	wrapped := errors.New("outer")
	wrapped = errors.Join(wrapped, qaErr)
	var extracted *QAError
	if !errors.As(wrapped, &extracted) {
		t.Fatal("expected errors.As to match *QAError")
	}
	if extracted.ErrCode != ErrCodeCommandInvalid {
		t.Fatalf("expected extracted code %q, got %q", ErrCodeCommandInvalid, extracted.ErrCode)
	}
}
