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

// readyActorReads counts the queries of the actor of a label that the fake
// GitHub received. Only that query asks for the type of the actor.
func (sc *scene) readyActorReads() int {
	n := 0
	for _, r := range sc.fake.Requests() {
		if r.Method == http.MethodPost && r.Path == "/graphql" && strings.Contains(string(r.Body), "actor { __typename login }") {
			n++
		}
	}
	return n
}

// permissionReads counts the reads of the permission of an account.
func (sc *scene) permissionReads() int {
	n := 0
	for _, r := range sc.fake.Requests() {
		if r.Method == http.MethodGet && strings.HasSuffix(r.Path, "/permission") {
			n++
		}
	}
	return n
}

// labelChanges counts the label changes of every issue.
func (sc *scene) labelChanges() int {
	n := 0
	for _, r := range sc.fake.Requests() {
		if r.Method == http.MethodPut && strings.HasSuffix(r.Path, "/labels") {
			n++
		}
	}
	return n
}

// The ready of the Owner (issue-states.md, I1): the Implementer starts once
// when a person with write or admin permission added the newest
// cumin/status/ready.
func TestOwnerReady_AReadyOfTheOwnerStartsTheImplementerOnce(t *testing.T) {
	for _, permission := range []string{"write", "admin"} {
		t.Run(permission, func(t *testing.T) {
			sc := newScene(t)
			sc.addUnlinkedPullRequest(21, sc.remoteHead)
			sc.repo.Issues[10].LabelEvents = []githubtest.LabelEvent{readyBy(theOwner, 5)}
			sc.fake.SetPermission(theOwner, permission, "User")
			service := sc.service()

			for range 3 {
				_ = service.Poll(context.Background())
			}
			service.Wait()

			if n := sc.agentRuns(t); n != 1 {
				t.Errorf("%d agent runs, want 1", n)
			}
			if got := sc.fake.Issue(sc.repo, 10).Labels; slices.Contains(got, "cumin/status/ready") {
				t.Errorf("labels of #10 = %v, want no cumin/status/ready after the start", got)
			}
		})
	}
}

// The ready of the Owner (issue-states.md, R1): the Planner starts once
// when the Owner added the newest cumin/status/ready of the requirement
// issue.
func TestOwnerReady_AReadyOfTheOwnerStartsThePlannerOnce(t *testing.T) {
	sc := newPlanScene(t)
	sc.repo.Issues[6].LabelEvents = []githubtest.LabelEvent{readyBy("a-triager", 60), removedBefore(readyBy(theOwner, 5)), readyBy(theOwner, 5)}
	sc.fake.SetPermission(theOwner, "write", "User")
	service := sc.service()

	for range 3 {
		_ = service.Poll(context.Background())
	}
	service.Wait()

	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1", n)
	}
	if got := sc.fake.Issue(sc.repo, 6).Labels; slices.Contains(got, "cumin/status/ready") {
		t.Errorf("labels of #6 = %v, want no cumin/status/ready after the start", got)
	}
}

// notOwners are the accounts whose cumin/status/ready starts nothing: a
// person with triage permission (GitHub reports triage as read), a GitHub
// App, a bot account with write permission, a person without write
// permission, and an account that no longer exists.
var notOwners = []struct {
	name  string
	event githubtest.LabelEvent
}{
	{name: "a person with triage permission", event: readyBy("a-triager", 5)},
	{name: "a GitHub App", event: githubtest.LabelEvent{
		Label: "cumin/status/ready", At: sceneNow.Add(-5 * time.Minute), Actor: "some-app", ActorType: "Bot"}},
	{name: "a bot account with write permission", event: readyBy("writer-bot", 5)},
	{name: "a person without permission", event: readyBy("a-visitor", 5)},
	{name: "an account that no longer exists", event: githubtest.LabelEvent{
		Label: "cumin/status/ready", At: sceneNow.Add(-5 * time.Minute)}},
}

func setNotOwnerPermissions(sc *scene) {
	sc.fake.SetPermission(theOwner, "admin", "User")
	sc.fake.SetPermission("a-triager", "read", "User")
	sc.fake.SetPermission("writer-bot", "write", "Bot")
	sc.fake.SetPermission("a-visitor", "none", "User")
}

