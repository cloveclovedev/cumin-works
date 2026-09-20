// Package agent starts an agent for one request and returns its result.
//
// This file prepares the work directory of a request: one clone for each
// repository under the setting work_dir, and one git worktree for each
// issue and role. docs/ja/designs/agent-run.md ("作業場所") records the
// layout and the rules. The git commands come from the official git
// documentation (git-clone, git-fetch, git-worktree, git-branch).
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
)

// cloneDirName is the clone under <work_dir>/<owner>/<repo>. It has no
// checked-out files. It is only the parent of the worktrees.
const cloneDirName = "clone"

// Workspace is the work directory of cumin, the setting work_dir. Prepare
// and Remove resolve a relative Root against the working directory of the
// process.
type Workspace struct {
	Root string
	// Logger may be nil. Then the default logger is used.
	Logger *slog.Logger
}

// Checkout names one worktree: the repository, the issue, and the role.
type Checkout struct {
	Owner string
	Repo  string
	Issue int
	Role  config.Role
	// Branch is the branch of a role that writes. An empty Branch gives a
	// detached checkout of the default branch, for a role that reads only.
	Branch string
}

func (c Checkout) validate() error {
	var errs []error
	if !isPathElement(c.Owner) {
		errs = append(errs, fmt.Errorf("owner %q must be one path element", c.Owner))
	}
	if !isPathElement(c.Repo) {
		errs = append(errs, fmt.Errorf("repo %q must be one path element", c.Repo))
	}
	if c.Issue <= 0 {
		errs = append(errs, fmt.Errorf("issue %d must be 1 or more", c.Issue))
	}
	if c.Role == "" {
		errs = append(errs, errors.New("role must not be empty"))
	}
	if strings.HasPrefix(c.Branch, "-") {
		errs = append(errs, fmt.Errorf("branch %q must not start with -", c.Branch))
	}
	return errors.Join(errs...)
}

// isPathElement reports whether s can be one element of a path under the
// work directory: not empty, no separator, and not "." or "..". A name
// such as ".." would leave the work directory.
func isPathElement(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, "/\\")
}

// root is the work directory as an absolute path. git runs with the clone
// as its working directory, so a relative path would be resolved from
// there, not from the process.
func (w Workspace) root() (string, error) {
	root, err := filepath.Abs(w.Root)
	if err != nil {
		return "", fmt.Errorf("work directory %q: %w", w.Root, err)
	}
	return root, nil
}

// repoDir is <work_dir>/<owner>/<repo>.
func (w Workspace) repoDir(c Checkout) string {
	return filepath.Join(w.Root, c.Owner, c.Repo)
}

// CloneDir is the clone of the repository of c. It is created by Prepare.
func (w Workspace) CloneDir(c Checkout) string {
	return filepath.Join(w.repoDir(c), cloneDirName)
}

// Dir is the worktree of c: <work_dir>/<owner>/<repo>/<issue>-<role>.
func (w Workspace) Dir(c Checkout) string {
	return filepath.Join(w.repoDir(c), strconv.Itoa(c.Issue)+"-"+string(c.Role))
}

