package agent

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
)

// The tests cover the first requirement of #7: a worktree for each issue
// and role, from a local clone that cumin keeps up to date, and its removal.

// gitCmd runs git in dir with a fixed identity and no Host configuration.
func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.name=cumin-test", "-c", "user.email=cumin-test@example.com"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// remote is a bare repository that stands in for GitHub.
type remote struct {
	t    *testing.T
	path string // the bare repository
	work string // a clone that the test commits in
}

// newRemote creates a bare repository with one commit on main.
func newRemote(t *testing.T) *remote {
	t.Helper()
	// Keep the Host configuration out of every git call in the test,
	// including the calls of Workspace.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	base := t.TempDir()
	r := &remote{t: t, path: filepath.Join(base, "remote.git"), work: filepath.Join(base, "work")}
	gitCmd(t, base, "init", "--quiet", "--bare", "--initial-branch=main", r.path)
	gitCmd(t, base, "clone", "--quiet", r.path, r.work)
	r.commit("main", "README.md", "first\n")
	return r
}

// commit adds one commit with the file to the branch, and pushes it.
// The branch is created from main when it does not exist.
func (r *remote) commit(branch, name, content string) string {
	r.t.Helper()
	if gitCmd(r.t, r.work, "branch", "--list", branch) == "" {
		gitCmd(r.t, r.work, "checkout", "--quiet", "-b", branch)
	} else {
		gitCmd(r.t, r.work, "checkout", "--quiet", branch)
	}
	if err := os.WriteFile(filepath.Join(r.work, name), []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
	gitCmd(r.t, r.work, "add", name)
	gitCmd(r.t, r.work, "commit", "--quiet", "-m", "add "+name)
	gitCmd(r.t, r.work, "push", "--quiet", "origin", branch)
	return gitCmd(r.t, r.work, "rev-parse", "HEAD")
}

func realPath(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func newWorkspace(t *testing.T, logs *bytes.Buffer) Workspace {
	t.Helper()
	handler := slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelInfo})
	return Workspace{Root: filepath.Join(t.TempDir(), "work"), Logger: slog.New(handler)}
}

func checkout(issue int, role config.Role, branch string) Checkout {
	return Checkout{Owner: "example-org", Repo: "example-repo", Issue: issue, Role: role, Branch: branch}
}