// A ready of an account that is not the Owner changes no label and sends
// no request (issue-states.md, the ready of the Owner), also when an older
// ready was of the Owner: the label was removed, and the other account put
// it on the issue again. Across three polls, cumin logs it once and tells
// the Owner once.
func TestOwnerReady_AReadyOfAnotherAccountStartsNothingAndIsToldOnce(t *testing.T) {
	scenes := []struct {
		name   string
		number int
		row    string
		scene  func(*testing.T) *scene
	}{
		{name: "an implementation issue", number: 10, row: "request the implementation", scene: func(t *testing.T) *scene { return newScene(t) }},
		{name: "a requirement issue", number: 6, row: "request the split", scene: newPlanScene},
	}
	for _, s := range scenes {
		for _, tt := range notOwners {
			t.Run(s.name+"/"+tt.name, func(t *testing.T) {
				sc := s.scene(t)
				sc.repo.Issues[s.number].LabelEvents = []githubtest.LabelEvent{readyBy(theOwner, 60), removedBefore(tt.event), tt.event}
				setNotOwnerPermissions(sc)
				service := sc.service()

				for i := range 3 {
					if err := service.Poll(context.Background()); err != nil {
						t.Fatalf("poll %d: %v", i+1, err)
					}
				}
				service.Wait()

				if n := sc.agentRuns(t); n != 0 {
					t.Errorf("%d agent runs, want none", n)
				}
				if n := sc.labelChanges(); n != 0 {
					t.Errorf("%d label changes, want none", n)
				}
				if got := sc.fake.Issue(sc.repo, s.number).Labels; !slices.Contains(got, "cumin/status/ready") {
					t.Errorf("labels of #%d = %v, want cumin/status/ready", s.number, got)
				}
				if n := strings.Count(sc.logs.String(), "is not of a Maintainer"); n != 1 {
					t.Errorf("%d log lines for one ready event across three polls, want 1:\n%s", n, sc.logs.String())
				}
				messages := sc.messagesExceptQ4()
				if len(messages) != 1 || !strings.Contains(messages[0], s.row) || !strings.Contains(messages[0], "is not a person with write access") {
					t.Errorf("messages = %q, want one of %s about the ready that is not of the Owner", messages, s.row)
				}
			})
		}
	}
}

// GitHub can record an event that adds cumin/status/ready again while the
// label is on the issue, late and with the GitHub App that created the
// issue (measured on 2026-10-05). The event of the Owner put the label on
// the issue, so the issue starts as the ready of the Owner.
func TestOwnerReady_ARepeatedReadyEventOfAGitHubAppDoesNotHideTheOwner(t *testing.T) {
	scenes := []struct {
		name   string
		number int
		scene  func(*testing.T) *scene
	}{
		{name: "an implementation issue", number: 10, scene: func(t *testing.T) *scene {
			sc := newScene(t)
			sc.addUnlinkedPullRequest(21, sc.remoteHead)
			return sc
		}},
		{name: "a requirement issue", number: 6, scene: newPlanScene},
	}
	for _, s := range scenes {
		t.Run(s.name, func(t *testing.T) {
			sc := s.scene(t)
			sc.repo.Issues[s.number].LabelEvents = []githubtest.LabelEvent{
				readyBy(theOwner, 5),
				{Label: "cumin/status/ready", At: sceneNow.Add(-4 * time.Minute), Actor: plannerLogin, ActorType: "Bot"},
			}
			setNotOwnerPermissions(sc)
			service := sc.service()

			for range 3 {
				_ = service.Poll(context.Background())
			}
			service.Wait()

			if n := sc.agentRuns(t); n != 1 {
				t.Errorf("%d agent runs, want 1", n)
			}
			if got := sc.fake.Issue(sc.repo, s.number).Labels; slices.Contains(got, "cumin/status/ready") {
				t.Errorf("labels of #%d = %v, want no cumin/status/ready after the start", s.number, got)
			}
			if strings.Contains(sc.logs.String(), "is not of a Maintainer") {
				t.Errorf("the ready of the Owner was logged as one of another account:\n%s", sc.logs.String())
			}
		})
	}
}

// A new ready event of another account on the same issue is a new event:
// cumin logs it once more.
func TestOwnerReady_ANewReadyOfAnotherAccountIsLoggedAgain(t *testing.T) {
	sc := newScene(t)
	sc.repo.Issues[10].LabelEvents = []githubtest.LabelEvent{readyBy("a-triager", 5)}
	setNotOwnerPermissions(sc)
	service := sc.service()
	sc.pollAndWait(t, service)
	sc.pollAndWait(t, service)

	sc.repo.Issues[10].LabelEvents = append(sc.repo.Issues[10].LabelEvents, removedBefore(readyBy("a-triager", 1)), readyBy("a-triager", 1))
	sc.pollAndWait(t, service)
	sc.pollAndWait(t, service)

	if n := strings.Count(sc.logs.String(), "is not of a Maintainer"); n != 2 {
		t.Errorf("%d log lines for two ready events, want 2:\n%s", n, sc.logs.String())
	}
}

