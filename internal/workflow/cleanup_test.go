package workflow_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/agent"
	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/core/state"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
	"github.com/cloveclovedev/cumin-works/internal/workflow"
)

// cleanupBranch is the branch of the Implementer worktree of #10.
const cleanupBranch = "cumin/10-add-the-login-screen"

// newCleanupScene is the scene of the cleanup: the sub-issue #10 of the open
// requirement issue #6 has a worktree of the Implementer on its branch, one
// of the Reviewer, and an entry in the state file. closed says whether #10
// is closed.
func newCleanupScene(t *testing.T, closed bool) (*scene, *workflow.Service) {
	t.Helper()
	sc := newScene(t)
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{
		Number: 10, Parent: 6, Title: subIssueTitle, Closed: closed, ClosedAt: sceneNow.Add(-time.Hour),
		Labels: []string{"cumin/status/checking", "risk/low"},
	})
	service := sc.service()
	service.State = state.Open(filepath.Join(t.TempDir(), "state.json"), nil)
	if err := service.State.Set("example-org/example-repo", 10, state.Issue{SessionID: "s1", CheckFixRequests: 1}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []agent.Checkout{sc.checkout(config.RoleImplementer, cleanupBranch, ""), sc.checkout(config.RoleReviewer, "", sc.remoteHead)} {
		if _, err := service.Workspace.Prepare(context.Background(), sc.remote, c); err != nil {
			t.Fatal(err)
		}
	}
	return sc, service
}

// checkout names a worktree of #10 in the scene.
func (sc *scene) checkout(role config.Role, branch, commit string) agent.Checkout {
	return agent.Checkout{Owner: "example-org", Repo: "example-repo", Issue: 10, Role: role, Branch: branch, Commit: commit}
}

// worktreeExists reports whether the worktree of #10 for the role exists.
func (sc *scene) worktreeExists(role config.Role) bool {
	_, err := os.Stat(filepath.Join(sc.workRoot, "example-org", "example-repo", "10-"+string(role)))
	return err == nil
}

// localBranches lists the branches of the clone of the work directory.
func (sc *scene) localBranches(t *testing.T) string {
	t.Helper()
	return git(t, filepath.Join(sc.workRoot, "example-org", "example-repo", "clone"), "branch", "--list")
}

// A closed sub-issue loses its worktrees of every role, its local branch,
// and its entry in the state file.
func TestCleanup_AClosedSubIssueLosesItsWorktreesBranchAndState(t *testing.T) {
	sc, service := newCleanupScene(t, true)
	sc.pollAndWait(t, service)

	for _, role := range []config.Role{config.RoleImplementer, config.RoleReviewer} {
		if sc.worktreeExists(role) {
			t.Errorf("the worktree of the %s stays", role)
		}
	}
	if branches := sc.localBranches(t); strings.Contains(branches, cleanupBranch) {
		t.Errorf("the local branch stays: %q", branches)
	}
	if got := service.State.Issue("example-org/example-repo", 10); got != (state.Issue{}) {
		t.Errorf("the state entry = %+v, want none", got)
	}
}