func TestWorktree_PrepareCreatesBranchFromDefaultBranch(t *testing.T) {
	r := newRemote(t)
	var logs bytes.Buffer
	w := newWorkspace(t, &logs)
	c := checkout(12, config.RoleImplementer, "cumin/12-example")

	dir, err := w.Prepare(context.Background(), r.path, c)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if want := filepath.Join(w.Root, "example-org", "example-repo", "12-implementer"); dir != want {
		t.Errorf("dir = %q, want %q", dir, want)
	}
	if _, err := os.Stat(filepath.Join(w.Root, "example-org", "example-repo", "clone")); err != nil {
		t.Errorf("the clone does not exist: %v", err)
	}
	if got := gitCmd(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); got != c.Branch {
		t.Errorf("branch = %q, want %q", got, c.Branch)
	}
	if got, want := gitCmd(t, dir, "rev-parse", "HEAD"), gitCmd(t, r.work, "rev-parse", "main"); got != want {
		t.Errorf("HEAD = %s, want the commit of main %s", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); err != nil {
		t.Errorf("README.md is not checked out: %v", err)
	}
	// Info logs name the issue and the role, and no path of the Host.
	if !strings.Contains(logs.String(), "issue=12") || strings.Contains(logs.String(), w.Root) {
		t.Errorf("info logs must name the issue and must not hold a path:\n%s", logs.String())
	}
}

func TestWorktree_PrepareContinuesRemoteBranch(t *testing.T) {
	r := newRemote(t)
	want := r.commit("cumin/3-continue", "work.txt", "in progress\n")
	w := newWorkspace(t, &bytes.Buffer{})
	c := checkout(3, config.RoleImplementer, "cumin/3-continue")

	dir, err := w.Prepare(context.Background(), r.path, c)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got := gitCmd(t, dir, "rev-parse", "HEAD"); got != want {
		t.Errorf("HEAD = %s, want the commit of origin/%s %s", got, c.Branch, want)
	}
	if got := gitCmd(t, dir, "rev-parse", "--abbrev-ref", "@{upstream}"); got != "origin/"+c.Branch {
		t.Errorf("upstream = %q, want origin/%s", got, c.Branch)
	}
}

func TestWorktree_PrepareDetachedWithoutBranch(t *testing.T) {
	r := newRemote(t)
	w := newWorkspace(t, &bytes.Buffer{})
	c := checkout(7, config.RolePlanner, "")

	dir, err := w.Prepare(context.Background(), r.path, c)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got := gitCmd(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); got != "HEAD" {
		t.Errorf("abbrev-ref HEAD = %q, want a detached HEAD", got)
	}
	if got, want := gitCmd(t, dir, "rev-parse", "HEAD"), gitCmd(t, r.work, "rev-parse", "main"); got != want {
		t.Errorf("HEAD = %s, want the commit of main %s", got, want)
	}
}

func TestWorktree_PrepareFetchesBeforeUse(t *testing.T) {
	r := newRemote(t)
	w := newWorkspace(t, &bytes.Buffer{})
	if _, err := w.Prepare(context.Background(), r.path, checkout(1, config.RoleImplementer, "cumin/1-first")); err != nil {
		t.Fatalf("first Prepare: %v", err)
	}
	// main moves after the clone.
	want := r.commit("main", "second.txt", "second\n")

	dir, err := w.Prepare(context.Background(), r.path, checkout(2, config.RoleImplementer, "cumin/2-second"))
	if err != nil {
		t.Fatalf("second Prepare: %v", err)
	}
	if got := gitCmd(t, dir, "rev-parse", "HEAD"); got != want {
		t.Errorf("HEAD = %s, want the new commit of main %s", got, want)
	}
}

func TestWorktree_PrepareReusesWorktree(t *testing.T) {
	r := newRemote(t)
	var logs bytes.Buffer
	w := newWorkspace(t, &logs)
	c := checkout(5, config.RoleImplementer, "cumin/5-reuse")

	first, err := w.Prepare(context.Background(), r.path, c)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	note := filepath.Join(first, "note.txt")
	if err := os.WriteFile(note, []byte("kept\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.commit("main", "later.txt", "later\n") // must not change the worktree

	second, err := w.Prepare(context.Background(), r.path, c)
	if err != nil {
		t.Fatalf("second Prepare: %v", err)
	}
	if second != first {
		t.Errorf("second path = %q, want %q", second, first)
	}
	if got, err := os.ReadFile(note); err != nil || string(got) != "kept\n" {
		t.Errorf("note.txt = %q, %v; want it unchanged", got, err)
	}
	if _, err := os.Stat(filepath.Join(first, "later.txt")); err == nil {
		t.Error("the reused worktree moved to the new commit")
	}
	if !strings.Contains(logs.String(), "worktree reused") {
		t.Errorf("logs do not say that the worktree was reused:\n%s", logs.String())
	}
}

func TestWorktree_TwoRolesGetTwoWorktrees(t *testing.T) {
	r := newRemote(t)
	w := newWorkspace(t, &bytes.Buffer{})
	impl, err := w.Prepare(context.Background(), r.path, checkout(9, config.RoleImplementer, "cumin/9-two"))
	if err != nil {
		t.Fatalf("Prepare implementer: %v", err)
	}
	ce, err := w.Prepare(context.Background(), r.path, checkout(9, config.RolePlanner, ""))
	if err != nil {
		t.Fatalf("Prepare planner: %v", err)
	}
	if impl == ce {
		t.Errorf("both roles got %q", impl)
	}
	// git prints real paths. The temporary directory may be a symbolic link.
	list := gitCmd(t, w.CloneDir(checkout(9, "", "")), "worktree", "list", "--porcelain")
	if !strings.Contains(list, "worktree "+realPath(t, impl)) || !strings.Contains(list, "worktree "+realPath(t, ce)) {
		t.Errorf("worktree list does not hold both:\n%s", list)
	}
}

// The Reviewer opens the head commit of the pull request, detached, while
// the Implementer worktree of the same issue holds the branch. git refuses
// a branch in two worktrees (git-worktree, --force), not a commit.
func TestWorktree_ReviewerOpensTheHeadCommitNextToTheImplementer(t *testing.T) {
	r := newRemote(t)
	branch := "cumin/5-review"
	first := r.commit(branch, "work.txt", "round 1\n")
	w := newWorkspace(t, &bytes.Buffer{})
	impl, err := w.Prepare(context.Background(), r.path, checkout(5, config.RoleImplementer, branch))
	if err != nil {
		t.Fatalf("Prepare implementer: %v", err)
	}
	reviewer := Checkout{Owner: "example-org", Repo: "example-repo", Issue: 5, Role: config.RoleReviewer, Commit: first}

	dir, err := w.Prepare(context.Background(), r.path, reviewer)
	if err != nil {
		t.Fatalf("Prepare reviewer: %v", err)
	}
	if want := filepath.Join(w.Root, "example-org", "example-repo", "5-reviewer"); dir != want {
		t.Errorf("dir = %q, want %q", dir, want)
	}
	if got := gitCmd(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); got != "HEAD" {
		t.Errorf("abbrev-ref HEAD = %q, want a detached HEAD", got)
	}
	if got := gitCmd(t, dir, "rev-parse", "HEAD"); got != first {
		t.Errorf("HEAD = %s, want the head commit %s", got, first)
	}
	if got := gitCmd(t, impl, "rev-parse", "--abbrev-ref", "HEAD"); got != branch {
		t.Errorf("the Implementer worktree is on %q, want %q", got, branch)
	}

	// The next round: the Implementer pushed a fix. The Reviewer worktree is
	// removed and opened again at the new head, after the fetch of Prepare.
	second := r.commit(branch, "work.txt", "round 2\n")
	if err := w.Remove(context.Background(), reviewer); err != nil {
		t.Fatalf("Remove reviewer: %v", err)
	}
	reviewer.Commit = second
	dir, err = w.Prepare(context.Background(), r.path, reviewer)
	if err != nil {
		t.Fatalf("Prepare reviewer again: %v", err)
	}
	if got := gitCmd(t, dir, "rev-parse", "HEAD"); got != second {
		t.Errorf("HEAD = %s, want the new head commit %s", got, second)
	}
}

func TestWorktree_PrepareAfterManualDeletion(t *testing.T) {
	r := newRemote(t)
	w := newWorkspace(t, &bytes.Buffer{})
	c := checkout(4, config.RoleImplementer, "cumin/4-deleted")
	dir, err := w.Prepare(context.Background(), r.path, c)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	// The local branch still exists. The new worktree checks it out again.
	if again, err := w.Prepare(context.Background(), r.path, c); err != nil || again != dir {
		t.Fatalf("Prepare after deletion = %q, %v; want %q", again, err, dir)
	}
	if got := gitCmd(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); got != c.Branch {
		t.Errorf("branch = %q, want %q", got, c.Branch)
	}
}

// An empty directory that an interrupted prepare left is not a worktree.
// Prepare creates the worktree there. A directory with other content is
// an error, so that nothing is deleted by mistake.
func TestWorktree_PrepareReplacesEmptyDirectory(t *testing.T) {
	r := newRemote(t)
	w := newWorkspace(t, &bytes.Buffer{})
	c := checkout(10, config.RoleImplementer, "cumin/10-empty")
	dir := w.Dir(c)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := w.Prepare(context.Background(), r.path, c); err != nil || got != dir {
		t.Fatalf("Prepare = %q, %v; want %q", got, err, dir)
	}
	if got := gitCmd(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); got != c.Branch {
		t.Errorf("branch = %q, want %q", got, c.Branch)
	}

	other := checkout(11, config.RoleImplementer, "cumin/11-not-empty")
	if err := os.MkdirAll(w.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.Dir(other), "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Prepare(context.Background(), r.path, other); err == nil {
		t.Error("Prepare over a directory with content succeeded, want an error")
	}
}

// The remote may change its default branch after the clone. Prepare
// follows the new default.
func TestWorktree_PrepareFollowsNewDefaultBranch(t *testing.T) {
	r := newRemote(t)
	w := newWorkspace(t, &bytes.Buffer{})
	if _, err := w.Prepare(context.Background(), r.path, checkout(1, config.RolePlanner, "")); err != nil {
		t.Fatalf("first Prepare: %v", err)
	}
	want := r.commit("develop", "develop.txt", "new default\n")
	gitCmd(t, r.path, "symbolic-ref", "HEAD", "refs/heads/develop")

	dir, err := w.Prepare(context.Background(), r.path, checkout(2, config.RolePlanner, ""))
	if err != nil {
		t.Fatalf("second Prepare: %v", err)
	}
	if got := gitCmd(t, dir, "rev-parse", "HEAD"); got != want {
		t.Errorf("HEAD = %s, want the commit of the new default branch %s", got, want)
	}
}

// Two requests for the same repository may prepare at the same time
// (max_issues_in_progress above 1). Only one of them clones.
func TestWorktree_PrepareSerializesTheClone(t *testing.T) {
	r := newRemote(t)
	w := newWorkspace(t, &bytes.Buffer{})
	const n = 4
	errs := make(chan error, n)
	for i := 1; i <= n; i++ {
		go func() {
			_, err := w.Prepare(context.Background(), r.path, checkout(i, config.RoleImplementer, fmt.Sprintf("cumin/%d-parallel", i)))
			errs <- err
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Errorf("Prepare: %v", err)
		}
	}
	list := gitCmd(t, w.CloneDir(checkout(1, "", "")), "worktree", "list", "--porcelain")
	if got := strings.Count(list, "\nworktree "); got != n {
		t.Errorf("worktree list has %d linked worktrees, want %d:\n%s", got, n, list)
	}
}

func TestWorktree_RemoveDeletesWorktreeAndBranch(t *testing.T) {
	r := newRemote(t)
	w := newWorkspace(t, &bytes.Buffer{})
	c := checkout(6, config.RoleImplementer, "cumin/6-remove")
	dir, err := w.Prepare(context.Background(), r.path, c)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dirty.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := w.Remove(context.Background(), c); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the worktree directory still exists: %v", err)
	}
	clone := w.CloneDir(c)
	if list := gitCmd(t, clone, "worktree", "list", "--porcelain"); strings.Contains(list, filepath.Base(dir)) {
		t.Errorf("worktree list still holds the worktree:\n%s", list)
	}
	if got := gitCmd(t, clone, "branch", "--list", c.Branch); got != "" {
		t.Errorf("the local branch still exists: %q", got)
	}
	if err := w.Remove(context.Background(), c); err != nil {
		t.Errorf("second Remove: %v", err)
	}
	// A repository that was never cloned is not an error either.
	other := Checkout{Owner: "example-org", Repo: "other", Issue: 1, Role: config.RoleImplementer}
	if err := w.Remove(context.Background(), other); err != nil {
		t.Errorf("Remove without a clone: %v", err)
	}
}

// A relative work_dir is resolved against the process, not against the
// clone that git runs in.
func TestWorktree_PrepareWithRelativeWorkDir(t *testing.T) {
	r := newRemote(t)
	base := t.TempDir()
	t.Chdir(base)
	w := Workspace{Root: "relative-work", Logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))}
	c := checkout(8, config.RoleImplementer, "cumin/8-relative")

	dir, err := w.Prepare(context.Background(), r.path, c)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	want := filepath.Join(base, "relative-work", "example-org", "example-repo", "8-implementer")
	if realPath(t, dir) != realPath(t, want) {
		t.Errorf("dir = %q, want %q", dir, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); err != nil {
		t.Errorf("the worktree is not at the returned path: %v", err)
	}
	if err := w.Remove(context.Background(), c); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the worktree still exists after Remove: %v", err)
	}
}

