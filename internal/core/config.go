package core

import (
	"time"
)

// Config holds the application configuration
type Config struct {
	HTTPListen   string
	MCPListen    string
	DatabaseURL  string
	ArtifactsDir string
}
