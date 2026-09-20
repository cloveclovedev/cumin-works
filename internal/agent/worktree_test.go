package agent

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
	c := checkout(7, config.RoleChiefEngineer, "")

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
	ce, err := w.Prepare(context.Background(), r.path, checkout(9, config.RoleChiefEngineer, ""))
	if err != nil {
		t.Fatalf("Prepare chief engineer: %v", err)
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
