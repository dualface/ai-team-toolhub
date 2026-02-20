package core

import (
	"fmt"
	"strings"
)

// Policy enforces repo and tool allowlists parsed from comma-separated env vars.
type Policy struct {
	allowedRepos map[string]bool
	allowedTools map[string]bool
}

// NewPolicy creates a Policy from comma-separated allowlist strings.
// Empty strings mean "allow nothing".
func NewPolicy(repoCSV, toolCSV string) *Policy {
	return &Policy{
		allowedRepos: parseCSV(repoCSV),
		allowedTools: parseCSV(toolCSV),
	}
}

// CheckRepo returns an error if repo is not in the allowlist.
func (p *Policy) CheckRepo(repo string) error {
	if len(p.allowedRepos) == 0 {
		return fmt.Errorf("no repos allowed (REPO_ALLOWLIST is empty)")
	}
	if !p.allowedRepos[repo] {
		return fmt.Errorf("repo %q not in allowlist", repo)
	}
	return nil
}

// CheckTool returns an error if toolName is not in the allowlist.
func (p *Policy) CheckTool(toolName string) error {
	if len(p.allowedTools) == 0 {
		return fmt.Errorf("no tools allowed (TOOL_ALLOWLIST is empty)")
	}
	if !p.allowedTools[toolName] {
		return fmt.Errorf("tool %q not in allowlist", toolName)
	}
	return nil
}

func parseCSV(s string) map[string]bool {
	m := make(map[string]bool)
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			m[item] = true
		}
	}
	return m
}
