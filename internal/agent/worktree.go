package agent

// This file prepares the work directory of a request: one clone for each
// repository under the setting work_dir, and one git worktree for each
// issue and role. docs/ja/designs/agent-run.md (the topic on the work
// directory) records the layout and the rules. The git commands come from
// the official git documentation (git-clone, git-fetch, git-remote,
// git-worktree, git-branch).
//
// A git command that talks to the remote (clone, fetch, remote set-head,
// ls-remote) carries the token of the App of the role of the worktree, in
// the environment of that one command only. No other git command gets it.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
)

// cloneDirName is the clone under <work_dir>/<owner>/<repo>. It has no
// checked-out files. It is only the parent of the worktrees.
const cloneDirName = "clone"

const (
	// gitTimeout is how long one git command may take. Without it, a
	// stalled git fetch or git clone holds the start of a run until cumin
	// restarts.
	gitTimeout = 5 * time.Minute
	// gitWaitDelay is how long git has to end after the SIGTERM of the
	// deadline. Then git is killed and the output pipe is closed: a child
	// process of git can still hold it.
	gitWaitDelay = 2 * time.Second
)

// mu serializes Prepare and Remove. Two requests for the same repository
// must not clone or change the same clone at the same time.
var mu sync.Mutex

// Workspace is the work directory of cumin, the setting work_dir. Prepare
// and Remove resolve a relative Root against the working directory of the
// process.
type Workspace struct {
	Root string
	// Logger may be nil. Then the default logger is used.
	Logger *slog.Logger
	// GitTimeout shortens the deadline of each git command, for tests. Zero
	// means gitTimeout.
	GitTimeout time.Duration
	// Token creates the token of the git commands of one prepare or one
	// cleanup that talk to the remote: a token of the App of the role of c,
	// limited to the repository of c and to the read of its contents. cumin
	// run passes the GitToken method of the Service. Nil sends no token, for
	// a test with a remote that needs none.
	Token func(ctx context.Context, c Checkout) (string, error)
}

// Checkout names one worktree: the repository, the issue, and the role.
type Checkout struct {
	Owner string
	Repo  string
	Issue int
	Role  config.Role
	// Branch is the branch of a role that writes. An empty Branch gives a
	// detached checkout, for a role that reads only.
	Branch string
	// Commit is the full SHA that a detached checkout opens: the head
	// commit of a pull request, for the Reviewer. An empty Commit opens the
	// default branch (origin/HEAD), for the Planner. A branch cannot be
	// checked out in two worktrees (git-worktree, --force), so the Reviewer
	// opens the commit next to the Implementer worktree on the branch.
	Commit string
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
	if c.Commit != "" {
		if c.Branch != "" {
			errs = append(errs, errors.New("a checkout has a branch or a commit, not both"))
		}
		if !isFullSHA(c.Commit) {
			errs = append(errs, fmt.Errorf("commit %q must be a full hex SHA", c.Commit))
		}
	}
	return errors.Join(errs...)
}

