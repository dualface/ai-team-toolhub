package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/toolhub/toolhub/internal/core"
	"github.com/toolhub/toolhub/internal/db"
	gh "github.com/toolhub/toolhub/internal/github"
	httpsvr "github.com/toolhub/toolhub/internal/http"
	mcpsvr "github.com/toolhub/toolhub/internal/mcp"
)

var (
	version   = ""
	gitCommit = ""
	buildTime = ""
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	database, err := db.New(requireEnv("DATABASE_URL"))
	if err != nil {
		logger.Error("database connection failed", "err", err)
		os.Exit(1)
	}
	defer database.Close()

	artifactsDir := requireEnv("ARTIFACTS_DIR")
	artifactStore, err := core.NewArtifactStore(database, artifactsDir)
	if err != nil {
		logger.Error("artifact store init failed", "err", err)
		os.Exit(1)
	}

	policy := core.NewPolicy(
		os.Getenv("REPO_ALLOWLIST"),
		os.Getenv("TOOL_ALLOWLIST"),
	)

	runService := core.NewRunService(database)
	auditService := core.NewAuditService(database, artifactStore, policy)

	appID, err := strconv.ParseInt(requireEnv("GITHUB_APP_ID"), 10, 64)
	if err != nil {
		logger.Error("invalid GITHUB_APP_ID", "err", err)
		os.Exit(1)
	}
	var installationID int64
	if rawInstallationID := os.Getenv("GITHUB_INSTALLATION_ID"); rawInstallationID != "" {
		installationID, err = strconv.ParseInt(rawInstallationID, 10, 64)
		if err != nil {
			logger.Error("invalid GITHUB_INSTALLATION_ID", "err", err)
			os.Exit(1)
		}
	}

	ghClient, err := gh.NewClient(appID, installationID, requireEnv("GITHUB_PRIVATE_KEY_PATH"))
	if err != nil {
		logger.Error("github client init failed", "err", err)
		os.Exit(1)
	}

	httpAddr := envOrDefault("TOOLHUB_HTTP_LISTEN", "0.0.0.0:8080")
	mcpAddr := envOrDefault("TOOLHUB_MCP_LISTEN", "0.0.0.0:8090")
	batchMode, err := core.ParseBatchMode(os.Getenv("BATCH_MODE"))
	if err != nil {
		logger.Error("invalid BATCH_MODE", "err", err)
		os.Exit(1)
	}

	httpServer := httpsvr.NewServer(httpAddr, runService, auditService, policy, ghClient, logger, batchMode, httpsvr.BuildInfo{
		Version:   version,
		GitCommit: gitCommit,
		BuildTime: buildTime,
	})
	mcpServer := mcpsvr.NewServer(mcpAddr, runService, auditService, policy, ghClient, logger, batchMode)

	errCh := make(chan error, 2)
	go func() { errCh <- httpServer.ListenAndServe() }()
	go func() { errCh <- mcpServer.ListenAndServe() }()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		logger.Info("shutting down", "signal", sig.String())
	case err := <-errCh:
		logger.Error("server error", "err", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	httpServer.Shutdown(ctx)
	mcpServer.Shutdown(ctx)
	logger.Info("shutdown complete")
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		slog.Error("required env var missing", "key", key)
		os.Exit(1)
	}
	return v
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
