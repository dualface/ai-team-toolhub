package core

import "testing"

func TestValidateIssueInput(t *testing.T) {
	if err := ValidateIssueInput("hello", "body", []string{"bug"}); err != nil {
		t.Fatalf("expected valid input, got %v", err)
	}

	if err := ValidateIssueInput("   ", "body", nil); err == nil {
		t.Fatal("expected title validation error")
	}

	tooLong := make([]byte, MaxIssueTitleLen+1)
	for i := range tooLong {
		tooLong[i] = 'a'
	}
	if err := ValidateIssueInput(string(tooLong), "body", nil); err == nil {
		t.Fatal("expected title length validation error")
	}
}

func TestMakeIssueIdempotencyKeyStableForLabelOrder(t *testing.T) {
	k1, err := MakeIssueIdempotencyKey("run1", "github.issues.create", "title", "body", []string{"b", "a"}, nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	k2, err := MakeIssueIdempotencyKey("run1", "github.issues.create", "title", "body", []string{"a", "b"}, nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if k1 != k2 {
		t.Fatalf("expected equal keys, got %s and %s", k1, k2)
	}
}

func TestMakeIssueIdempotencyKeyIncludesIndex(t *testing.T) {
	i0 := 0
	i1 := 1
	k1, err := MakeIssueIdempotencyKey("run1", "github.issues.batch_create", "title", "body", []string{"a"}, &i0)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	k2, err := MakeIssueIdempotencyKey("run1", "github.issues.batch_create", "title", "body", []string{"a"}, &i1)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if k1 == k2 {
		t.Fatal("expected different keys for different indexes")
	}
}
