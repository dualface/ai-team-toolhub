package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/toolhub/toolhub/internal/codeops"
	"github.com/toolhub/toolhub/internal/core"
	"github.com/toolhub/toolhub/internal/db"
	gh "github.com/toolhub/toolhub/internal/github"
	httpsvr "github.com/toolhub/toolhub/internal/http"
	mcpsvr "github.com/toolhub/toolhub/internal/mcp"
	"github.com/toolhub/toolhub/internal/qa"
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
	policy.SetPathPolicy(
		os.Getenv("PATH_POLICY_FORBIDDEN_PREFIXES"),
		os.Getenv("PATH_POLICY_APPROVAL_PREFIXES"),
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

	qaTimeout := 10 * time.Minute
	if raw := strings.TrimSpace(os.Getenv("QA_TIMEOUT_SECONDS")); raw != "" {
		secs, parseErr := strconv.Atoi(raw)
		if parseErr != nil || secs <= 0 {
			logger.Error("invalid QA_TIMEOUT_SECONDS", "value", raw)
			os.Exit(1)
		}
		qaTimeout = time.Duration(secs) * time.Second
	}
	qaMaxOutputBytes := 256 * 1024
	if raw := strings.TrimSpace(os.Getenv("QA_MAX_OUTPUT_BYTES")); raw != "" {
		bytes, parseErr := strconv.Atoi(raw)
		if parseErr != nil || bytes <= 0 {
			logger.Error("invalid QA_MAX_OUTPUT_BYTES", "value", raw)
			os.Exit(1)
		}
		qaMaxOutputBytes = bytes
	}
	qaMaxConcurrency := 0
	if raw := strings.TrimSpace(os.Getenv("QA_MAX_CONCURRENCY")); raw != "" {
		if concurrency, parseErr := strconv.Atoi(raw); parseErr == nil && concurrency > 0 {
			qaMaxConcurrency = concurrency
		}
	}
	qaAllowedExecutables := []string{"go", "make", "pytest", "python", "python3", "npm", "npx", "yarn", "pnpm", "ruff", "eslint", "golangci-lint"}
	if raw := strings.TrimSpace(os.Getenv("QA_ALLOWED_EXECUTABLES")); raw != "" {
		qaAllowedExecutables = splitCSV(raw)
		if len(qaAllowedExecutables) == 0 {
			logger.Error("invalid QA_ALLOWED_EXECUTABLES", "value", raw)
			os.Exit(1)
		}
	}
	qaBackend := strings.TrimSpace(envOrDefault("QA_BACKEND", "local"))
	qaSandboxImage := strings.TrimSpace(envOrDefault("QA_SANDBOX_IMAGE", "golang:1.25"))
	qaSandboxDockerBin := strings.TrimSpace(envOrDefault("QA_SANDBOX_DOCKER_BIN", "docker"))
	qaSandboxContainerWD := strings.TrimSpace(envOrDefault("QA_SANDBOX_CONTAINER_WORKDIR", "/workspace"))
	qaRunner, err := qa.NewRunner(qa.Config{
		WorkDir:            envOrDefault("QA_WORKDIR", "."),
		TestCmd:            envOrDefault("QA_TEST_CMD", "go -C toolhub test ./..."),
		LintCmd:            envOrDefault("QA_LINT_CMD", "go -C toolhub test ./..."),
		Timeout:            qaTimeout,
		MaxOutputBytes:     qaMaxOutputBytes,
		MaxConcurrency:     qaMaxConcurrency,
		Backend:            qaBackend,
		SandboxImage:       qaSandboxImage,
		SandboxDockerBin:   qaSandboxDockerBin,
		SandboxContainerWD: qaSandboxContainerWD,
		AllowedExecutables: qaAllowedExecutables,
	})
	if err != nil {
		logger.Error("qa runner init failed", "err", err)
		os.Exit(1)
	}

	codeRunner := codeops.NewRunner(codeops.Config{
		WorkDir: envOrDefault("CODE_WORKDIR", envOrDefault("QA_WORKDIR", ".")),
		Remote:  envOrDefault("CODE_GIT_REMOTE", "origin"),
	})

	httpAddr := envOrDefault("TOOLHUB_HTTP_LISTEN", "0.0.0.0:8080")
	mcpAddr := envOrDefault("TOOLHUB_MCP_LISTEN", "0.0.0.0:8090")
	batchMode, err := core.ParseBatchMode(os.Getenv("BATCH_MODE"))
	if err != nil {
		logger.Error("invalid BATCH_MODE", "err", err)
		os.Exit(1)
	}

	httpServer := httpsvr.NewServer(httpAddr, runService, auditService, policy, ghClient, qaRunner, codeRunner, logger, batchMode, httpsvr.BuildInfo{
		Version:   version,
		GitCommit: gitCommit,
		BuildTime: buildTime,
	})
	mcpServer := mcpsvr.NewServer(mcpAddr, runService, auditService, policy, ghClient, qaRunner, codeRunner, logger, batchMode)

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

func splitCSV(raw string) []string {
	out := make([]string, 0)
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