// Head is what "wait for the checks" compares with the head commit of the pull request.
func TestWorktree_HeadReadsTheCommitOfTheWorktree(t *testing.T) {
	r := newRemote(t)
	var logs bytes.Buffer
	w := newWorkspace(t, &logs)
	c := checkout(12, config.RoleImplementer, "cumin/12-example")
	dir, err := w.Prepare(context.Background(), r.path, c)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	head, err := w.Head(context.Background(), dir)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if want := gitCmd(t, r.work, "rev-parse", "main"); head != want {
		t.Errorf("Head = %q, want the commit of main %q", head, want)
	}

	// A new commit in the worktree changes what Head returns, which is how
	// "wait for the checks" sees a commit that is not pushed.
	if err := os.WriteFile(filepath.Join(dir, "local.txt"), []byte("change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "add", "local.txt")
	gitCmd(t, dir, "commit", "--quiet", "-m", "a local commit")
	after, err := w.Head(context.Background(), dir)
	if err != nil {
		t.Fatalf("Head after the commit: %v", err)
	}
	if after == head {
		t.Errorf("Head = %q after a commit, want a new commit", after)
	}

	if _, err := w.Head(context.Background(), filepath.Join(w.Root, "no-such-directory")); err == nil {
		t.Error("Head of a directory that does not exist returned no error")
	}
}

func TestWorktree_RejectsInvalidCheckout(t *testing.T) {
	w := newWorkspace(t, &bytes.Buffer{})
	tests := []struct {
		name string
		c    Checkout
	}{
		{"no owner", Checkout{Repo: "r", Issue: 1, Role: config.RoleImplementer}},
		{"owner is a dot", Checkout{Owner: ".", Repo: "r", Issue: 1, Role: config.RoleImplementer}},
		{"owner leaves the work directory", Checkout{Owner: "..", Repo: "r", Issue: 1, Role: config.RoleImplementer}},
		{"repo leaves the work directory", Checkout{Owner: "o", Repo: "..", Issue: 1, Role: config.RoleImplementer}},
		{"owner with slash", Checkout{Owner: "a/b", Repo: "r", Issue: 1, Role: config.RoleImplementer}},
		{"no repo", Checkout{Owner: "o", Issue: 1, Role: config.RoleImplementer}},
		{"issue zero", Checkout{Owner: "o", Repo: "r", Issue: 0, Role: config.RoleImplementer}},
		{"no role", Checkout{Owner: "o", Repo: "r", Issue: 1}},
		{"branch that looks like an option", Checkout{Owner: "o", Repo: "r", Issue: 1, Role: config.RoleImplementer, Branch: "-b"}},
		{"short commit", Checkout{Owner: "o", Repo: "r", Issue: 1, Role: config.RoleReviewer, Commit: "abc1234"}},
		{"commit that looks like an option", Checkout{Owner: "o", Repo: "r", Issue: 1, Role: config.RoleReviewer, Commit: "--" + strings.Repeat("a", 38)}},
		{"commit in upper case", Checkout{Owner: "o", Repo: "r", Issue: 1, Role: config.RoleReviewer, Commit: strings.Repeat("A", 40)}},
		{"branch and commit", Checkout{Owner: "o", Repo: "r", Issue: 1, Role: config.RoleReviewer, Branch: "b", Commit: strings.Repeat("a", 40)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := w.Prepare(context.Background(), "/nowhere", tt.c); err == nil {
				t.Error("Prepare succeeded, want an error")
			}
			if err := w.Remove(context.Background(), tt.c); err == nil {
				t.Error("Remove succeeded, want an error")
			}
		})
	}
	if _, err := w.Prepare(context.Background(), "", checkout(1, config.RoleImplementer, "")); err == nil {
		t.Error("Prepare with an empty remote URL succeeded, want an error")
	}
}

// RemoveIfPushed removes a worktree that holds only what origin has, and
// keeps one with a change that is not committed, a file that git does not
// track, or a commit that is not pushed.
func TestWorktree_RemoveIfPushedKeepsWorkThatIsNotOnTheRemote(t *testing.T) {
	tests := []struct {
		name        string
		change      func(t *testing.T, dir string)
		wantRemoved bool
	}{
		{"a worktree at a pushed commit is removed", func(*testing.T, string) {}, true},
		{"a change that is not committed keeps it", func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("changed\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"a file that git does not track keeps it", func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"a commit that is not pushed keeps it", func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			gitCmd(t, dir, "add", "new.txt")
			gitCmd(t, dir, "commit", "--quiet", "-m", "add new.txt")
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRemote(t)
			r.commit("cumin/3-continue", "work.txt", "in progress\n")
			w := newWorkspace(t, &bytes.Buffer{})
			c := checkout(3, config.RoleImplementer, "cumin/3-continue")
			dir, err := w.Prepare(context.Background(), r.path, c)
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			tt.change(t, dir)

			removed, err := w.RemoveIfPushed(context.Background(), c)
			if err != nil {
				t.Fatalf("RemoveIfPushed: %v", err)
			}
			if removed != tt.wantRemoved {
				t.Errorf("removed = %v, want %v", removed, tt.wantRemoved)
			}
			_, statErr := os.Stat(dir)
			if exists := statErr == nil; exists == tt.wantRemoved {
				t.Errorf("the worktree exists = %v after RemoveIfPushed = %v", exists, removed)
			}
		})
	}
}

// Without a worktree there is nothing to keep, and RemoveIfPushed says so
// without a clone.
func TestWorktree_RemoveIfPushedWithoutAWorktree(t *testing.T) {
	w := newWorkspace(t, &bytes.Buffer{})
	removed, err := w.RemoveIfPushed(context.Background(), checkout(3, config.RoleImplementer, "cumin/3-continue"))
	if err != nil || !removed {
		t.Errorf("RemoveIfPushed = %v, %v; want true, nil", removed, err)
	}
}

// gitTestTimeout is the deadline of the tests with a git command that does
// not end. It leaves the fake git time to start on a busy machine.
const gitTestTimeout = 3 * time.Second

// stallingGit puts a fake git first in PATH. The fake git writes its
// process ID to the returned file and runs stall when its first argument is
// subcommand. Every other command goes to the real git.
func stallingGit(t *testing.T, subcommand, stall string) (pidFile string) {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	pidFile = filepath.Join(dir, "pid")
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = %q ]; then\n  echo $$ > %q\n  %s\nfi\nexec %q \"$@\"\n", subcommand, pidFile, stall, realGit)
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return pidFile
}

