package core

import (
	"fmt"
	"strings"
)

// ProfileDefaults holds environment-specific default configuration values.
// Profiles provide defaults only — explicit env vars always override.
type ProfileDefaults struct {
	Name                        string
	PathPolicyForbiddenPrefixes string
	PathPolicyApprovalPrefixes  string
	QATimeoutSeconds            int
	BatchMode                   string

	// RepairMaxIterations is the default max iterations for repair loops.
	// NOTE: not yet wired — currently hardcoded to max 3 in HTTP/MCP handlers.
	// Included here for documentation and future use.
	RepairMaxIterations int
}

var profiles = map[string]*ProfileDefaults{
	"dev": {
		Name:                        "dev",
		PathPolicyForbiddenPrefixes: ".github/,.git/,secrets/,.env",
		PathPolicyApprovalPrefixes:  "",
		QATimeoutSeconds:            600,
		BatchMode:                   "partial",
		RepairMaxIterations:         3,
	},
	"staging": {
		Name:                        "staging",
		PathPolicyForbiddenPrefixes: ".github/,.git/,secrets/,.env,infra/",
		PathPolicyApprovalPrefixes:  "db/init/",
		QATimeoutSeconds:            600,
		BatchMode:                   "partial",
		RepairMaxIterations:         3,
	},
	"prod": {
		Name:                        "prod",
		PathPolicyForbiddenPrefixes: ".github/,.git/,secrets/,.env,infra/,deploy/,terraform/",
		PathPolicyApprovalPrefixes:  "db/init/,toolhub/internal/db/migrations/",
		QATimeoutSeconds:            300,
		BatchMode:                   "strict",
		RepairMaxIterations:         2,
	},
}

// LoadProfile returns profile defaults for the given name.
// Empty name defaults to "dev". Unknown names return an error.
func LoadProfile(name string) (*ProfileDefaults, error) {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		name = "dev"
	}
	p, ok := profiles[name]
	if !ok {
		return nil, fmt.Errorf("unknown profile %q (valid: dev, staging, prod)", name)
	}
	copy := *p
	return &copy, nil
}
