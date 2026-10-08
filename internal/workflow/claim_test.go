package workflow_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/agent"
	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/core/state"
	"github.com/cloveclovedev/cumin-works/internal/core/testenv"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
	"github.com/cloveclovedev/cumin-works/internal/workflow"
)

// The test of a top-level requirement in cumin-core.md: one ready
// implementation issue, two or more polls,
// one request to the Implementer.
func TestReadyIssueIsRequestedOnce(t *testing.T) {
	sc := newScene(t)
	// The pull request that the Implementer opens, so that the run ends on
	// the success path of the verification and the issue is not stopped for the Maintainer.
	// GitHub made no closing link, so cumin adds it.
	sc.addUnlinkedPullRequest(21, sc.remoteHead)
	service := sc.service()
	ctx := context.Background()

	for i := range 3 {
		if err := service.Poll(ctx); err != nil {
			t.Fatalf("poll %d: %v", i+1, err)
		}
	}
	service.Wait()

	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", "cumin/status/checking"}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/checking", got)
	}
	// Two label changes: the claim ("request the implementation") and the end of
	// the run ("wait for the checks").
	if n := sc.fake.CountRequests(http.MethodPut, putLabelsPath); n != 2 {
		t.Errorf("%d label changes, want 2", n)
	}
	// Three polls of two queries each, the read of the login of the Issue Owner
	// before the start, three reads at the end of the run (the issue, the
	// actor of its label, and its comments), the closing link, and one
	// read after it.
	if n := sc.fake.CountRequests(http.MethodPost, "/graphql"); n != 12 {
		t.Errorf("%d GraphQL requests, want 12", n)
	}
	if n := len(sc.fake.Comments(sc.repo, 10)); n != 0 {
		t.Errorf("%d comments on #10, want none on the success path", n)
	}

	// The CLI ran in the worktree of the issue and the role, on the branch
	// that the title gives.
	wantDir := filepath.Join(sc.workRoot, "example-org", "example-repo", "10-implementer")
	if got := strings.TrimSpace(sc.record(t, "agent.cwd")); got != realPath(t, wantDir) {
		t.Errorf("the CLI ran in %q, want %q", got, realPath(t, wantDir))
	}
	if got := branchOf(t, wantDir); got != wantBranch {
		t.Errorf("branch of the worktree = %q, want %q", got, wantBranch)
	}

	// The request text names the kind, the repository, the issue, and the
	// branch. It is the last argument of -p.
	text := promptOf(t, sc.record(t, "agent.args"))
	requireIssueOfTheRun(t, text, 10, "implementation issue")
	for _, want := range []string{"Request: implement", "example-org/example-repo", "#10", wantBranch} {
		if !strings.Contains(text, want) {
			t.Errorf("the request text has no %q:\n%s", want, text)
		}
	}

	logs := sc.logs.String()
	if strings.Contains(logs, githubtest.Token) {
		t.Error("the log holds the token")
	}
	for _, want := range []string{`"msg":"poll"`, `"rate_limit_cost"`, `"msg":"request the implementation: claimed the issue"`,
		`"msg":"request the implementation: requested the work"`, `"branch":"` + wantBranch + `"`,
		`"msg":"the agent run ended"`, `"result":"done"`} {
		if !strings.Contains(logs, want) {
			t.Errorf("the log has no %s", want)
		}
	}
}

// The test of a top-level requirement in cumin-core.md: stop cumin and start
// it again; the same issue is
// not requested twice.
func TestRestartDoesNotRequestTwice(t *testing.T) {
	sc := newScene(t)
	ctx := context.Background()

	first := sc.service()
	if err := first.Poll(ctx); err != nil {
		t.Fatalf("first run: %v", err)
	}
	first.Wait()
	runs := sc.agentRuns(t)
	// A new Service holds nothing from the first one. The facts are on
	// GitHub: the first one decided the end of its run there.
	second := sc.service()
	if err := second.Poll(ctx); err != nil {
		t.Fatalf("second run: %v", err)
	}
	second.Wait()

	if n := sc.agentRuns(t); n != runs {
		t.Errorf("%d agent runs after the restart, want none", n-runs)
	}
}

// The risk criteria of the target repository reaches the agent as the last
// part of its instruction. cumin resolves the three levels for the
// repository and passes the text with the start request; internal/agent
// joins it to the role file, the discipline file, and the writing rules
// (docs/ja/requirements/agents/common.md, the section on the composition
// of the instruction).
func TestTheInstructionOfTheImplementerEndsWithTheRiskCriteriaOfTheRepository(t *testing.T) {
	const criteria = "# Risk criteria of this repository\n\nEvery change is risk/high.\n"
	sc := newScene(t)
	sc.fake.SetFile(sc.repo, ".cumin/risk-criteria.md", githubtest.File{Content: criteria})
	service := sc.service()

	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	service.Wait()

	instruction := systemPromptOf(t, sc.record(t, "agent.args"))
	if !strings.HasSuffix(instruction, criteria) {
		t.Errorf("the instruction does not end with the risk criteria of the repository:\n%s", instruction)
	}
	for _, want := range []string{"# Implementer", "# Software engineering for the Implementer"} {
		if !strings.Contains(instruction, want) {
			t.Errorf("the instruction does not hold %q", want)
		}
	}
}

