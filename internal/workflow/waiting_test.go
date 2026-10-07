package workflow_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
	"github.com/cloveclovedev/cumin-works/internal/workflow"
)

// q4Messages returns the notifications of Q4.
func (sc *scene) q4Messages() []string {
	var q4 []string
	for _, m := range sc.webhook.messagesSent() {
		if strings.HasPrefix(m, "cumin: tell that cumin waits: ") {
			q4 = append(q4, m)
		}
	}
	return q4
}

// Core-9 and Q4: with no ready issue and no agent running, the Owner hears
// once across polls. After cumin does something, a new time with nothing to
// do notifies again.
func TestCore09_NothingToDoNotifiesOnceUntilCuminActs(t *testing.T) {
	sc := newScene(t)
	if err := sc.fake.SetLabels(sc.repo, 10, []string{"risk/low"}); err != nil {
		t.Fatal(err)
	}
	service := sc.service()
	for range 3 {
		sc.pollAndWait(t, service)
	}
	q4 := sc.q4Messages()
	if len(q4) != 1 || !strings.Contains(q4[0], "No issue can go on") {
		t.Fatalf("Q4 notifications = %q, want one", q4)
	}
	if !strings.Contains(sc.logs.String(), `"msg":"Q4: no issue can go on and no agent runs; the Owner is told once"`) {
		t.Error("the log has no Q4 line")
	}

	// The Owner adds cumin/status/ready: cumin claims the issue and runs
	// the Implementer (it acts). With no pull request, I2 hands the issue
	// back to the Owner, and nothing is left to do again.
	if err := sc.fake.SetLabels(sc.repo, 10, []string{"cumin/status/ready", "risk/low"}); err != nil {
		t.Fatal(err)
	}
	sc.pollAndWait(t, service)
	sc.pollAndWait(t, service)
	sc.pollAndWait(t, service)
	if got := len(sc.q4Messages()); got != 2 {
		t.Errorf("%d Q4 notifications, want a second one after cumin acted", got)
	}
}

// Q4: a ready issue that only the quota stops is work left. Q1 names the
// cause; Q4 does not fire (the Owner's decision on #234).
func TestQ4_AQuotaStopIsNotWaiting(t *testing.T) {
	sc := newScene(t)
	sc.setQuota(t, 0.90, sceneNow.Add(2*time.Hour), 0.10, sceneNow.Add(time.Hour))
	service := sc.service()
	for range 3 {
		sc.pollAndWait(t, service)
	}
	if got := len(sc.q1Messages()); got != 1 {
		t.Errorf("%d Q1 notifications, want 1", got)
	}
	if got := sc.q4Messages(); len(got) != 0 {
		t.Errorf("Q4 notifications = %q, want none", got)
	}
}

// Q4: when the quota resumes (Q3) and the work is done, cumin has nothing
// to do and says so.
func TestQ4_AResumeWithNoWorkLeftNotifies(t *testing.T) {
	sc := newScene(t)
	reset := sceneNow.Add(2 * time.Hour)
	sc.setQuota(t, 0.90, reset, 0.10, sceneNow.Add(time.Hour))
	service := sc.service()
	withState(t, service)
	sc.pollAndWait(t, service)

	sc.clock.Set(reset)
	sc.setQuota(t, 0.05, reset.Add(5*time.Hour), 0.10, reset.Add(time.Hour))
	sc.pollAndWait(t, service) // the claim and the run; I2 hands the issue back
	sc.pollAndWait(t, service) // nothing to do
	if n := sc.agentRuns(t); n != 2 {
		t.Fatalf("%d agent runs, want 2 (the request and the second request)", n)
	}
	if got := len(sc.q4Messages()); got != 1 {
		t.Errorf("%d Q4 notifications, want 1", got)
	}
}

// Q4: while an agent runs, cumin is not waiting.
func TestQ4_ARunningAgentIsNotWaiting(t *testing.T) {
	sc := newScene(t, cliOptions{sleeps: true})
	service := sc.service()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); service.Wait() })
	if err := service.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	// The run of #10 goes on; the next poll decides nothing.
	if err := service.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if got := sc.q4Messages(); len(got) != 0 {
		t.Errorf("Q4 notifications = %q, want none while an agent runs", got)
	}
}

