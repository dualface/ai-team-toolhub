package core

import (
	"errors"
	"testing"
)

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
