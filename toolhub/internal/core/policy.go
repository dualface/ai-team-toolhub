package core

import (
	"fmt"
	"strings"
)

// Policy enforces repo and tool allowlists parsed from comma-separated env vars.
type Policy struct {
	allowedRepos          map[string]bool
	allowedTools          map[string]bool
	forbiddenPathPrefixes []string
	approvalPathPrefixes  []string
}

// NewPolicy creates a Policy from comma-separated allowlist strings.
// Empty strings mean "allow nothing".
func NewPolicy(repoCSV, toolCSV string) *Policy {
	return &Policy{
		allowedRepos:          parseCSV(repoCSV),
		allowedTools:          parseCSV(toolCSV),
		forbiddenPathPrefixes: make([]string, 0),
		approvalPathPrefixes:  make([]string, 0),
	}
}

func (p *Policy) SetPathPolicy(forbiddenCSV, approvalCSV string) {
	p.forbiddenPathPrefixes = parsePrefixesCSV(forbiddenCSV)
	p.approvalPathPrefixes = parsePrefixesCSV(approvalCSV)
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

func (p *Policy) CheckPaths(paths []string) error {
	for _, raw := range paths {
		path := normalizePath(raw)
		for _, prefix := range p.forbiddenPathPrefixes {
			if strings.HasPrefix(path, prefix) {
				return fmt.Errorf("path %q forbidden by policy", raw)
			}
		}
	}
	return nil
}

func (p *Policy) RequiresApproval(paths []string) bool {
	for _, raw := range paths {
		path := normalizePath(raw)
		for _, prefix := range p.approvalPathPrefixes {
			if strings.HasPrefix(path, prefix) {
				return true
			}
		}
	}
	return false
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

func parsePrefixesCSV(s string) []string {
	out := make([]string, 0)
	for _, item := range strings.Split(s, ",") {
		item = normalizePath(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func normalizePath(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "./")
	s = strings.TrimPrefix(s, "/")
	return s
}