// An issue whose ready is not of the Owner takes no slot: with one slot,
// another ready issue of the Owner starts in the same poll, although its
// number is higher.
func TestOwnerReady_AReadyOfAnotherAccountTakesNoSlot(t *testing.T) {
	sc := newScene(t)
	sc.repo.Issues[10].LabelEvents = []githubtest.LabelEvent{readyBy("a-triager", 5)}
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 11, Parent: 6, Title: "Another issue", Labels: []string{"cumin/status/ready", "risk/low"},
		LabelEvents: []githubtest.LabelEvent{readyBy(theOwner, 5)}})
	setNotOwnerPermissions(sc)
	service := sc.service()

	// The run for #11 leaves no pull request, so its implementation is
	// requested again once; no run starts for #10.
	_ = service.Poll(context.Background())
	service.Wait()

	if n := sc.agentRuns(t); n != 2 {
		t.Errorf("%d agent runs, want 2 for #11", n)
	}
	if got := sc.fake.Issue(sc.repo, 11).Labels; slices.Contains(got, "cumin/status/ready") {
		t.Errorf("labels of #11 = %v, want no cumin/status/ready: the Owner's ready starts", got)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, "cumin/status/ready") {
		t.Errorf("labels of #10 = %v, want cumin/status/ready", got)
	}
}

// A poll with no start candidate, or with no free slot, reads no actor and
// no permission, so that the poll keeps its cost.
func TestOwnerReady_NoReadWithoutACandidateOrWithoutAFreeSlot(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*scene)
	}{
		{name: "no start candidate", setup: func(sc *scene) {
			sc.repo.Issues[10].Labels = []string{"cumin/status/awaiting-decision", "risk/low"}
		}},
		{name: "an open blocked-by issue", setup: func(sc *scene) {
			sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 9, Parent: 6, Title: "The work before", Labels: []string{"risk/low"}})
			sc.repo.Issues[10].BlockedBy = []int{9}
		}},
		{name: "no free slot", setup: func(sc *scene) {
			sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 11, Parent: 6, Title: "Another issue", Labels: []string{"cumin/status/checking", "risk/low"}})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := newScene(t)
			sc.repo.Issues[10].LabelEvents = []githubtest.LabelEvent{readyBy(theOwner, 5)}
			sc.fake.SetPermission(theOwner, "admin", "User")
			tt.setup(sc)

			sc.pollAndWait(t, sc.service())

			if n := sc.agentRuns(t); n != 0 {
				t.Errorf("%d agent runs, want none", n)
			}
			if n := sc.readyActorReads(); n != 0 {
				t.Errorf("%d reads of the actor of a label, want none", n)
			}
			if n := sc.permissionReads(); n != 0 {
				t.Errorf("%d reads of a permission, want none", n)
			}
		})
	}
}

// A start reads the actor and the permission once: the request takes the
// login of the Owner from the read of the poll.
func TestOwnerReady_AStartReadsTheActorAndThePermissionOnce(t *testing.T) {
	sc := newScene(t)
	sc.addUnlinkedPullRequest(21, sc.remoteHead)
	sc.repo.Issues[10].LabelEvents = []githubtest.LabelEvent{readyBy(theOwner, 5)}
	sc.fake.SetPermission(theOwner, "admin", "User")

	sc.pollAndWait(t, sc.service())

	// The end of the run reads the actor of cumin/status/implementing; that
	// account is cumin-core, so no permission is read for it.
	if n := sc.readyActorReads(); n != 2 {
		t.Errorf("%d reads of the actor of a label, want 2: the ready and the label of the run", n)
	}
	if n := sc.permissionReads(); n != 1 {
		t.Errorf("%d reads of a permission, want 1", n)
	}
	if text := promptOf(t, sc.record(t, "agent.args")); !strings.Contains(text, ownerLoginLine) {
		t.Errorf("the prompt does not name the Owner %s:\n%s", theOwner, text)
	}
}

// A failed read of the actor logs an error, changes nothing, and the next
// poll starts the issue.
func TestOwnerReady_AFailedReadWaitsForTheNextPoll(t *testing.T) {
	sc := newScene(t)
	sc.addUnlinkedPullRequest(21, sc.remoteHead)
	sc.repo.Issues[10].LabelEvents = []githubtest.LabelEvent{readyBy(theOwner, 5)}
	sc.fake.SetPermission(theOwner, "admin", "User")
	sc.fake.FailTimes(http.MethodGet, "/repos/example-org/example-repo/collaborators/"+theOwner+"/permission", 0, everyTry, http.StatusBadGateway)
	service := sc.service()

	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none after a failed read", n)
	}
	if n := sc.labelChanges(); n != 0 {
		t.Errorf("%d label changes, want none after a failed read", n)
	}
	if !strings.Contains(sc.logs.String(), "the actor of the newest cumin/status/ready was not read") {
		t.Errorf("the log does not name the failed read:\n%s", sc.logs.String())
	}
	if strings.Contains(sc.logs.String(), "is not of a Maintainer") {
		t.Errorf("a failed read is logged as a ready that is not of the Owner:\n%s", sc.logs.String())
	}

	sc.pollAndWait(t, service)
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs after the second poll, want 1", n)
	}
}