// TestTheSessionOfARunIsKeptAndAClaimForgetsTheOldOne covers what the
// state file of the Host is for: a request in the same session ("request a check fix", a check
// failed) needs the session of the last run, and a claim ("request the
// implementation") after the
// Maintainer added cumin/status/ready must forget the old session and the count
// of check fixes.
func TestTheSessionOfARunIsKeptAndAClaimForgetsTheOldOne(t *testing.T) {
	sc := newScene(t)
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	path := filepath.Join(t.TempDir(), "state.json")
	store := state.Open(path, nil)
	// What an earlier round left behind for this issue.
	if err := store.Set(sc.repo.Owner+"/"+sc.repo.Name, 10, state.Issue{SessionID: "old-session", CheckFixRequests: 2}); err != nil {
		t.Fatal(err)
	}
	service := sc.service()
	service.State = store

	sc.pollAndWait(t, service)

	// The session of the run that just ended, and the count back at zero.
	const repository = "example-org/example-repo"
	got := store.Issue(repository, 10)
	if got.SessionID != "11111111-2222-4333-8444-555555555555" {
		t.Errorf("session = %q, want the session of the run", got.SessionID)
	}
	if got.CheckFixRequests != 0 {
		t.Errorf("check fix requests = %d, want 0 after a claim", got.CheckFixRequests)
	}
	// A restart of cumin reads the same session back.
	if again := state.Open(path, nil).Issue(repository, 10); again != got {
		t.Errorf("after a restart: %+v, want %+v", again, got)
	}
}

// TestAStateThatCannotBeClearedStopsTheClaim: the state of an issue must
// be cleared before the label changes. A stale entry would make a request in
// the same session ("request a check fix") resume the session from before the Maintainer added
// cumin/status/ready. The issue keeps its label, so the next poll tries
// again.
func TestAStateThatCannotBeClearedStopsTheClaim(t *testing.T) {
	sc := newScene(t)
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	// A directory that cumin cannot write in.
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	store := state.Open(filepath.Join(dir, "state.json"), nil)
	if err := store.Set("example-org/example-repo", 10, state.Issue{SessionID: "old-session"}); err == nil {
		testenv.SkipOrFail(t, "the test user can write in a directory with mode 500")
	}
	service := sc.service()
	service.State = store

	err := service.Poll(context.Background())
	service.Wait()

	if err == nil || !strings.Contains(err.Error(), "clear the state") {
		t.Fatalf("err = %v, want the failed state write", err)
	}
	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelReady) {
		t.Errorf("labels of #10 = %v, want cumin/status/ready to stay", got)
	}
}

// TestAClaimWithoutAStateFileWorks: a Host without the file keeps
// nothing, as a Host that just lost it does.
func TestAClaimWithoutAStateFileWorks(t *testing.T) {
	sc := newScene(t)
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	service := sc.service()
	service.State = nil

	sc.pollAndWait(t, service)

	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelChecking) {
		t.Errorf("labels of #10 = %v, want cumin/status/checking", got)
	}
}

// "request the implementation" (implementer.md): the Maintainer added cumin/status/ready
// again to an issue whose pull request is open. The claim prepares the
// worktree on the branch of that pull request, not on the branch of the
// title, and asks to continue in the same pull request, in a new session.
// A worktree that an earlier round left on another branch is replaced.
func TestAClaimWithAnOpenPullRequestContinuesOnItsBranch(t *testing.T) {
	sc := newScene(t)
	const branch = "cumin/10-an-older-title"
	head := sc.pushBranch(t, branch)
	sc.fake.AddPullRequest(sc.repo, &githubtest.PullRequest{
		Number: 21, HeadCommit: head, HeadBranch: branch, Author: implementerSlug, AuthorIsBot: true, Closes: []int{10},
	})
	service := sc.service()
	// An earlier round left a worktree on the branch of the title, at main.
	earlier, err := service.Workspace.Prepare(context.Background(), sc.remote, agent.Checkout{
		Owner: "example-org", Repo: "example-repo", Issue: 10, Role: config.RoleImplementer, Branch: wantBranch,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := branchOf(t, earlier); got != wantBranch {
		t.Fatalf("the earlier worktree is on %q, want %q", got, wantBranch)
	}

	sc.pollAndWait(t, service)

	dir := filepath.Join(sc.workRoot, "example-org", "example-repo", "10-implementer")
	if got := branchOf(t, dir); got != branch {
		t.Errorf("branch of the worktree = %q, want the branch of the pull request %q", got, branch)
	}
	if got := git(t, dir, "rev-parse", "HEAD"); got != head {
		t.Errorf("head of the worktree = %s, want the head of the pull request %s", got, head)
	}
	args := sc.record(t, "agent.args")
	text := promptOf(t, args)
	requireIssueOfTheRun(t, text, 10, "implementation issue")
	for _, want := range []string{"Request: continue", "Pull request: #21", "Branch: " + branch, "Do not open a new pull request"} {
		if !strings.Contains(text, want) {
			t.Errorf("the request text has no %q:\n%s", want, text)
		}
	}
	if strings.Contains(args, "--resume") {
		t.Error("a claim resumed a session; a claim always starts a new one")
	}
	// The agent made no commit, so the head of the worktree is the head of
	// the pull request, and the verification passes on it.
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", "cumin/status/checking"}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/checking", got)
	}
	logs := sc.logs.String()
	for _, want := range []string{`"kind":"continue"`, `"branch":"` + branch + `"`, `"pull_request":21`} {
		if !strings.Contains(logs, want) {
			t.Errorf("the log has no %s", want)
		}
	}
}

