package core

import "testing"

func TestParseBatchMode(t *testing.T) {
	mode, err := ParseBatchMode("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != BatchModePartial {
		t.Fatalf("expected partial default, got %s", mode)
	}

	mode, err = ParseBatchMode("STRICT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != BatchModeStrict {
		t.Fatalf("expected strict, got %s", mode)
	}

	if _, err := ParseBatchMode("foo"); err == nil {
		t.Fatal("expected parse error for invalid mode")
	}
}