// isFullSHA reports whether s is a full object name of git: 40 hex digits
// for SHA-1, or 64 for SHA-256, in lower case as GitHub gives them. A
// shorter name could be ambiguous, and a name that is not hex could be an
// option or a ref.
func isFullSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !('0' <= r && r <= '9' || 'a' <= r && r <= 'f') {
			return false
		}
	}
	return true
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
// remoteURL, and is fetched before each new worktree. The clone and the
// fetch carry one token of the App of the role of c, which Prepare creates
// only when it has to talk to the remote. A new worktree with
// a branch starts from origin/<branch> when that exists, and from
// origin/HEAD otherwise. A new worktree without a branch is a detached
// checkout of the commit of c, or of origin/HEAD when c has no commit. An
// existing worktree is returned as it is.
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

	mu.Lock()
	defer mu.Unlock()

	if _, err := os.Stat(dir); err == nil {
		// Reuse only a directory that git knows as a worktree. An empty
		// directory that an interrupted prepare left is created again.
		if w.isWorktree(ctx, dir) {
			log.Info("worktree reused", "branch", c.Branch)
			return dir, nil
		}
		if err := os.Remove(dir); err != nil {
			return "", fmt.Errorf("prepare worktree: %s exists and is not a worktree: %w", dir, err)
		}
	}

	auth, err := w.auth(ctx, c, remoteURL)
	if err != nil {
		return "", fmt.Errorf("prepare worktree: %w", err)
	}
	clone := w.CloneDir(c)
	if _, err := os.Stat(clone); err != nil {
		if err := os.MkdirAll(w.repoDir(c), 0o755); err != nil {
			return "", fmt.Errorf("prepare worktree: %w", err)
		}
		// --no-checkout: the clone holds no files of its own.
		if _, err := w.remoteGit(ctx, w.repoDir(c), auth, "clone", "--quiet", "--no-checkout", "--", remoteURL, cloneDirName); err != nil {
			// git removes the directory of a clone that fails. A git that
			// was killed leaves it, and the next Prepare would take the
			// unfinished directory for the clone.
			if rmErr := os.RemoveAll(clone); rmErr != nil {
				return "", fmt.Errorf("prepare worktree: %w", errors.Join(err, rmErr))
			}
			return "", fmt.Errorf("prepare worktree: %w", err)
		}
		log.Info("clone created")
	}

	// Keep the clone up to date. fetch does not move origin/HEAD, so
	// set-head asks the remote for its default branch (git-remote). Then
	// forget worktrees whose directory is gone.
	steps := [][]string{
		{"fetch", "--quiet", "--prune", "origin"},
		{"remote", "set-head", "origin", "--auto"},
	}
	for _, args := range steps {
		if _, err := w.remoteGit(ctx, clone, auth, args...); err != nil {
			return "", fmt.Errorf("prepare worktree: %w", err)
		}
	}
	if _, err := w.git(ctx, clone, "worktree", "prune"); err != nil {
		return "", fmt.Errorf("prepare worktree: %w", err)
	}

	var add []string
	start := "origin/HEAD"
	switch {
	case c.Commit != "":
		start = c.Commit
		add = []string{"worktree", "add", "--quiet", "--detach", dir, c.Commit}
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

	mu.Lock()
	defer mu.Unlock()

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

// RemoveIfPushed removes the worktree of c as Remove does, but only when
// it holds nothing that origin lacks: no change that is not committed, no
// file that git does not track, and no commit that no branch of origin
// holds. It fetches origin first, so that a commit pushed since the last
// fetch counts as pushed. It reports whether no worktree is left: true
// when it removed the worktree or when there was none, and false when it
// kept a worktree with work that is not on GitHub.
//
// A continuation ("request the implementation") uses it: a worktree of an
// earlier round can be on another branch or behind the pull request, but it
// can also hold the work of a run that cumin stopped before the push.
func (w Workspace) RemoveIfPushed(ctx context.Context, c Checkout) (bool, error) {
	if err := c.validate(); err != nil {
		return false, fmt.Errorf("remove worktree: %w", err)
	}
	root, err := w.root()
	if err != nil {
		return false, fmt.Errorf("remove worktree: %w", err)
	}
	w.Root = root
	pushed, err := w.onlyPushedWork(ctx, c)
	if err != nil || !pushed {
		return false, err
	}
	if err := w.Remove(ctx, c); err != nil {
		return false, err
	}
	return true, nil
}

// RemoveClosed removes the worktree of c after its issue closed, with the
// local branch that the worktree holds, when the worktree holds nothing
// that GitHub lacks. It reports whether it kept a worktree with work that
// is not on GitHub. A missing worktree is not an error.
//
// A merge deletes the branch of the pull request, and a squash merge puts
// none of its commits on the default branch. So a commit also counts as on
// GitHub when it is the head of a pull request: GitHub keeps the ref
// refs/pull/<number>/head of every pull request (official: "Checking out
// pull requests locally"). A worktree behind the last push of its pull
// request is kept, which is the safe side.
func (w Workspace) RemoveClosed(ctx context.Context, c Checkout) (bool, error) {
	if err := c.validate(); err != nil {
		return false, fmt.Errorf("remove worktree: %w", err)
	}
	root, err := w.root()
	if err != nil {
		return false, fmt.Errorf("remove worktree: %w", err)
	}
	w.Root = root
	dir := w.Dir(c)
	if _, err := os.Stat(dir); err != nil {
		return false, nil
	}
	pushed, branch, err := w.closedWork(ctx, c)
	if err != nil {
		return false, err
	}
	if !pushed {
		return true, nil
	}
	c.Branch = branch
	return false, w.Remove(ctx, c)
}

// closedWork reports whether the worktree of a closed issue holds only
// work that GitHub has, and the branch that it holds ("" when detached).
// It holds the lock of Prepare and Remove while it reads the worktree.
func (w Workspace) closedWork(ctx context.Context, c Checkout) (bool, string, error) {
	mu.Lock()
	defer mu.Unlock()
	dir := w.Dir(c)
	if !w.isWorktree(ctx, dir) {
		// Nothing of value: a directory that a failed Prepare left.
		return true, "", nil
	}
	branch, err := w.git(ctx, dir, "branch", "--show-current")
	if err != nil {
		return false, "", fmt.Errorf("check the worktree: %w", err)
	}
	auth, err := w.cloneAuth(ctx, c)
	if err != nil {
		return false, "", fmt.Errorf("check the worktree: %w", err)
	}
	if _, err := w.remoteGit(ctx, w.CloneDir(c), auth, "fetch", "--quiet", "--prune", "origin"); err != nil {
		return false, "", fmt.Errorf("check the worktree: %w", err)
	}
	status, err := w.git(ctx, dir, "status", "--porcelain")
	if err != nil {
		return false, "", fmt.Errorf("check the worktree: %w", err)
	}
	if status != "" {
		return false, branch, nil
	}
	unpushed, err := w.git(ctx, dir, "rev-list", "--max-count=1", "HEAD", "--not", "--remotes=origin")
	if err != nil {
		return false, "", fmt.Errorf("check the worktree: %w", err)
	}
	if unpushed == "" {
		return true, branch, nil
	}
	head, err := w.git(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return false, "", fmt.Errorf("check the worktree: %w", err)
	}
	heads, err := w.remoteGit(ctx, w.CloneDir(c), auth, "ls-remote", "origin", "refs/pull/*/head")
	if err != nil {
		return false, "", fmt.Errorf("check the worktree: %w", err)
	}
	for _, line := range strings.Split(heads, "\n") {
		sha, _, _ := strings.Cut(line, "\t")
		if sha == head {
			return true, branch, nil
		}
	}
	return false, branch, nil
}

// onlyPushedWork reports whether the worktree of c is missing, or holds
// only work that origin has. It holds the lock of Prepare and Remove while
// it reads the worktree.
func (w Workspace) onlyPushedWork(ctx context.Context, c Checkout) (bool, error) {
	mu.Lock()
	defer mu.Unlock()
	dir := w.Dir(c)
	if _, err := os.Stat(dir); err != nil || !w.isWorktree(ctx, dir) {
		// Nothing of value: Prepare creates the worktree again.
		return true, nil
	}
	auth, err := w.cloneAuth(ctx, c)
	if err != nil {
		return false, fmt.Errorf("check the worktree: %w", err)
	}
	if _, err := w.remoteGit(ctx, w.CloneDir(c), auth, "fetch", "--quiet", "--prune", "origin"); err != nil {
		return false, fmt.Errorf("check the worktree: %w", err)
	}
	status, err := w.git(ctx, dir, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("check the worktree: %w", err)
	}
	unpushed, err := w.git(ctx, dir, "rev-list", "--max-count=1", "HEAD", "--not", "--remotes=origin")
	if err != nil {
		return false, fmt.Errorf("check the worktree: %w", err)
	}
	return status == "" && unpushed == "", nil
}

// Head returns the full SHA of the head commit of the worktree dir. "wait
// for the checks" compares it with the head commit of the pull request, to
// see that the last commit of the agent is pushed.
func (w Workspace) Head(ctx context.Context, dir string) (string, error) {
	out, err := w.git(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("read the head commit of %s: %w", dir, err)
	}
	return out, nil
}

// isWorktree reports whether dir is a working tree that git knows.
func (w Workspace) isWorktree(ctx context.Context, dir string) bool {
	out, err := w.git(ctx, dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && out == "true"
}

// refExists reports whether the full ref name exists in the clone.
func (w Workspace) refExists(ctx context.Context, clone, ref string) bool {
	_, err := w.git(ctx, clone, "rev-parse", "--verify", "--quiet", ref)
	return err == nil
}

// gitAuth is the token of the git commands of one prepare or one cleanup
// that talk to the remote, as git reads it from its environment. The zero
// value sends no token.
type gitAuth struct {
	env []string
	// secrets are the forms of the token. They never leave run.
	secrets []string
}

// auth creates the token of c for the remote at remoteURL. It sends no
// token when the Workspace has no Token, and when the remote is not an
// HTTP address: only HTTP has the header that carries the token.
func (w Workspace) auth(ctx context.Context, c Checkout, remoteURL string) (gitAuth, error) {
	key := extraHeaderKey(remoteURL)
	if w.Token == nil || key == "" {
		return gitAuth{}, nil
	}
	token, err := w.Token(ctx, c)
	if err != nil {
		return gitAuth{}, fmt.Errorf("create the token of git: %w", err)
	}
	if token == "" {
		return gitAuth{}, errors.New("create the token of git: the token is empty")
	}
	cred := Credentials{Token: token}
	return gitAuth{
		// The same three variables as the environment of an agent (env.go;
		// git-config(1): GIT_CONFIG_COUNT).
		env: []string{
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=" + key,
			"GIT_CONFIG_VALUE_0=Authorization: Basic " + cred.basicHeader(),
		},
		secrets: cred.secrets(),
	}, nil
}

// cloneAuth creates the token of c for the remote of its clone. Remove and
// its checks do not get the address of the remote, so it is read from the
// clone.
func (w Workspace) cloneAuth(ctx context.Context, c Checkout) (gitAuth, error) {
	if w.Token == nil {
		return gitAuth{}, nil
	}
	remoteURL, err := w.git(ctx, w.CloneDir(c), "remote", "get-url", "origin")
	if err != nil {
		return gitAuth{}, err
	}
	return w.auth(ctx, c, remoteURL)
}

// extraHeaderKey is the name of the git setting that adds a header to the
// requests to the host of remoteURL only: for GitHub,
// "http.https://github.com/.extraheader" (git-config(1): http.<url>.*,
// http.extraHeader). It is empty when remoteURL is not an HTTP address.
func extraHeaderKey(remoteURL string) string {
	u, err := url.Parse(remoteURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	return "http." + u.Scheme + "://" + u.Host + "/.extraheader"
}

// git runs one git command in dir and returns its output. An error names
// the command and has the output of git. The command ends after the
// deadline of the Workspace: then the error says that the deadline passed.
func (w Workspace) git(ctx context.Context, dir string, args ...string) (string, error) {
	return w.run(ctx, dir, gitAuth{}, args...)
}

// remoteGit runs one git command that talks to the remote, as git does.
// git reads no configuration file of the Host user or of the system, so no
// credential helper answers, and it has no SSH and no program that asks
// for a password (git(1): GIT_CONFIG_GLOBAL, GIT_CONFIG_NOSYSTEM,
// GIT_SSH_COMMAND, GIT_ASKPASS). GIT_CONFIG_COUNT=0 drops a setting that
// the environment of cumin holds. The token of auth is the only credential.
func (w Workspace) remoteGit(ctx context.Context, dir string, auth gitAuth, args ...string) (string, error) {
	auth.env = append([]string{
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_SSH_COMMAND=false",
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
		"GIT_CONFIG_COUNT=0",
	}, auth.env...)
	return w.run(ctx, dir, auth, args...)
}

// run runs one git command with the environment of auth added to the
// environment of cumin. The output of git in an error holds no token.
func (w Workspace) run(ctx context.Context, dir string, auth gitAuth, args ...string) (string, error) {
	timeout := w.GitTimeout
	if timeout <= 0 {
		timeout = gitTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "git", args...)
	cmd.Dir = dir
	// Never wait for a credential prompt. cumin runs without a terminal.
	cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0"), auth.env...)
	// When the context is done, Cancel sends SIGTERM, so that git removes
	// its lock files and the directory of an unfinished clone. After
	// WaitDelay, os/exec kills git and closes the output pipe, which a
	// child process of git can keep open (os/exec: Cmd.Cancel,
	// Cmd.WaitDelay).
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = gitWaitDelay
	out, err := cmd.CombinedOutput()
	text := redact(strings.TrimSpace(string(out)), auth.secrets)
	if err != nil {
		// The deadline of this command passed, not the context of the caller.
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			if text != "" {
				return "", fmt.Errorf("git %s: the deadline of %s passed: %w: %s", strings.Join(args, " "), timeout, err, text)
			}
			return "", fmt.Errorf("git %s: the deadline of %s passed: %w", strings.Join(args, " "), timeout, err)
		}
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
