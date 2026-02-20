package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/toolhub/toolhub/internal/core"
	httpserver "github.com/toolhub/toolhub/internal/http"
	mcpserver "github.com/toolhub/toolhub/internal/mcp"
)

func main() {
	log.Println("🚀 Starting ToolHub Phase A...")

	// Initialize configuration
	config := &core.Config{
		HTTPListen:   getEnv("TOOLHUB_HTTP_LISTEN", "0.0.0.0:8080"),
		MCPListen:    getEnv("TOOLHUB_MCP_LISTEN", "0.0.0.0:8090"),
		DatabaseURL:  getEnv("DATABASE_URL", ""),
		ArtifactsDir: getEnv("ARTIFACTS_DIR", "/var/lib/toolhub/artifacts"),
	}

	// Start MCP server
	mcpServer := mcpserver.NewServer(config)
	go func() {
		if err := mcpServer.Start(); err != nil {
			log.Printf("MCP server error: %v", err)
		}
	}()

	// Start HTTP server
	httpServer := httpserver.NewServer(config)
	go func() {
		log.Printf("📡 HTTP server listening on %s", config.HTTPListen)
		if err := httpServer.Start(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("⏳ Shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("HTTP server shutdown error: %v", err)
	}

	if err := mcpServer.Shutdown(ctx); err != nil {
		log.Printf("MCP server shutdown error: %v", err)
	}

	log.Println("✅ ToolHub stopped")
}

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