// requireProcessGone fails when the process of the pid file still exists.
func requireProcessGone(t *testing.T, pidFile string) {
	t.Helper()
	pid := readPID(t, pidFile)
	// Signal 0 only asks whether the process exists (kill(2)).
	if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
		t.Errorf("the git process %d is not gone: kill(pid, 0) = %v", pid, err)
	}
}

func readPID(t *testing.T, pidFile string) int {
	t.Helper()
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the fake git did not start: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

// A git command that does not end returns an error after the deadline, and
// its process is gone. The second case covers a child process of git that
// keeps the output pipe open after git is gone.
func TestWorktree_GitCommandEndsAfterTheDeadline(t *testing.T) {
	tests := []struct {
		name  string
		stall string
	}{
		{"git itself stalls", "exec sleep 60"},
		{"a child process of git keeps the output pipe open", "sleep 60 &\n  echo $! > \"$0.child\"\n  wait"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pidFile := stallingGit(t, "rev-parse", tt.stall)
			t.Cleanup(func() {
				// The child process outlives git. Do not leave it behind.
				child := filepath.Join(filepath.Dir(pidFile), "git.child")
				if _, err := os.Stat(child); err == nil {
					_ = syscall.Kill(readPID(t, child), syscall.SIGKILL)
				}
			})
			w := newWorkspace(t, &bytes.Buffer{})
			w.GitTimeout = gitTestTimeout

			start := time.Now()
			_, err := w.Head(context.Background(), t.TempDir())
			elapsed := time.Since(start)

			if err == nil {
				t.Fatal("Head returned no error for a git command that does not end")
			}
			for _, want := range []string{"git rev-parse HEAD", "the deadline of 3s passed"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to contain %q", err, want)
				}
			}
			if elapsed > 30*time.Second {
				t.Errorf("Head returned after %s, want the deadline of 3s", elapsed)
			}
			requireProcessGone(t, pidFile)
		})
	}
}

