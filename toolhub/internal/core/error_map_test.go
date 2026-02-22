package core

import (
	"errors"
	"testing"
)

type testCodedError struct{ code, msg string }

func (e *testCodedError) Error() string     { return e.msg }
func (e *testCodedError) ErrorCode() string { return e.code }

func TestMapErrorCommonCases(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		fallback int
		wantCode string
		wantHTTP int
	}{
		{name: "repo allowlist", err: errors.New("repo \"x/y\" not in allowlist"), fallback: 500, wantCode: "repo_not_allowed", wantHTTP: 403},
		{name: "tool allowlist", err: errors.New("tool \"z\" not in allowlist"), fallback: 500, wantCode: "tool_not_allowed", wantHTTP: 403},
		{name: "github 403", err: errors.New("create issue HTTP 403: denied"), fallback: 502, wantCode: "github_permission_denied", wantHTTP: 502},
		{name: "github 422", err: errors.New("create issue HTTP 422: validation"), fallback: 502, wantCode: "github_validation_failed", wantHTTP: 400},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MapError(tt.err, tt.fallback)
			if got.Code != tt.wantCode {
				t.Fatalf("want code %q, got %q", tt.wantCode, got.Code)
			}
			if got.HTTPStatus != tt.wantHTTP {
				t.Fatalf("want status %d, got %d", tt.wantHTTP, got.HTTPStatus)
			}
		})
	}
}

func TestMapErrorQACodedErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		fallback int
		wantCode string
		wantHTTP int
	}{
		{name: "qa command empty", err: &testCodedError{code: "qa_command_empty", msg: "qa command is empty"}, fallback: 500, wantCode: "qa_command_empty", wantHTTP: 400},
		{name: "qa command not allowed", err: &testCodedError{code: "qa_command_not_allowed", msg: "qa executable \"foo\" is not in allowlist"}, fallback: 500, wantCode: "qa_command_not_allowed", wantHTTP: 403},
		{name: "qa backend invalid", err: &testCodedError{code: "qa_backend_invalid", msg: "unsupported qa backend: nope"}, fallback: 500, wantCode: "qa_backend_invalid", wantHTTP: 400},
		{name: "qa timeout", err: &testCodedError{code: "qa_timeout", msg: "qa command timed out after 5s"}, fallback: 500, wantCode: "qa_timeout", wantHTTP: 200},
		{name: "qa exec failed", err: &testCodedError{code: "qa_execution_failed", msg: "qa command failed with exit code 1"}, fallback: 500, wantCode: "qa_execution_failed", wantHTTP: 200},
		{name: "idempotency conflict", err: &testCodedError{code: "idempotency_key_conflict", msg: "idempotency key reused with different request payload"}, fallback: 500, wantCode: "idempotency_key_conflict", wantHTTP: 409},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MapError(tt.err, tt.fallback)
			if got.Code != tt.wantCode {
				t.Fatalf("want code %q, got %q", tt.wantCode, got.Code)
			}
			if got.HTTPStatus != tt.wantHTTP {
				t.Fatalf("want status %d, got %d", tt.wantHTTP, got.HTTPStatus)
			}
		})
	}
}