// A squash merge deletes the branch and puts none of its commits on main.
// The last push is still the head of the pull request on GitHub, so the
// worktree holds nothing that GitHub lacks.
func TestCleanup_AMergedBranchThatIsGoneStillCountsAsPushed(t *testing.T) {
	sc, service := newCleanupScene(t, true)
	dir := filepath.Join(sc.workRoot, "example-org", "example-repo", "10-implementer")
	if err := os.WriteFile(filepath.Join(dir, "login.txt"), []byte("login\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "login.txt")
	git(t, dir, "commit", "--quiet", "-m", "add the login screen")
	head := git(t, dir, "rev-parse", "HEAD")
	// The push of the Implementer, then the merge that deleted the branch.
	git(t, dir, "push", "--quiet", "origin", "HEAD:refs/pull/21/head")

	sc.pollAndWait(t, service)
	if sc.worktreeExists(config.RoleImplementer) {
		t.Errorf("the worktree at the head of the pull request (%s) stays", head)
	}
}

// A worktree with a change that is not committed, or with a commit that is
// not pushed, stays, and the log names the issue once across polls.
func TestCleanup_AWorktreeWithWorkThatIsNotOnGitHubStays(t *testing.T) {
	tests := []struct {
		name   string
		commit bool
	}{
		{"a change that is not committed", false},
		{"a commit that is not pushed", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc, service := newCleanupScene(t, true)
			dir := filepath.Join(sc.workRoot, "example-org", "example-repo", "10-implementer")
			if err := os.WriteFile(filepath.Join(dir, "login.txt"), []byte("login\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if tt.commit {
				git(t, dir, "add", "login.txt")
				git(t, dir, "commit", "--quiet", "-m", "add the login screen")
			}
			sc.pollAndWait(t, service)
			sc.pollAndWait(t, service)

			if !sc.worktreeExists(config.RoleImplementer) {
				t.Error("the worktree with work was removed")
			}
			if branches := sc.localBranches(t); !strings.Contains(branches, cleanupBranch) {
				t.Errorf("the local branch of the kept worktree is gone: %q", branches)
			}
			if sc.worktreeExists(config.RoleReviewer) {
				t.Error("the worktree of the Reviewer stays")
			}
			warnings := strings.Count(sc.logs.String(), "cleanup: the worktree of a closed issue holds work")
			if warnings != 1 {
				t.Errorf("%d warnings, want 1:\n%s", warnings, sc.logs.String())
			}
			if !strings.Contains(sc.logs.String(), `"issue":10`) {
				t.Error("the log does not name the issue")
			}
		})
	}
}

// An open sub-issue, a repository whose snapshot failed, and an issue that
// the snapshot does not show keep everything.
func TestCleanup_WhatIsNotKnownClosedStays(t *testing.T) {
	t.Run("an open sub-issue", func(t *testing.T) {
		sc, service := newCleanupScene(t, false)
		sc.pollAndWait(t, service)
		assertKept(t, sc, service)
	})
	t.Run("a failed snapshot", func(t *testing.T) {
		sc, service := newCleanupScene(t, true)
		sc.fake.FailTimes("POST", "/graphql", 0, everyTry, 502)
		if err := service.Poll(context.Background()); err == nil {
			t.Fatal("the poll did not fail")
		}
		service.Wait()
		assertKept(t, sc, service)
	})
	t.Run("a sub-issue of a closed requirement issue", func(t *testing.T) {
		sc, service := newCleanupScene(t, true)
		sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 6, Closed: true, Labels: []string{githubtest.RequirementLabel}})
		sc.pollAndWait(t, service)
		assertKept(t, sc, service)
	})
}

func assertKept(t *testing.T, sc *scene, service *workflow.Service) {
	t.Helper()
	for _, role := range []config.Role{config.RoleImplementer, config.RoleReviewer} {
		if !sc.worktreeExists(role) {
			t.Errorf("the worktree of the %s was removed", role)
		}
	}
	if got := service.State.Issue("example-org/example-repo", 10); got == (state.Issue{}) {
		t.Error("the state entry was removed")
	}
}

// An issue whose agent runs now is not cleaned up, even when it is closed.
func TestIssuesToCleanUp(t *testing.T) {
	t.Parallel()
	snapshot := workflow.Snapshot{
		RequirementIssues: []workflow.RequirementIssue{{Number: 6, SubIssues: []workflow.SubIssue{
			{Number: 10, Closed: true},
			{Number: 11, Closed: true},
			{Number: 12},
		}}},
		Running: map[int]bool{11: true},
	}
	got := workflow.IssuesToCleanUp(snapshot)
	if len(got) != 1 || got[0] != 10 {
		t.Errorf("IssuesToCleanUp = %v, want [10]", got)
	}
}

// The work directory of the Planner is removed when its run ends.
func TestCleanup_ThePlannerWorktreeGoesAtTheEndOfItsRun(t *testing.T) {
	sc := newPlanScene(t)
	sc.pollAndWait(t, sc.service())

	if n := sc.agentRuns(t); n != 1 {
		t.Fatalf("%d agent runs, want 1", n)
	}
	dir := filepath.Join(sc.workRoot, "example-org", "example-repo", "6-planner")
	if _, err := os.Stat(dir); err == nil {
		t.Errorf("the worktree of the Planner stays: %s", dir)
	}
}