// Q4: a poll that failed sends no Q4, since a repository it did not read
// may hold work.
func TestQ4_AFailedPollSendsNothing(t *testing.T) {
	sc := newScene(t)
	if err := sc.fake.SetLabels(sc.repo, 10, []string{"risk/low"}); err != nil {
		t.Fatal(err)
	}
	service := sc.service()
	sc.fake.FailTimes(http.MethodPost, "/graphql", 0, everyTry, http.StatusBadGateway)
	_ = service.Poll(t.Context())
	if got := sc.q4Messages(); len(got) != 0 {
		t.Errorf("Q4 after a failed poll = %q, want none", got)
	}
	// The first poll that succeeds moves #6 back to the Owner (R6); the
	// next one finds nothing to do.
	sc.pollAndWait(t, service)
	sc.pollAndWait(t, service)
	if got := len(sc.q4Messages()); got != 1 {
		t.Errorf("%d Q4 after polls that succeeded, want 1", got)
	}
}

// Q4: an action that a poll decided ends the silence, even when that poll
// failed. A later time with nothing to do notifies again.
func TestQ4_AnActionInAFailedPollEndsTheSilence(t *testing.T) {
	sc := newScene(t)
	if err := sc.fake.SetLabels(sc.repo, 10, []string{"risk/low"}); err != nil {
		t.Fatal(err)
	}
	service := sc.service()
	// R6 moves #6 back to the Owner; then nothing is left to do.
	sc.pollAndWait(t, service)
	sc.pollAndWait(t, service)
	if got := len(sc.q4Messages()); got != 1 {
		t.Fatalf("%d Q4 notifications, want 1", got)
	}

	// The Owner makes #10 ready; the claim is decided, and its label
	// change fails, so the poll fails.
	if err := sc.fake.SetLabels(sc.repo, 10, []string{"cumin/status/ready", "risk/low"}); err != nil {
		t.Fatal(err)
	}
	sc.fake.FailNext(http.MethodPut, putLabelsPath, http.StatusBadGateway)
	_ = service.Poll(t.Context())
	service.Wait()
	// The Owner takes the label away again. R3 moved #6 to implementing,
	// so R6 moves it back first; then nothing is left to do.
	if err := sc.fake.SetLabels(sc.repo, 10, []string{"risk/low"}); err != nil {
		t.Fatal(err)
	}
	sc.pollAndWait(t, service)
	sc.pollAndWait(t, service)
	if got := len(sc.q4Messages()); got != 2 {
		t.Errorf("%d Q4 notifications, want 2", got)
	}
}

// Q4: an issue that waits for its required checks is a step that cumin
// takes by itself. No agent runs and the poll decides nothing, and still
// the Owner hears nothing.
func TestQ4_AnIssueAwaitingChecksIsNotWaiting(t *testing.T) {
	sc := newScene(t)
	sc.awaitingChecks(t, []string{"ci"}, []githubtest.Check{{Name: "ci", Status: "IN_PROGRESS"}})
	service := sc.service()
	for range 3 {
		sc.pollAndWait(t, service)
	}
	if got := sc.q4Messages(); len(got) != 0 {
		t.Errorf("Q4 notifications = %q, want none while the checks run", got)
	}
	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none", n)
	}
}

// Q4: a ready issue that waits only for room under the limit starts when
// the room is free, so cumin is not waiting. #11 fills the one place and
// has no agent.
func TestQ4_AReadyIssueWaitingForRoomIsNotWaiting(t *testing.T) {
	sc := newScene(t)
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 11, Parent: 6, Title: "Add the logout screen",
		Labels: []string{"cumin/status/implementing", "risk/low"}})
	service := sc.service()
	for range 3 {
		sc.pollAndWait(t, service)
	}
	if got := sc.q4Messages(); len(got) != 0 {
		t.Errorf("Q4 notifications = %q, want none while a ready issue waits for room", got)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, "cumin/status/ready") {
		t.Errorf("labels of #10 = %v, want cumin/status/ready: the limit is 1", got)
	}
}

