package core

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Policy enforces repo and tool allowlists parsed from comma-separated env vars.
type Policy struct {
	allowedRepos          map[string]bool
	allowedTools          map[string]bool
	forbiddenPathPrefixes []string
	approvalPathPrefixes  []string
}

var builtinForbiddenPrefixes = []string{
	".github/",
	".git/",
	"secrets/",
	".env",
}

// NewPolicy creates a Policy from comma-separated allowlist strings.
// Empty strings mean "allow nothing".
func NewPolicy(repoCSV, toolCSV string) *Policy {
	return &Policy{
		allowedRepos:          parseCSV(repoCSV),
		allowedTools:          parseCSV(toolCSV),
		forbiddenPathPrefixes: append([]string{}, builtinForbiddenPrefixes...),
		approvalPathPrefixes:  make([]string, 0),
	}
}

func (p *Policy) SetPathPolicy(forbiddenCSV, approvalCSV string) {
	p.forbiddenPathPrefixes = mergeUniquePrefixes(builtinForbiddenPrefixes, parsePrefixesCSV(forbiddenCSV))
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
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return &PolicyViolation{Code: ViolationPathEmpty, Path: raw, Reason: "path is empty"}
		}

		path, err := canonicalizePath(trimmed)
		if err != nil {
			return &PolicyViolation{Code: ViolationPathTraversal, Path: raw, Reason: err.Error()}
		}
		for _, prefix := range p.forbiddenPathPrefixes {
			if matchesForbiddenPrefix(path, prefix) {
				return &PolicyViolation{Code: ViolationPathForbidden, Path: raw, Reason: fmt.Sprintf("matched forbidden prefix %q", prefix)}
			}
		}
	}
	return nil
}

func (p *Policy) RequiresApproval(paths []string) bool {
	for _, raw := range paths {
		path, err := canonicalizePath(raw)
		if err != nil {
			return true // treat unparseable paths as requiring approval
		}
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

func mergeUniquePrefixes(base []string, extra []string) []string {
	seen := make(map[string]struct{}, len(base)+len(extra))
	out := make([]string, 0, len(base)+len(extra))
	for _, prefix := range append(append([]string{}, base...), extra...) {
		if _, ok := seen[prefix]; ok {
			continue
		}
		seen[prefix] = struct{}{}
		out = append(out, prefix)
	}
	return out
}

func matchesForbiddenPrefix(path, prefix string) bool {
	if strings.HasSuffix(prefix, "/") {
		return strings.HasPrefix(path, prefix)
	}
	if strings.HasPrefix(prefix, ".") {
		return path == prefix || strings.HasPrefix(path, prefix+".")
	}
	return strings.HasPrefix(path, prefix)
}

// normalizePath performs basic cleanup for prefix config values.
func normalizePath(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "./")
	s = strings.TrimPrefix(s, "/")
	return s
}

// canonicalizePath fully resolves traversal sequences and rejects unsafe paths.
// It uses filepath.Clean to collapse ".." segments, then strips leading "/" and "./".
// Paths that escape the root (resolve to "..") are rejected.
func canonicalizePath(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("empty path")
	}

	cleaned := filepath.Clean(s)

	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "..\\") {
		return "", fmt.Errorf("path traversal detected")
	}

	cleaned = strings.TrimPrefix(cleaned, "/")
	cleaned = strings.TrimPrefix(cleaned, "./")
	if cleaned == "." {
		return "", fmt.Errorf("path resolves to root")
	}
	return cleaned, nil
}
