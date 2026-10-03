package workflow_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/state"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
)

const (
	ownerLoginLine   = "\n- Login of the Owner: " + theOwner + "\n"
	noOwnerLoginLine = "\n- Login of the Owner: there is no Owner login\n"
)

// readyBy is one event that added cumin/status/ready, by a person.
func readyBy(login string, minutesAgo int) githubtest.LabelEvent {
	return githubtest.LabelEvent{Label: "cumin/status/ready", At: sceneNow.Add(-time.Duration(minutesAgo) * time.Minute), Actor: login, ActorType: "User"}
}

// The facts of the start request (agents/common.md): the prompt of the
// Implementer names the account that added the newest cumin/status/ready
// to the issue of the run, when that account is the Owner. An older event
// by another Owner does not win.
func TestOwnerLogin_TheImplementerReceivesTheActorOfTheNewestReady(t *testing.T) {
	sc := newScene(t)
	sc.addUnlinkedPullRequest(21, sc.remoteHead)
	sc.repo.Issues[10].LabelEvents = []githubtest.LabelEvent{readyBy("an-earlier-owner", 60), readyBy(theOwner, 5)}
	sc.fake.SetPermission("an-earlier-owner", "admin", "User")
	sc.fake.SetPermission(theOwner, "write", "User")

	sc.pollAndWait(t, sc.service())

	text := promptOf(t, sc.record(t, "agent.args"))
	if !strings.Contains(text, "- Issue of the run: #10 (implementation issue)"+ownerLoginLine) {
		t.Errorf("the prompt does not name the Owner %s after the issue of the run:\n%s", theOwner, text)
	}
	if strings.Contains(text, "an-earlier-owner") {
		t.Errorf("the prompt names the actor of an older event:\n%s", text)
	}
}

// The prompt of the Planner names the Owner of the requirement issue. A
// requirement issue with no cumin/status/ready event takes the newest such
// event of its sub-issues.
func TestOwnerLogin_ThePlannerReceivesTheOwnerOfTheRequirementIssueOrOfItsSubIssues(t *testing.T) {
	tests := []struct {
		name        string
		requirement []githubtest.LabelEvent
		subIssue    []githubtest.LabelEvent
	}{
		{name: "the event of the requirement issue", requirement: []githubtest.LabelEvent{readyBy(theOwner, 5)},
			subIssue: []githubtest.LabelEvent{readyBy("another-owner", 1)}},
		{name: "no event on the requirement issue", subIssue: []githubtest.LabelEvent{readyBy(theOwner, 5)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := newPlanScene(t)
			sc.repo.Issues[6].LabelEvents = tt.requirement
			sc.repo.Issues[10].LabelEvents = tt.subIssue
			sc.fake.SetPermission(theOwner, "admin", "User")
			sc.fake.SetPermission("another-owner", "admin", "User")

			sc.pollAndWait(t, sc.service())

			text := promptOf(t, sc.record(t, "agent.args"))
			if !strings.Contains(text, "- Issue of the run: #6 (requirement issue)"+ownerLoginLine) {
				t.Errorf("the prompt does not name the Owner %s after the issue of the run:\n%s", theOwner, text)
			}
		})
	}
}

// The prompt of the Reviewer names the Owner of the implementation issue.
func TestOwnerLogin_TheReviewerReceivesTheOwner(t *testing.T) {
	sc := newScene(t, cliOptions{reviews: []string{"APPROVE"}})
	service := sc.service()
	sc.reviewing(t, service, state.Issue{})
	sc.repo.Issues[10].LabelEvents = []githubtest.LabelEvent{readyBy(theOwner, 30)}
	sc.fake.SetPermission(theOwner, "admin", "User")

	sc.pollAndWait(t, service)

	text := promptOf(t, sc.record(t, "agent.args"))
	if !strings.Contains(text, "- Issue of the run: #10 (implementation issue)"+ownerLoginLine) {
		t.Errorf("the prompt does not name the Owner %s after the issue of the run:\n%s", theOwner, text)
	}
}

// Without an event, and with an actor that is not the Owner (cumin-core.md:
// a person with write permission or higher), the prompt says that there is
// no Owner login. The permission of a GitHub App is not read.
func TestOwnerLogin_WithoutAnOwnerThePromptSaysThatThereIsNoOwnerLogin(t *testing.T) {
	tests := []struct {
		name            string
		events          []githubtest.LabelEvent
		permissionReads int
	}{
		{name: "no event"},
		{name: "a person with triage permission", events: []githubtest.LabelEvent{readyBy("a-triager", 5)}, permissionReads: 1},
		{name: "a GitHub App", events: []githubtest.LabelEvent{
			{Label: "cumin/status/ready", At: sceneNow.Add(-5 * time.Minute), Actor: "some-app", ActorType: "Bot"}}},
		{name: "an account that no longer exists", events: []githubtest.LabelEvent{
			{Label: "cumin/status/ready", At: sceneNow.Add(-5 * time.Minute)}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := newScene(t)
			sc.addUnlinkedPullRequest(21, sc.remoteHead)
			sc.repo.Issues[10].LabelEvents = tt.events
			// GitHub reports triage as read.
			sc.fake.SetPermission("a-triager", "read", "User")

			sc.pollAndWait(t, sc.service())

			text := promptOf(t, sc.record(t, "agent.args"))
			if !strings.Contains(text, "- Issue of the run: #10 (implementation issue)"+noOwnerLoginLine) {
				t.Errorf("the prompt does not say that there is no Owner login:\n%s", text)
			}
			for _, login := range []string{"a-triager", "some-app"} {
				if strings.Contains(text, login) {
					t.Errorf("the prompt names %s, which is not the Owner:\n%s", login, text)
				}
			}
			n := 0
			for _, request := range sc.fake.Requests() {
				if request.Method == http.MethodGet && strings.HasSuffix(request.Path, "/permission") {
					n++
				}
			}
			if n != tt.permissionReads {
				t.Errorf("%d reads of a permission, want %d", n, tt.permissionReads)
			}
		})
	}
}

// A failed read of the login of the Owner sends no request and changes no
// label, so the next poll tries again. Then the label changes before the
// request, and the request names the Owner.
func TestOwnerLogin_AFailedReadSendsNoRequestAndTheNextPollTriesAgain(t *testing.T) {
	sc := newScene(t)
	sc.addUnlinkedPullRequest(21, sc.remoteHead)
	sc.repo.Issues[10].LabelEvents = []githubtest.LabelEvent{readyBy(theOwner, 5)}
	sc.fake.SetPermission(theOwner, "admin", "User")
	sc.fake.FailTimes(http.MethodGet, "/repos/example-org/example-repo/collaborators/"+theOwner+"/permission", 0, everyTry, http.StatusBadGateway)
	service := sc.service()

	// The poll reports the failed read or only logs it; both send nothing.
	_ = service.Poll(context.Background())
	service.Wait()

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none after a failed read", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, "cumin/status/ready") {
		t.Errorf("labels of #10 = %v, want cumin/status/ready: a failed read changes nothing", got)
	}
	if !strings.Contains(sc.logs.String(), "read the login of the Owner of issue #10") {
		t.Errorf("the log does not name the failed read:\n%s", sc.logs.String())
	}

	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1 after the next poll", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; slices.Contains(got, "cumin/status/ready") {
		t.Errorf("labels of #10 = %v, want no cumin/status/ready after the request", got)
	}
	if text := promptOf(t, sc.record(t, "agent.args")); !strings.Contains(text, ownerLoginLine) {
		t.Errorf("the prompt does not name the Owner %s:\n%s", theOwner, text)
	}
}
