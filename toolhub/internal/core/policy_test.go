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

func TestPolicyPathChecks(t *testing.T) {
	p := NewPolicy("owner/repo", "github.issues.create")
	p.SetPathPolicy(".github/,infra/", "db/init/,toolhub/internal/db/migrations/")

	if err := p.CheckPaths([]string{"src/app.go", "./docs/readme.md"}); err != nil {
		t.Fatalf("expected allowed paths, got %v", err)
	}
	if err := p.CheckPaths([]string{".github/workflows/ci.yml"}); err == nil {
		t.Fatal("expected forbidden path to be denied")
	}
	if !p.RequiresApproval([]string{"db/init/001_schema.sql"}) {
		t.Fatal("expected approval-required path to require approval")
	}
	if p.RequiresApproval([]string{"src/main.go"}) {
		t.Fatal("unexpected approval requirement for normal path")
	}
}

func TestPolicyPathBuiltinsAlwaysEnforced(t *testing.T) {
	p := NewPolicy("owner/repo", "github.issues.create")
	p.SetPathPolicy("", "")

	cases := []string{".github/workflows/ci.yml", ".git/config", "secrets/key.txt", ".env"}
	for _, path := range cases {
		err := p.CheckPaths([]string{path})
		pv, ok := err.(*PolicyViolation)
		if !ok {
			t.Fatalf("expected PolicyViolation for %q, got %T (%v)", path, err, err)
		}
		if pv.Code != ViolationPathForbidden {
			t.Fatalf("expected forbidden code for %q, got %q", path, pv.Code)
		}
	}
}

func TestPolicyPathBuiltinsMergeWithEnv(t *testing.T) {
	p := NewPolicy("owner/repo", "github.issues.create")
	p.SetPathPolicy(".github/,infra/,.env", "")

	if len(p.forbiddenPathPrefixes) != 5 {
		t.Fatalf("expected 5 unique forbidden prefixes, got %d: %v", len(p.forbiddenPathPrefixes), p.forbiddenPathPrefixes)
	}

	count := 0
	for _, prefix := range p.forbiddenPathPrefixes {
		if prefix == ".github/" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected .github/ to be deduplicated, got count=%d in %v", count, p.forbiddenPathPrefixes)
	}
}

func TestPolicyPathViolationCodes(t *testing.T) {
	p := NewPolicy("owner/repo", "github.issues.create")
	p.SetPathPolicy("infra/", "")

	testCases := []struct {
		name string
		path string
		code PolicyViolationCode
	}{
		{name: "traversal", path: "../secrets.txt", code: ViolationPathTraversal},
		{name: "empty", path: "", code: ViolationPathEmpty},
		{name: "forbidden", path: "infra/deploy.sh", code: ViolationPathForbidden},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := p.CheckPaths([]string{tc.path})
			pv, ok := err.(*PolicyViolation)
			if !ok {
				t.Fatalf("expected PolicyViolation, got %T (%v)", err, err)
			}
			if pv.Code != tc.code {
				t.Fatalf("expected code %q, got %q", tc.code, pv.Code)
			}
		})
	}
}

func TestPolicyPathEnvVariantsBlocked(t *testing.T) {
	p := NewPolicy("owner/repo", "github.issues.create")
	p.SetPathPolicy("", "")

	blocked := []string{".env", ".env.local", ".env.production", "./.env.local"}
	for _, path := range blocked {
		err := p.CheckPaths([]string{path})
		pv, ok := err.(*PolicyViolation)
		if !ok {
			t.Fatalf("expected PolicyViolation for %q, got %T (%v)", path, err, err)
		}
		if pv.Code != ViolationPathForbidden {
			t.Fatalf("expected forbidden code for %q, got %q", path, pv.Code)
		}
	}

	if err := p.CheckPaths([]string{".environment"}); err != nil {
		t.Fatalf("expected .environment to be allowed, got %v", err)
	}
}
