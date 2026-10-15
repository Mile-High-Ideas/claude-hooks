package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/milehighideas/claude-hooks/internal/commitqueue"
	"github.com/milehighideas/claude-hooks/internal/jsonc"
)

// queueConfig is the "commitQueue" block of .pre-commit.json.
type queueConfig struct {
	// Scope is "machine" (default): one queue for every repo on this
	// machine, because the CPU is what they share. "repo" keeps a queue per
	// repository in its git directory.
	Scope string `json:"scope,omitempty"`
	// Slots applies only with scope "repo". A machine queue is shared by
	// repos that may disagree, so it takes --slots or COMMIT_QUEUE_SLOTS.
	Slots int `json:"slots,omitempty"`
	// StaleMinutes reclaims a slot held this long even if its owner looks
	// alive. Default 60.
	StaleMinutes int `json:"staleMinutes,omitempty"`
	// MaxHeaderLength rejects a commit whose subject is longer, before it
	// queues (commitlint would reject it only after the whole suite). 0 = off.
	MaxHeaderLength int `json:"maxHeaderLength,omitempty"`
	// SetupScript, relative to the worktree, runs before the commit; each
	// KEY=VALUE line it prints is added to the commit's environment.
	SetupScript string `json:"setupScript,omitempty"`
}

// repoInfo identifies the repository a worktree belongs to. Every worktree
// of one clone shares the git common directory, so that is the identity.
type repoInfo struct {
	CommonDir string // absolute git common directory
	Key       string // file-safe, unique per repository
	Label     string // short human name
}

func resolveRepo(worktree string) (repoInfo, error) {
	out, err := gitOutput(worktree, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return repoInfo{}, fmt.Errorf("%s is not in a git repository: %w", worktree, err)
	}
	common := filepath.Clean(out)
	return repoInfo{
		CommonDir: common,
		Key:       commitqueue.RepoKey(common),
		Label:     commitqueue.RepoLabel(common),
	}, nil
}

func loadQueueConfig(worktree string) (queueConfig, error) {
	var file struct {
		CommitQueue queueConfig `json:"commitQueue"`
	}
	path := filepath.Join(worktree, ".pre-commit.json")
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return queueConfig{}, nil
	}
	if err := jsonc.Unmarshal(path, &file); err != nil {
		return queueConfig{}, fmt.Errorf("read %s: %w", path, err)
	}
	cfg := file.CommitQueue
	if cfg.Scope != "" && cfg.Scope != "machine" && cfg.Scope != "repo" {
		return queueConfig{}, fmt.Errorf("%s: commitQueue.scope must be \"machine\" or \"repo\", not %q", path, cfg.Scope)
	}
	return cfg, nil
}

// stateDir is where the queue lives: COMMIT_QUEUE_DIR when set, otherwise
// the repo's git directory or the user's state directory, by scope.
func stateDir(cfg queueConfig, repo repoInfo) (string, error) {
	if dir := os.Getenv("COMMIT_QUEUE_DIR"); dir != "" {
		return dir, nil
	}
	if cfg.Scope == "repo" {
		return filepath.Join(repo.CommonDir, "commit-queue"), nil
	}
	return machineStateDir()
}

func machineStateDir() (string, error) {
	if runtime.GOOS == "windows" {
		if base := os.Getenv("LOCALAPPDATA"); base != "" {
			return filepath.Join(base, "claude-hooks", "commit-queue"), nil
		}
	}
	if base := os.Getenv("XDG_STATE_HOME"); base != "" {
		return filepath.Join(base, "claude-hooks", "commit-queue"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".local", "state", "claude-hooks", "commit-queue"), nil
}

// slotCount picks the slot count: the --slots flag, then COMMIT_QUEUE_SLOTS,
// then the repo config (repo scope only), then the default.
func slotCount(flagValue int, cfg queueConfig) (int, error) {
	if flagValue > 0 {
		return flagValue, nil
	}
	if env := os.Getenv("COMMIT_QUEUE_SLOTS"); env != "" {
		n, err := strconv.Atoi(env)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("COMMIT_QUEUE_SLOTS must be a positive whole number, not %q", env)
		}
		return n, nil
	}
	if cfg.Scope == "repo" && cfg.Slots > 0 {
		return cfg.Slots, nil
	}
	return commitqueue.DefaultSlots, nil
}

// openQueue resolves everything a command needs for the worktree.
func openQueue(worktree string, slotsFlag int) (*commitqueue.Queue, queueConfig, repoInfo, error) {
	repo, err := resolveRepo(worktree)
	if err != nil {
		return nil, queueConfig{}, repoInfo{}, err
	}
	cfg, err := loadQueueConfig(worktreeRoot(worktree))
	if err != nil {
		return nil, queueConfig{}, repoInfo{}, err
	}
	dir, err := stateDir(cfg, repo)
	if err != nil {
		return nil, queueConfig{}, repoInfo{}, err
	}
	slots, err := slotCount(slotsFlag, cfg)
	if err != nil {
		return nil, queueConfig{}, repoInfo{}, err
	}
	q, err := commitqueue.New(commitqueue.Options{
		Dir:        dir,
		Slots:      slots,
		StaleAfter: time.Duration(cfg.StaleMinutes) * time.Minute,
	})
	return q, cfg, repo, err
}

// runsDir holds one repo's run logs and exit files.
func runsDir(q *commitqueue.Queue, repo repoInfo) string {
	return filepath.Join(q.Dir(), "runs", repo.Key)
}

func worktreeRoot(dir string) string {
	if out, err := gitOutput(dir, "rev-parse", "--show-toplevel"); err == nil {
		return out
	}
	return dir
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// validName rejects run names that are unsafe as a file name.
func validName(name string) error {
	if name == "" || strings.HasPrefix(name, ".") || commitqueue.FileSafe(name) != name {
		return fmt.Errorf("invalid name %q: use letters, digits, '.', '_' or '-', not starting with '.'", name)
	}
	return nil
}