// Prepare returns the error of a stalled git fetch, as of any failed git
// command.
func TestWorktree_PrepareReturnsTheErrorOfAStalledFetch(t *testing.T) {
	r := newRemote(t)
	w := newWorkspace(t, &bytes.Buffer{})
	// The clone exists, so the next Prepare starts with git fetch.
	if _, err := w.Prepare(context.Background(), r.path, checkout(1, config.RoleImplementer, "cumin/1-first")); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	pidFile := stallingGit(t, "fetch", "exec sleep 60")
	w.GitTimeout = gitTestTimeout

	c := checkout(2, config.RoleImplementer, "cumin/2-second")
	_, err := w.Prepare(context.Background(), r.path, c)
	if err == nil {
		t.Fatal("Prepare returned no error for a git fetch that does not end")
	}
	for _, want := range []string{"prepare worktree: git fetch --quiet --prune origin", "the deadline of 3s passed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
	requireProcessGone(t, pidFile)
	if _, statErr := os.Stat(w.Dir(c)); statErr == nil {
		t.Error("Prepare created the worktree after a git fetch that did not end")
	}
}

// silentRemote is a git:// URL of a server on the loopback interface that
// accepts a connection and never answers. A git clone of it does not end.
func silentRemote(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		var conns []net.Conn
		defer func() {
			for _, conn := range conns {
				conn.Close()
			}
		}()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conns = append(conns, conn)
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		<-done
	})
	return "git://" + listener.Addr().String() + "/example-org/example-repo"
}

// A git clone that ends at the deadline leaves no directory of the clone,
// so the next Prepare of the repository clones again and succeeds.
func TestWorktree_PrepareSucceedsAfterACloneThatEndedAtTheDeadline(t *testing.T) {
	r := newRemote(t)
	w := newWorkspace(t, &bytes.Buffer{})
	w.GitTimeout = gitTestTimeout
	c := checkout(1, config.RoleImplementer, "cumin/1-first")

	_, err := w.Prepare(context.Background(), silentRemote(t), c)
	if err == nil {
		t.Fatal("Prepare returned no error for a git clone that does not end")
	}
	for _, want := range []string{"prepare worktree: git clone", "the deadline of 3s passed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
	if _, statErr := os.Stat(w.CloneDir(c)); statErr == nil {
		// Stop here: the next Prepare would fetch from the silent server.
		t.Fatal("the directory of the clone remains after a git clone that ended at the deadline")
	}

	w.GitTimeout = 0
	if _, err := w.Prepare(context.Background(), r.path, c); err != nil {
		t.Errorf("Prepare after a git clone that ended at the deadline: %v", err)
	}
}
