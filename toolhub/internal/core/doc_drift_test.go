//go:build !short

package core_test

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test file location")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..", "..")
	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("cannot resolve repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(abs, "README.md")); err != nil {
		t.Fatalf("repo root %q does not contain README.md", abs)
	}
	return abs
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}
	return string(data)
}

func TestDocDrift_EnvVarsInExample(t *testing.T) {
	root := repoRoot(t)
	mainSrc := readFile(t, filepath.Join(root, "toolhub", "cmd", "toolhub", "main.go"))
	envExample := readFile(t, filepath.Join(root, ".env.example"))

	reGetenv := regexp.MustCompile(`(?:os\.Getenv|requireEnv|envOrDefault)\("([A-Z_]+)"`)
	matches := reGetenv.FindAllStringSubmatch(mainSrc, -1)

	codeVars := make(map[string]bool)
	excludeVars := map[string]bool{
		"DATABASE_URL":  true,
		"ARTIFACTS_DIR": true,
	}
	for _, m := range matches {
		name := m[1]
		if !excludeVars[name] {
			codeVars[name] = true
		}
	}

	envLines := strings.Split(envExample, "\n")
	reEnvLine := regexp.MustCompile(`^([A-Z][A-Z0-9_]*)=`)
	exampleVars := make(map[string]bool)
	for _, line := range envLines {
		if m := reEnvLine.FindStringSubmatch(line); m != nil {
			exampleVars[m[1]] = true
		}
	}

	var missing []string
	for v := range codeVars {
		if !exampleVars[v] {
			missing = append(missing, v)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("env vars referenced in main.go but missing from .env.example:\n  %s",
			strings.Join(missing, "\n  "))
	}
}

func TestDocDrift_HTTPEndpointsInREADME(t *testing.T) {
	root := repoRoot(t)
	openapi := readFile(t, filepath.Join(root, "openapi.yaml"))
	readme := readFile(t, filepath.Join(root, "README.md"))

	reOpenAPIPath := regexp.MustCompile(`(?m)^  (/[^\s:]+):`)
	matches := reOpenAPIPath.FindAllStringSubmatch(openapi, -1)

	skipPaths := map[string]bool{
		"/healthz": true,
		"/version": true,
	}

	openapiPaths := make(map[string]bool)
	for _, m := range matches {
		path := m[1]
		if !skipPaths[path] {
			openapiPaths[path] = true
		}
	}

	var missing []string
	for path := range openapiPaths {
		if !strings.Contains(readme, path) {
			missing = append(missing, path)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("OpenAPI paths not found in README.md:\n  %s",
			strings.Join(missing, "\n  "))
	}
}

func TestDocDrift_MCPToolsInREADME(t *testing.T) {
	root := repoRoot(t)
	mcpSrc := readFile(t, filepath.Join(root, "toolhub", "internal", "mcp", "server.go"))
	readme := readFile(t, filepath.Join(root, "README.md"))

	reToolName := regexp.MustCompile(`"name":\s*"([a-z_]+)"`)
	matches := reToolName.FindAllStringSubmatch(mcpSrc, -1)

	skipNames := map[string]bool{
		"toolhub": true,
	}

	serverTools := make(map[string]bool)
	for _, m := range matches {
		name := m[1]
		if !skipNames[name] {
			serverTools[name] = true
		}
	}

	reMCPSection := regexp.MustCompile(`(?s)## MCP Tools\n(.*?)(?:\n## |\z)`)
	sectionMatch := reMCPSection.FindStringSubmatch(readme)
	if sectionMatch == nil {
		t.Fatal("cannot find '## MCP Tools' section in README.md")
	}
	mcpSection := sectionMatch[1]

	reToolInREADME := regexp.MustCompile("`([a-z_]+)`")
	readmeMatches := reToolInREADME.FindAllStringSubmatch(mcpSection, -1)
	readmeTools := make(map[string]bool)
	for _, m := range readmeMatches {
		readmeTools[m[1]] = true
	}

	var missingInREADME []string
	for tool := range serverTools {
		if !readmeTools[tool] {
			missingInREADME = append(missingInREADME, tool)
		}
	}
	sort.Strings(missingInREADME)

	var missingInServer []string
	for tool := range readmeTools {
		if !serverTools[tool] {
			missingInServer = append(missingInServer, tool)
		}
	}
	sort.Strings(missingInServer)

	if len(missingInREADME) > 0 {
		t.Errorf("MCP tools registered in server.go but missing from README:\n  %s",
			strings.Join(missingInREADME, "\n  "))
	}
	if len(missingInServer) > 0 {
		t.Errorf("MCP tools listed in README but not registered in server.go:\n  %s",
			strings.Join(missingInServer, "\n  "))
	}
}
