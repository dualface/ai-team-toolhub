package core

import (
	"testing"
)

func TestLoadProfile_Dev(t *testing.T) {
	p, err := LoadProfile("dev")
	if err != nil {
		t.Fatalf("LoadProfile(dev) error: %v", err)
	}
	if p.Name != "dev" {
		t.Errorf("Name = %q, want %q", p.Name, "dev")
	}
	if p.PathPolicyForbiddenPrefixes != ".github/,.git/,secrets/,.env" {
		t.Errorf("ForbiddenPrefixes = %q", p.PathPolicyForbiddenPrefixes)
	}
	if p.PathPolicyApprovalPrefixes != "" {
		t.Errorf("ApprovalPrefixes = %q, want empty", p.PathPolicyApprovalPrefixes)
	}
	if p.QATimeoutSeconds != 600 {
		t.Errorf("QATimeoutSeconds = %d, want 600", p.QATimeoutSeconds)
	}
	if p.BatchMode != "partial" {
		t.Errorf("BatchMode = %q, want %q", p.BatchMode, "partial")
	}
	if p.RepairMaxIterations != 3 {
		t.Errorf("RepairMaxIterations = %d, want 3", p.RepairMaxIterations)
	}
}

func TestLoadProfile_Staging(t *testing.T) {
	p, err := LoadProfile("staging")
	if err != nil {
		t.Fatalf("LoadProfile(staging) error: %v", err)
	}
	if p.Name != "staging" {
		t.Errorf("Name = %q, want %q", p.Name, "staging")
	}
	if p.PathPolicyForbiddenPrefixes != ".github/,.git/,secrets/,.env,infra/" {
		t.Errorf("ForbiddenPrefixes = %q", p.PathPolicyForbiddenPrefixes)
	}
	if p.PathPolicyApprovalPrefixes != "db/init/" {
		t.Errorf("ApprovalPrefixes = %q, want %q", p.PathPolicyApprovalPrefixes, "db/init/")
	}
	if p.QATimeoutSeconds != 600 {
		t.Errorf("QATimeoutSeconds = %d, want 600", p.QATimeoutSeconds)
	}
	if p.BatchMode != "partial" {
		t.Errorf("BatchMode = %q, want %q", p.BatchMode, "partial")
	}
	if p.RepairMaxIterations != 3 {
		t.Errorf("RepairMaxIterations = %d, want 3", p.RepairMaxIterations)
	}
}

func TestLoadProfile_Prod(t *testing.T) {
	p, err := LoadProfile("prod")
	if err != nil {
		t.Fatalf("LoadProfile(prod) error: %v", err)
	}
	if p.Name != "prod" {
		t.Errorf("Name = %q, want %q", p.Name, "prod")
	}
	if p.PathPolicyForbiddenPrefixes != ".github/,.git/,secrets/,.env,infra/,deploy/,terraform/" {
		t.Errorf("ForbiddenPrefixes = %q", p.PathPolicyForbiddenPrefixes)
	}
	if p.PathPolicyApprovalPrefixes != "db/init/,toolhub/internal/db/migrations/" {
		t.Errorf("ApprovalPrefixes = %q", p.PathPolicyApprovalPrefixes)
	}
	if p.QATimeoutSeconds != 300 {
		t.Errorf("QATimeoutSeconds = %d, want 300", p.QATimeoutSeconds)
	}
	if p.BatchMode != "strict" {
		t.Errorf("BatchMode = %q, want %q", p.BatchMode, "strict")
	}
	if p.RepairMaxIterations != 2 {
		t.Errorf("RepairMaxIterations = %d, want 2", p.RepairMaxIterations)
	}
}

func TestLoadProfile_EmptyDefaultsToDev(t *testing.T) {
	p, err := LoadProfile("")
	if err != nil {
		t.Fatalf("LoadProfile(\"\") error: %v", err)
	}
	if p.Name != "dev" {
		t.Errorf("Name = %q, want %q", p.Name, "dev")
	}
}

func TestLoadProfile_CaseInsensitive(t *testing.T) {
	p, err := LoadProfile("PROD")
	if err != nil {
		t.Fatalf("LoadProfile(PROD) error: %v", err)
	}
	if p.Name != "prod" {
		t.Errorf("Name = %q, want %q", p.Name, "prod")
	}
}

func TestLoadProfile_UnknownReturnsError(t *testing.T) {
	_, err := LoadProfile("unknown")
	if err == nil {
		t.Fatal("LoadProfile(unknown) should return error")
	}
}

func TestLoadProfile_ReturnsCopy(t *testing.T) {
	p1, _ := LoadProfile("dev")
	p2, _ := LoadProfile("dev")
	p1.QATimeoutSeconds = 9999
	if p2.QATimeoutSeconds == 9999 {
		t.Error("LoadProfile should return independent copies")
	}
}
