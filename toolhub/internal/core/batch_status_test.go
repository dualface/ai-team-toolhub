package core

import "testing"

func TestDeriveBatchStatus(t *testing.T) {
	tests := []struct {
		name     string
		total    int
		replayed int
		errCount int
		want     string
	}{
		{name: "all ok", total: 3, replayed: 0, errCount: 0, want: "ok"},
		{name: "partial", total: 3, replayed: 0, errCount: 1, want: "partial"},
		{name: "all failed", total: 3, replayed: 0, errCount: 3, want: "fail"},
		{name: "all fresh failed with replayed", total: 3, replayed: 1, errCount: 2, want: "fail"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeriveBatchStatus(tt.total, tt.replayed, tt.errCount)
			if got != tt.want {
				t.Fatalf("want %q, got %q", tt.want, got)
			}
		})
	}
}
