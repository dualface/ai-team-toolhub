package core

import "testing"

func TestPolicyCheckRepo(t *testing.T) {
	p := NewPolicy("owner/repo-a,owner/repo-b", "github.issues.create")

	if err := p.CheckRepo("owner/repo-a"); err != nil {
		t.Fatalf("expected allowed, got %v", err)
	}
	if err := p.CheckRepo("owner/repo-b"); err != nil {
		t.Fatalf("expected allowed, got %v", err)
	}
	if err := p.CheckRepo("evil/repo"); err == nil {
		t.Fatal("expected denied for unlisted repo")
	}
}

func TestPolicyCheckTool(t *testing.T) {
	p := NewPolicy("owner/repo", "github.issues.create,github.issues.batch_create")

	if err := p.CheckTool("github.issues.create"); err != nil {
		t.Fatalf("expected allowed, got %v", err)
	}
	if err := p.CheckTool("github.issues.batch_create"); err != nil {
		t.Fatalf("expected allowed, got %v", err)
	}
	if err := p.CheckTool("rm_rf_everything"); err == nil {
		t.Fatal("expected denied for unlisted tool")
	}
}

func TestPolicyEmptyAllowlist(t *testing.T) {
	p := NewPolicy("", "")

	if err := p.CheckRepo("any/repo"); err == nil {
		t.Fatal("expected denied when allowlist is empty")
	}
	if err := p.CheckTool("any.tool"); err == nil {
		t.Fatal("expected denied when allowlist is empty")
	}
}

func TestPolicyCSVWhitespace(t *testing.T) {
	p := NewPolicy(" owner/repo , owner/other ", " tool.a , tool.b ")

	if err := p.CheckRepo("owner/repo"); err != nil {
		t.Fatalf("expected allowed after trimming, got %v", err)
	}
	if err := p.CheckTool("tool.b"); err != nil {
		t.Fatalf("expected allowed after trimming, got %v", err)
	}
}