// ReadyActorReads names the candidates of R1 and of I1 in the order of the
// starts, and none without a free slot. Decide starts only a candidate
// whose ready the Owner added.
func TestReadyActorReads_NamesTheStartCandidatesOnlyWithAFreeSlot(t *testing.T) {
	ready := []string{workflow.LabelReady}
	snapshot := workflow.Snapshot{RequirementIssues: []workflow.RequirementIssue{
		{Number: 1, Labels: []string{workflow.LabelImplementing}, SubIssues: []workflow.SubIssue{
			{Number: 12, Labels: ready},
			{Number: 11, Labels: ready, BlockedBy: []workflow.BlockedBy{{Number: 12}}},
			{Number: 13, Labels: ready, Closed: true},
			{Number: 14, Labels: []string{workflow.LabelReady, workflow.LabelOwnerTask}},
		}},
		{Number: 2, Labels: ready},
	}}

	numbers, room := workflow.ReadyActorReads(snapshot, 2, nil)
	if !slices.Equal(numbers, []int{2, 12}) || room != 2 {
		t.Errorf("ReadyActorReads = %v with %d free slots, want [2 12] with 2", numbers, room)
	}
	if actions := workflow.Decide(snapshot, 2, nil, nil, sceneNow, time.Hour); len(actions) != 0 {
		t.Errorf("Decide = %v, want no start before the ready was read", actions)
	}

	snapshot.RequirementIssues[0].SubIssues[0].Labels = []string{workflow.LabelImplementing}
	snapshot.RequirementIssues[0].SubIssues[1].BlockedBy = nil
	if numbers, _ := workflow.ReadyActorReads(snapshot, 1, nil); len(numbers) != 0 {
		t.Errorf("ReadyActorReads = %v with no free slot, want none", numbers)
	}

	// A ready that was not read can still start; a ready of another
	// account waits for the Owner (the table under Q4). #12 leaves its
	// working label, which would count by itself.
	snapshot.RequirementIssues[0].SubIssues[0].Labels = []string{workflow.LabelAwaitingDecision}
	if !snapshot.MovesWithoutOwner() {
		t.Error("MovesWithoutOwner = false, want true while a ready was not read")
	}
	snapshot.RequirementIssues[1].ReadyRead = true
	snapshot.RequirementIssues[0].SubIssues[1].ReadyRead = true
	if snapshot.MovesWithoutOwner() {
		t.Error("MovesWithoutOwner = true, want false when every ready is of another account")
	}
	snapshot.RequirementIssues[0].SubIssues[1].ReadyOwner = theOwner
	actions := workflow.Decide(snapshot, 3, nil, nil, sceneNow, time.Hour)
	if len(actions) != 1 || actions[0] != (workflow.Claim{Number: 11, RequirementIssue: 1}) {
		t.Errorf("Decide = %v, want only the claim of #11, whose ready is of the Owner", actions)
	}
}

// Only an event of the issue itself answers the check. A ready of a triage
// account that left the newest label events that cumin reads starts
// nothing, although a sub-issue has a ready of the Owner.
func TestOwnerReady_AReadyOutsideTheEventsReadDoesNotTakeTheOwnerOfASubIssue(t *testing.T) {
	sc := newPlanScene(t)
	events := []githubtest.LabelEvent{readyBy("a-triager", 500)}
	for i := range 101 {
		events = append(events, githubtest.LabelEvent{Label: "risk/low", At: sceneNow.Add(-time.Duration(400-i) * time.Minute), Actor: "a-triager", ActorType: "User"})
	}
	sc.repo.Issues[6].LabelEvents = events
	sc.repo.Issues[10].LabelEvents = []githubtest.LabelEvent{readyBy(theOwner, 5)}
	setNotOwnerPermissions(sc)
	service := sc.service()

	for i := range 3 {
		if err := service.Poll(context.Background()); err != nil {
			t.Fatalf("poll %d: %v", i+1, err)
		}
	}
	service.Wait()

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none", n)
	}
	if n := sc.labelChanges(); n != 0 {
		t.Errorf("%d label changes, want none", n)
	}
	if n := strings.Count(sc.logs.String(), "is not of a Maintainer"); n != 1 {
		t.Errorf("%d log lines across three polls, want 1:\n%s", n, sc.logs.String())
	}
	if messages := sc.messagesExceptQ4(); len(messages) != 1 || !strings.Contains(messages[0], "could not find") {
		t.Errorf("messages = %q, want one that says that cumin found no ready event", messages)
	}
}
