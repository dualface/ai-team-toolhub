package http

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseToolCallListFilters(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/v1/runs/r1/tool-calls?status=ok&tool_name=qa.test&created_after=2026-02-25T00:00:00Z&created_before=2026-02-25T01:00:00Z", nil)

	filters, err := parseToolCallListFilters(r)
	if err != nil {
		t.Fatalf("parseToolCallListFilters error: %v", err)
	}
	if filters.Status != "ok" {
		t.Fatalf("status = %q, want ok", filters.Status)
	}
	if filters.ToolName != "qa.test" {
		t.Fatalf("tool_name = %q, want qa.test", filters.ToolName)
	}
	if filters.CreatedAfter == nil || filters.CreatedBefore == nil {
		t.Fatal("expected created_after and created_before to be parsed")
	}
	if !filters.CreatedAfter.Equal(time.Date(2026, 2, 25, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected created_after: %v", filters.CreatedAfter)
	}
	if !filters.CreatedBefore.Equal(time.Date(2026, 2, 25, 1, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected created_before: %v", filters.CreatedBefore)
	}
}

func TestParseToolCallListFilters_Validation(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{name: "invalid status", url: "/api/v1/runs/r1/tool-calls?status=partial"},
		{name: "invalid created_after", url: "/api/v1/runs/r1/tool-calls?created_after=not-a-time"},
		{name: "invalid created_before", url: "/api/v1/runs/r1/tool-calls?created_before=not-a-time"},
		{name: "range inverted", url: "/api/v1/runs/r1/tool-calls?created_after=2026-02-25T02:00:00Z&created_before=2026-02-25T01:00:00Z"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", tt.url, nil)
			if _, err := parseToolCallListFilters(r); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
