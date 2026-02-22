package codeops

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var branchNameRe = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

type FileChange struct {
	Path            string `json:"path"`
	OriginalContent string `json:"original_content,omitempty"`
	ModifiedContent string `json:"modified_content"`
}

type Config struct {
	WorkDir string
	Remote  string
}

type Runner struct {
	cfg Config
}

type Request struct {
	BaseBranch    string
	HeadBranch    string
	CommitMessage string
	Files         []FileChange
	DryRun        bool
}

type Result struct {
	PlannedCommands []string `json:"planned_commands"`
	CommitHash      string   `json:"commit_hash,omitempty"`
}

func NewRunner(cfg Config) *Runner {
	if strings.TrimSpace(cfg.WorkDir) == "" {
		cfg.WorkDir = "."
	}
	if strings.TrimSpace(cfg.Remote) == "" {
		cfg.Remote = "origin"
	}
	return &Runner{cfg: cfg}
}

func (r *Runner) Execute(ctx context.Context, req Request) (*Result, error) {
	if err := validateBranch(req.BaseBranch); err != nil {
		return nil, err
	}
	if err := validateBranch(req.HeadBranch); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.CommitMessage) == "" {
		return nil, fmt.Errorf("commit_message is required")
	}
	if len(req.Files) == 0 {
		return nil, fmt.Errorf("files is required")
	}

	absWD, err := filepath.Abs(r.cfg.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("resolve workdir: %w", err)
	}

	commands := []string{
		fmt.Sprintf("git -C %q checkout %q", absWD, req.BaseBranch),
		fmt.Sprintf("git -C %q checkout -b %q", absWD, req.HeadBranch),
	}

	for _, f := range req.Files {
		cleanPath, err := safeRelativePath(f.Path)
		if err != nil {
			return nil, err
		}
		commands = append(commands, fmt.Sprintf("write %q", cleanPath))
		commands = append(commands, fmt.Sprintf("git -C %q add %q", absWD, cleanPath))
	}

	commands = append(commands,
		fmt.Sprintf("git -C %q commit -m %q", absWD, req.CommitMessage),
		fmt.Sprintf("git -C %q push -u %q %q", absWD, r.cfg.Remote, req.HeadBranch),
	)

	if req.DryRun {
		return &Result{PlannedCommands: commands}, nil
	}

	if err := runGit(ctx, absWD, "checkout", req.BaseBranch); err != nil {
		return nil, err
	}
	if err := runGit(ctx, absWD, "checkout", "-b", req.HeadBranch); err != nil {
		return nil, err
	}

	for _, f := range req.Files {
		cleanPath, err := safeRelativePath(f.Path)
		if err != nil {
			return nil, err
		}
		full := filepath.Join(absWD, cleanPath)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return nil, fmt.Errorf("mkdir for file %q: %w", cleanPath, err)
		}
		if err := os.WriteFile(full, []byte(f.ModifiedContent), 0o644); err != nil {
			return nil, fmt.Errorf("write file %q: %w", cleanPath, err)
		}
		if err := runGit(ctx, absWD, "add", cleanPath); err != nil {
			return nil, err
		}
	}

	if err := runGit(ctx, absWD, "commit", "-m", req.CommitMessage); err != nil {
		return nil, err
	}
	if err := runGit(ctx, absWD, "push", "-u", r.cfg.Remote, req.HeadBranch); err != nil {
		return nil, err
	}

	out, err := runGitOutput(ctx, absWD, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}

	return &Result{PlannedCommands: commands, CommitHash: strings.TrimSpace(out)}, nil
}

func validateBranch(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return fmt.Errorf("branch name is required")
	}
	if strings.HasPrefix(trimmed, "-") || strings.Contains(trimmed, "..") || strings.Contains(trimmed, " ") || !branchNameRe.MatchString(trimmed) {
		return fmt.Errorf("invalid branch name: %q", name)
	}
	return nil
}

func safeRelativePath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	trimmed = strings.TrimPrefix(trimmed, "./")
	if trimmed == "" {
		return "", fmt.Errorf("file path is required")
	}
	if strings.HasPrefix(trimmed, "/") {
		return "", fmt.Errorf("absolute file path is not allowed: %q", path)
	}
	cleaned := filepath.Clean(trimmed)
	if cleaned == "." || strings.HasPrefix(cleaned, "..") {
		return "", fmt.Errorf("unsafe file path: %q", path)
	}
	return cleaned, nil
}

func runGit(ctx context.Context, workdir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", workdir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s failed: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func runGitOutput(ctx context.Context, workdir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", workdir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s failed: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
