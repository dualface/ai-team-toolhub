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

	profileName := strings.TrimSpace(os.Getenv("TOOLHUB_PROFILE"))
	profile, err := core.LoadProfile(profileName)
	if err != nil {
		logger.Error("invalid TOOLHUB_PROFILE", "value", profileName, "err", err)
		os.Exit(1)
	}
	logger.Info("profile loaded", "profile", profile.Name)

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
	forbiddenPrefixes := envOrDefault("PATH_POLICY_FORBIDDEN_PREFIXES", profile.PathPolicyForbiddenPrefixes)
	approvalPrefixes := envOrDefault("PATH_POLICY_APPROVAL_PREFIXES", profile.PathPolicyApprovalPrefixes)
	policy.SetPathPolicy(forbiddenPrefixes, approvalPrefixes)

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

	qaTimeoutSecs := profile.QATimeoutSeconds
	if raw := strings.TrimSpace(os.Getenv("QA_TIMEOUT_SECONDS")); raw != "" {
		secs, parseErr := strconv.Atoi(raw)
		if parseErr != nil || secs <= 0 {
			logger.Error("invalid QA_TIMEOUT_SECONDS", "value", raw)
			os.Exit(1)
		}
		qaTimeoutSecs = secs
	}
	qaTimeout := time.Duration(qaTimeoutSecs) * time.Second
	repairMaxIterations := profile.RepairMaxIterations
	if raw := strings.TrimSpace(os.Getenv("REPAIR_MAX_ITERATIONS")); raw != "" {
		v, parseErr := strconv.Atoi(raw)
		if parseErr != nil || v < 1 || v > 10 {
			logger.Error("invalid REPAIR_MAX_ITERATIONS", "value", raw, "allowed_range", "1..10")
			os.Exit(1)
		}
		repairMaxIterations = v
	}
	if repairMaxIterations < 1 || repairMaxIterations > 10 {
		logger.Error("invalid repair max iterations from profile", "value", repairMaxIterations, "allowed_range", "1..10")
		os.Exit(1)
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
	batchMode, err := core.ParseBatchMode(envOrDefault("BATCH_MODE", profile.BatchMode))
	if err != nil {
		logger.Error("invalid BATCH_MODE", "err", err)
		os.Exit(1)
	}

	logger.Info("effective config",
		"profile", profile.Name,
		"path_policy_forbidden_prefixes", forbiddenPrefixes,
		"path_policy_approval_prefixes", approvalPrefixes,
		"qa_timeout_seconds", qaTimeoutSecs,
		"repair_max_iterations", repairMaxIterations,
		"batch_mode", string(batchMode),
	)

	httpServer := httpsvr.NewServer(httpAddr, runService, auditService, policy, ghClient, qaRunner, codeRunner, logger, batchMode, repairMaxIterations, httpsvr.BuildInfo{
		Version:         version,
		GitCommit:       gitCommit,
		BuildTime:       buildTime,
		ContractVersion: core.ContractVersion,
	})
	mcpServer := mcpsvr.NewServer(mcpAddr, runService, auditService, policy, ghClient, qaRunner, codeRunner, logger, batchMode, repairMaxIterations)

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