// Prepare returns the worktree of c, and creates it when it does not exist.
//
// The clone is created on the first call for the repository, from
// remoteURL, and is fetched before each new worktree. A new worktree with
// a branch starts from origin/<branch> when that exists, and from
// origin/HEAD otherwise. A new worktree without a branch is a detached
// checkout of origin/HEAD. An existing worktree is returned as it is.
func (w Workspace) Prepare(ctx context.Context, remoteURL string, c Checkout) (string, error) {
	if err := c.validate(); err != nil {
		return "", fmt.Errorf("prepare worktree: %w", err)
	}
	if remoteURL == "" {
		return "", errors.New("prepare worktree: remote URL must not be empty")
	}
	root, err := w.root()
	if err != nil {
		return "", fmt.Errorf("prepare worktree: %w", err)
	}
	w.Root = root
	dir := w.Dir(c)
	log := w.logger().With("repository", c.Owner+"/"+c.Repo, "issue", c.Issue, "role", c.Role)

	if _, err := os.Stat(dir); err == nil {
		log.Info("worktree reused", "branch", c.Branch)
		return dir, nil
	}

	clone := w.CloneDir(c)
	if _, err := os.Stat(clone); err != nil {
		if err := os.MkdirAll(w.repoDir(c), 0o755); err != nil {
			return "", fmt.Errorf("prepare worktree: %w", err)
		}
		// --no-checkout: the clone holds no files of its own.
		if _, err := w.git(ctx, w.repoDir(c), "clone", "--quiet", "--no-checkout", "--", remoteURL, cloneDirName); err != nil {
			return "", fmt.Errorf("prepare worktree: %w", err)
		}
		log.Info("clone created")
	}

	// Keep the clone up to date, and forget worktrees whose directory is gone.
	steps := [][]string{
		{"fetch", "--quiet", "--prune", "origin"},
		{"worktree", "prune"},
	}
	for _, args := range steps {
		if _, err := w.git(ctx, clone, args...); err != nil {
			return "", fmt.Errorf("prepare worktree: %w", err)
		}
	}

	var add []string
	start := "origin/HEAD"
	switch {
	case c.Branch == "":
		add = []string{"worktree", "add", "--quiet", "--detach", dir, "origin/HEAD"}
	case w.refExists(ctx, clone, "refs/heads/"+c.Branch):
		start = c.Branch
		add = []string{"worktree", "add", "--quiet", dir, c.Branch}
	case w.refExists(ctx, clone, "refs/remotes/origin/"+c.Branch):
		start = "origin/" + c.Branch
		add = []string{"worktree", "add", "--quiet", "--track", "-b", c.Branch, dir, "origin/" + c.Branch}
	default:
		add = []string{"worktree", "add", "--quiet", "-b", c.Branch, dir, "origin/HEAD"}
	}
	if _, err := w.git(ctx, clone, add...); err != nil {
		return "", fmt.Errorf("prepare worktree: %w", err)
	}
	log.Info("worktree created", "branch", c.Branch, "start", start)
	log.Debug("worktree path", "path", dir)
	return dir, nil
}

// Remove deletes the worktree of c and its local branch. A worktree that
// does not exist is not an error.
func (w Workspace) Remove(ctx context.Context, c Checkout) error {
	if err := c.validate(); err != nil {
		return fmt.Errorf("remove worktree: %w", err)
	}
	root, err := w.root()
	if err != nil {
		return fmt.Errorf("remove worktree: %w", err)
	}
	w.Root = root
	clone := w.CloneDir(c)
	if _, err := os.Stat(clone); err != nil {
		return nil
	}
	dir := w.Dir(c)
	if _, err := os.Stat(dir); err == nil {
		if _, err := w.git(ctx, clone, "worktree", "remove", "--force", dir); err != nil {
			return fmt.Errorf("remove worktree: %w", err)
		}
	}
	if _, err := w.git(ctx, clone, "worktree", "prune"); err != nil {
		return fmt.Errorf("remove worktree: %w", err)
	}
	if c.Branch != "" && w.refExists(ctx, clone, "refs/heads/"+c.Branch) {
		if _, err := w.git(ctx, clone, "branch", "--quiet", "-D", c.Branch); err != nil {
			return fmt.Errorf("remove worktree: %w", err)
		}
	}
	w.logger().Info("worktree removed", "repository", c.Owner+"/"+c.Repo, "issue", c.Issue, "role", c.Role)
	return nil
}

// refExists reports whether the full ref name exists in the clone.
func (w Workspace) refExists(ctx context.Context, clone, ref string) bool {
	_, err := w.git(ctx, clone, "rev-parse", "--verify", "--quiet", ref)
	return err == nil
}

// git runs one git command in dir and returns its output. An error names
// the command and has the output of git.
func (w Workspace) git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	// Never wait for a credential prompt. cumin runs without a terminal.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text != "" {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, text)
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return text, nil
}

func (w Workspace) logger() *slog.Logger {
	if w.Logger != nil {
		return w.Logger
	}
	return slog.Default()
}