// Q4: an issue in cumin/status/merging or cumin/status/accepting with no
// agent is an issue that cumin moves on at a later poll. The merge is
// refused at every poll, and the quota stops the second request of the
// acceptance check, so both issues keep their label; the Owner hears no
// waiting notification.
func TestQ4_AnIssueInMergingOrAcceptingWithNoAgentIsNotWaiting(t *testing.T) {
	t.Run("merging", func(t *testing.T) {
		sc := mergingScene(t, "risk/low")
		sc.fake.RefuseMergesForBaseBranch(3)
		sc.pollTimes(t, sc.service(), 3)
		if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelMerging) {
			t.Fatalf("labels of #10 = %v, want cumin/status/merging: the test would pass for a wrong reason", got)
		}
		if got := sc.q4Messages(); len(got) != 0 {
			t.Errorf("Q4 notifications = %q, want none while an issue is in merging", got)
		}
	})
	t.Run("accepting", func(t *testing.T) {
		sc, _ := acceptingScene(t, cliOptions{fixture: "planner-done.jsonl"})
		sc.setQuota(t, 0.90, sceneNow.Add(2*time.Hour), 0.10, sceneNow.Add(time.Hour))
		sc.pollTimes(t, sc.service(), 3)
		if got := requirementLabels(t, sc); !slices.Contains(got, workflow.LabelAccepting) {
			t.Fatalf("labels of #6 = %v, want cumin/status/accepting: the test would pass for a wrong reason", got)
		}
		if got := sc.q4Messages(); len(got) != 0 {
			t.Errorf("Q4 notifications = %q, want none while an issue is in accepting", got)
		}
	})
}

// Q4: when every issue waits for the Owner, the Owner hears once across
// polls. A ready issue behind an open blocked-by issue and an issue that
// is stopped for a decision both wait for the Owner.
func TestQ4_OnlyWaitsForTheOwnerNotifyOnce(t *testing.T) {
	sc := newScene(t)
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle,
		Labels: []string{"cumin/status/ready", "risk/low"}, BlockedBy: []int{11}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 11, Parent: 6, Title: "Add the logout screen",
		Labels: []string{"cumin/status/awaiting-plan-review", "risk/low"}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 12, Parent: 6, Title: "Add the profile screen",
		Labels: []string{"cumin/status/awaiting-decision", "risk/low"}})
	service := sc.service()
	for range 3 {
		sc.pollAndWait(t, service)
	}
	if got := len(sc.q4Messages()); got != 1 {
		t.Errorf("%d Q4 notifications, want 1", got)
	}
}

// Q4: an issue that cumin moves on in one target repository keeps the
// notification back for every repository.
func TestQ4_AnIssueInOneRepositoryStopsTheNotificationForAll(t *testing.T) {
	sc := newScene(t)
	sc.awaitingChecks(t, []string{"ci"}, []githubtest.Check{{Name: "ci", Status: "IN_PROGRESS"}})
	other := sc.fake.AddRepository("example-org", "other-repo")
	sc.fake.AddIssue(other, &githubtest.Issue{Number: 1, Labels: []string{githubtest.RequirementLabel}})
	service := sc.service()
	// The repository with nothing to do comes last, so its result must not
	// replace the one of the first repository.
	service.Targets = append(service.Targets, target(sc, "other-repo"))
	for range 3 {
		sc.pollAndWait(t, service)
	}
	if got := sc.q4Messages(); len(got) != 0 {
		t.Errorf("Q4 notifications = %q, want none", got)
	}

	// The checks pass and the Reviewer approves (cumin acts). Then only
	// the Owner can move things on, and the Owner hears once.
	sc.fake.AddPullRequest(sc.repo, &githubtest.PullRequest{
		Number: 21, HeadCommit: sc.remoteHead, HeadBranch: "cumin/10-add-the-login-screen",
		Author: implementerSlug, AuthorIsBot: true, Closes: []int{10},
		Checks: []githubtest.Check{{Name: "ci", Conclusion: "SUCCESS"}},
	})
	for range 4 {
		sc.pollAndWait(t, service)
	}
	if got := len(sc.q4Messages()); got != 1 {
		t.Errorf("%d Q4 notifications, want 1 after the issue went on to the Owner; labels of #10 = %v",
			got, sc.fake.Issue(sc.repo, 10).Labels)
	}
}

