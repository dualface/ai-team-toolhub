package http

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/toolhub/toolhub/internal/core"
)

func TestVersionEndpointReturnsDefaults(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	s := NewServer("127.0.0.1:0", nil, nil, nil, nil, nil, nil, logger, core.BatchModePartial, 3, BuildInfo{})

	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	rr := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var got map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if got["version"] != "" {
		t.Fatalf("expected empty version, got %q", got["version"])
	}
	if got["git_commit"] != "" {
		t.Fatalf("expected empty git_commit, got %q", got["git_commit"])
	}
	if got["build_time"] != "" {
		t.Fatalf("expected empty build_time, got %q", got["build_time"])
	}
}

func TestVersionEndpointReturnsInjectedValues(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	s := NewServer("127.0.0.1:0", nil, nil, nil, nil, nil, nil, logger, core.BatchModePartial, 3, BuildInfo{
		Version:   "1.2.3",
		GitCommit: "abc123",
		BuildTime: "2026-02-21T12:00:00Z",
	})

	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	rr := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var got map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if got["version"] != "1.2.3" {
		t.Fatalf("unexpected version: %q", got["version"])
	}
	if got["git_commit"] != "abc123" {
		t.Fatalf("unexpected git_commit: %q", got["git_commit"])
	}
	if got["build_time"] != "2026-02-21T12:00:00Z" {
		t.Fatalf("unexpected build_time: %q", got["build_time"])
	}
}