// "request the implementation": a run that cumin stopped leaves its work in
// the worktree, and the
// Maintainer restarts the issue with cumin/status/ready. The continuation keeps
// that worktree, because its work is not on GitHub.
func TestAContinuationKeepsAWorktreeWithWorkThatIsNotPushed(t *testing.T) {
	sc := newScene(t)
	const branch = "cumin/10-an-older-title"
	head := sc.pushBranch(t, branch)
	sc.fake.AddPullRequest(sc.repo, &githubtest.PullRequest{
		Number: 21, HeadCommit: head, HeadBranch: branch, Author: implementerSlug, AuthorIsBot: true, Closes: []int{10},
	})
	service := sc.service()
	earlier, err := service.Workspace.Prepare(context.Background(), sc.remote, agent.Checkout{
		Owner: "example-org", Repo: "example-repo", Issue: 10, Role: config.RoleImplementer, Branch: branch,
	})
	if err != nil {
		t.Fatal(err)
	}
	unpushed := filepath.Join(earlier, "unpushed.txt")
	if err := os.WriteFile(unpushed, []byte("the work of a stopped run\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sc.pollAndWait(t, service)

	if _, err := os.Stat(unpushed); err != nil {
		t.Errorf("the work that is not pushed is gone: %v", err)
	}
	if !strings.Contains(sc.logs.String(), `"msg":"request the implementation: the worktree of an earlier round holds work that is not on GitHub; it is used as it is"`) {
		t.Error("the log does not say that the worktree was kept")
	}
	if text := promptOf(t, sc.record(t, "agent.args")); !strings.Contains(text, "Request: continue") {
		t.Errorf("the request is not a continuation:\n%s", text)
	}
}

// "request a check fix": a worktree of the issue on another branch than the pull request (a
// renamed branch, a newer pull request) is made again on the branch of the
// pull request, because it holds nothing that GitHub lacks.
func TestAWorktreeOnAnotherBranchIsMadeAgainOnThePullRequest(t *testing.T) {
	sc := newScene(t)
	service := sc.service()
	sc.failingCheck(t, service, 0)
	earlier, err := service.Workspace.Prepare(context.Background(), sc.remote, agent.Checkout{
		Owner: "example-org", Repo: "example-repo", Issue: 10, Role: config.RoleImplementer, Branch: "cumin/10-an-older-title",
	})
	if err != nil {
		t.Fatal(err)
	}

	sc.pollAndWait(t, service)

	if got := branchOf(t, earlier); got != wantBranch {
		t.Errorf("branch of the worktree = %q, want the branch of the pull request %q", got, wantBranch)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", workflow.LabelChecking}) {
		t.Errorf("labels of #10 = %v, want the verification to pass on the pull request", got)
	}
}

// "request the implementation": a ready sub-issue with cumin/type/owner-task is never
// claimed, across two polls, while a ready sub-issue without it is.
func TestAnOwnerTaskIsNeverClaimed(t *testing.T) {
	sc := newScene(t)
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 9, Parent: 6, Title: "Change a workflow", Labels: []string{"cumin/type/owner-task", "cumin/status/ready", "risk/high"}})
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	service := sc.service()

	sc.pollAndWait(t, service)
	sc.pollAndWait(t, service)

	if got := sc.fake.Issue(sc.repo, 9).Labels; !slices.Equal(got, []string{"cumin/type/owner-task", "cumin/status/ready", "risk/high"}) {
		t.Errorf("labels of #9 = %v, want them unchanged", got)
	}
	if n := sc.fake.CountRequests(http.MethodPut, "/repos/example-org/example-repo/issues/9/labels"); n != 0 {
		t.Errorf("%d label changes of #9, want none", n)
	}
	// #10 was claimed and ran; #9 started nothing. The second poll also
	// starts the Reviewer of #10 ("request the review"), so the runs are counted by work
	// directory: #9 has none.
	for _, role := range []string{"implementer", "reviewer", "planner"} {
		if _, err := os.Stat(filepath.Join(sc.workRoot, "example-org", "example-repo", "9-"+role)); err == nil {
			t.Errorf("#9 has a %s worktree, want no run for it", role)
		}
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; slices.Contains(got, "cumin/status/ready") {
		t.Errorf("labels of #10 = %v, want it claimed", got)
	}
}