// Core-34 (cumin-core.md): at a limit, with no running agent, an issue
// that waits for a start of an agent gives no waiting notification, in
// whatever state it waits. The quota notification names the cause once.
func TestCore34_AnIssueThatWaitsOnlyForTheQuotaIsNotWaiting(t *testing.T) {
	scenes := map[string]func(t *testing.T) (*scene, *workflow.Service){
		"the review under checking": func(t *testing.T) (*scene, *workflow.Service) {
			sc := approved(t, "risk/low")
			sc.atAQuotaLimit(t)
			return sc, sc.service()
		},
		"a change request of the Owner under awaiting-merge-decision": func(t *testing.T) (*scene, *workflow.Service) {
			sc := awaitingOwner(t, cliOptions{})
			sc.review(implementerSlug, true, "APPROVED", sc.remoteHead, 30)
			sc.review(theOwner, false, "CHANGES_REQUESTED", sc.remoteHead, 5)
			sc.repo.Issues[10].LabelEvents = append([]githubtest.LabelEvent{readyBy(theOwner, 60)}, sc.repo.Issues[10].LabelEvents...)
			sc.atAQuotaLimit(t)
			return sc, sc.serviceWithSession(t)
		},
		"a ready issue": func(t *testing.T) (*scene, *workflow.Service) {
			sc := newScene(t)
			sc.atAQuotaLimit(t)
			return sc, sc.service()
		},
	}
	for name, build := range scenes {
		t.Run(name, func(t *testing.T) {
			sc, service := build(t)
			labels := slices.Clone(sc.fake.Issue(sc.repo, 10).Labels)
			sc.pollTimes(t, service, 3)
			if n := sc.agentRuns(t); n != 0 {
				t.Fatalf("%d agent runs at the limit, want 0: the test would pass for a wrong reason", n)
			}
			if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, labels) {
				t.Fatalf("labels of #10 at the limit = %v, want %v", got, labels)
			}
			if got := sc.q4Messages(); len(got) != 0 {
				t.Errorf("Q4 notifications = %q, want none while an issue waits only for the quota", got)
			}
			if got := sc.q1Messages(); len(got) != 1 {
				t.Errorf("notifications of the stop = %q, want one over three polls", got)
			}
		})
	}
}

// Q4: a start that waited only for the quota is forgotten when the wait
// ends. The Owner takes the request for changes back while cumin is at the
// limit; then only the Owner can move the issue on, and the Owner hears
// once.
func TestQ4_AQuotaWaitThatEndedNotifiesOnce(t *testing.T) {
	sc := awaitingOwner(t, cliOptions{})
	sc.review(implementerSlug, true, "APPROVED", sc.remoteHead, 30)
	sc.review(theOwner, false, "CHANGES_REQUESTED", sc.remoteHead, 5)
	sc.repo.Issues[10].LabelEvents = append([]githubtest.LabelEvent{readyBy(theOwner, 60)}, sc.repo.Issues[10].LabelEvents...)
	sc.atAQuotaLimit(t)
	service := sc.serviceWithSession(t)
	sc.pollTimes(t, service, 2)
	if got := sc.q4Messages(); len(got) != 0 {
		t.Fatalf("Q4 notifications = %q, want none while the request waits for the quota", got)
	}

	sc.repo.PullRequests[21].Reviews = sc.repo.PullRequests[21].Reviews[:1]
	sc.pollTimes(t, service, 3)
	if got := len(sc.q4Messages()); got != 1 {
		t.Errorf("%d Q4 notifications, want 1 after the wait for the quota ended", got)
	}
	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want 0", n)
	}
}
