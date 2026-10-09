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

// The account that added a status label (issue-states.md): a status label
// counts when a Maintainer added it or, for every label but
// cumin/status/ready, the cumin-core App.
func TestStatusLabelCounts(t *testing.T) {
	const core = "example-cumin-core[bot]"
	tests := []struct {
		name  string
		label string
		actor workflow.StatusActor
		want  bool
	}{
		{name: "the cumin-core App", label: workflow.LabelPlanning,
			actor: workflow.StatusActor{Login: "example-cumin-core", Type: "Bot"}, want: true},
		{name: "the cumin-core App in the REST form", label: workflow.LabelAccepting,
			actor: workflow.StatusActor{Login: "example-cumin-core[bot]", Type: "Bot"}, want: true},
		{name: "a Maintainer with admin permission", label: workflow.LabelPlanning,
			actor: workflow.StatusActor{Login: "the-owner", Type: "User", Permission: "admin", UserType: "User"}, want: true},
		{name: "a Maintainer with write permission", label: workflow.LabelAccepting,
			actor: workflow.StatusActor{Login: "the-owner", Type: "User", Permission: "write", UserType: "User"}, want: true},
		{name: "a Maintainer adds cumin/status/ready", label: workflow.LabelReady,
			actor: workflow.StatusActor{Login: "the-owner", Type: "User", Permission: "admin", UserType: "User"}, want: true},
		{name: "a triage account", label: workflow.LabelPlanning,
			actor: workflow.StatusActor{Login: "a-triager", Type: "User", Permission: "read", UserType: "User"}},
		{name: "a person whose permission was not read", label: workflow.LabelPlanning,
			actor: workflow.StatusActor{Login: "a-triager", Type: "User"}},
		{name: "a person with the name of cumin-core", label: workflow.LabelPlanning,
			actor: workflow.StatusActor{Login: "example-cumin-core", Type: "User", Permission: "read", UserType: "User"}},
		{name: "another GitHub App", label: workflow.LabelPlanning,
			actor: workflow.StatusActor{Login: "example-implementer", Type: "Bot"}},
		{name: "a bot account with write permission", label: workflow.LabelPlanning,
			actor: workflow.StatusActor{Login: "writer-bot", Type: "User", Permission: "write", UserType: "Bot"}},
		{name: "the cumin-core App adds cumin/status/ready", label: workflow.LabelReady,
			actor: workflow.StatusActor{Login: "example-cumin-core", Type: "Bot"}},
		{name: "an account that no longer exists", label: workflow.LabelPlanning,
			actor: workflow.StatusActor{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := workflow.StatusLabelCounts(tt.label, tt.actor, core); got != tt.want {
				t.Errorf("StatusLabelCounts(%s, %+v) = %v, want %v", tt.label, tt.actor, got, tt.want)
			}
		})
	}
	t.Run("a GitHub App when the login of cumin-core is unknown", func(t *testing.T) {
		if workflow.StatusLabelCounts(workflow.LabelPlanning, workflow.StatusActor{Login: "[bot]", Type: "Bot"}, "") {
			t.Error("a GitHub App counts without the login of cumin-core, want not")
		}
	})
}

// The poll reads the account of a status label only for an issue that
// cumin is about to act from, with no Planner running: in
// cumin/status/planning or in cumin/status/accepting, or in
// cumin/status/implementing or cumin/status/awaiting-plan-review with one
// or more sub-issues, all closed ("request the acceptance check").
func TestStatusActorReads(t *testing.T) {
	requirement := func(number int, status string, subs ...workflow.SubIssue) workflow.RequirementIssue {
		return workflow.RequirementIssue{Number: number, Labels: []string{"cumin/type/requirement", status}, SubIssues: subs}
	}
	closed, open := workflow.SubIssue{Number: 100, Closed: true}, workflow.SubIssue{Number: 101}
	snapshot := workflow.Snapshot{
		RequirementIssues: []workflow.RequirementIssue{
			requirement(16, workflow.LabelAwaitingAcceptance, closed),
			requirement(15, workflow.LabelImplementing, closed),
			requirement(14, workflow.LabelAwaitingPlanReview, closed, open),
			requirement(13, workflow.LabelImplementing, closed, open),
			requirement(12, workflow.LabelAwaitingPlanReview, closed, closed),
			requirement(11, workflow.LabelImplementing, closed),
			requirement(9, workflow.LabelAccepting),
			requirement(8, workflow.LabelPlanning),
			requirement(7, workflow.LabelPlanning),
			requirement(6, workflow.LabelImplementing),
			requirement(5, workflow.LabelAwaitingPlanReview),
			requirement(4, workflow.LabelReady),
			requirement(3, workflow.LabelAccepting),
		},
		Running: map[int]bool{8: true, 3: true, 15: true},
	}
	if got, want := workflow.StatusActorReads(snapshot), []int{7, 9, 11, 12}; !slices.Equal(got, want) {
		t.Errorf("StatusActorReads = %v, want %v", got, want)
	}
}

// statusBy is the event of a status label that the login added, as a
// person.
func statusBy(label, login string) githubtest.LabelEvent {
	return githubtest.LabelEvent{Label: label, At: sceneNow.Add(-time.Hour), Actor: login, ActorType: "User"}
}

// statusScenes are the states that the poll decides from: a requirement
// issue in cumin/status/planning and one in cumin/status/accepting, with no
// Planner running. Each one starts a Planner when its label counts.
var statusScenes = []struct {
	label string
	scene func(*testing.T) *scene
}{
	{label: "cumin/status/planning", scene: func(t *testing.T) *scene {
		sc, _ := planningScene(t, cliOptions{fixture: "planner-done.jsonl"})
		return sc
	}},
	{label: "cumin/status/accepting", scene: func(t *testing.T) *scene {
		sc, _ := acceptingScene(t, cliOptions{fixture: "planner-done.jsonl"})
		return sc
	}},
}

// acceptanceStarts are the two starting states of "request the acceptance
// check": a requirement issue in cumin/status/implementing and one in
// cumin/status/awaiting-plan-review, every sub-issue closed. The Planner
// holds its run until the test releases it.
var acceptanceStarts = []struct {
	label string
	scene func(*testing.T) *scene
}{
	{label: "cumin/status/implementing", scene: func(t *testing.T) *scene {
		sc, _ := newAcceptanceScene(t, cliOptions{fixture: "planner-done.jsonl", holds: true})
		return sc
	}},
	{label: "cumin/status/awaiting-plan-review", scene: func(t *testing.T) *scene {
		sc, _ := planReviewScene(t, cliOptions{fixture: "planner-done.jsonl", holds: true})
		return sc
	}},
}

// endAcceptanceCheck lets the held Planner write its acceptance check
// comment and end its run.
func endAcceptanceCheck(t *testing.T, sc *scene, service *workflow.Service) {
	t.Helper()
	acceptanceComment(sc, sceneNow.Add(-time.Minute), plannerLogin)
	sc.release(t)
	service.Wait()
}

// A status label of an account that is not cumin-core or a Maintainer
// (cumin-core.md, test 30): no agent starts and no label changes. Across
// three polls, cumin logs it once and tells the Maintainer once.
func TestStatusLabel_ALabelOfAnotherAccountDoesNothingAndIsToldOnce(t *testing.T) {
	others := []struct {
		name  string
		event func(label string) githubtest.LabelEvent
	}{
		{name: "a person with triage permission", event: func(label string) githubtest.LabelEvent { return statusBy(label, "a-triager") }},
		{name: "another GitHub App", event: func(label string) githubtest.LabelEvent {
			return githubtest.LabelEvent{Label: label, At: sceneNow.Add(-time.Hour), Actor: implementerSlug, ActorType: "Bot"}
		}},
		{name: "a bot account with write permission", event: func(label string) githubtest.LabelEvent { return statusBy(label, "writer-bot") }},
		{name: "an account that no longer exists", event: func(label string) githubtest.LabelEvent {
			return githubtest.LabelEvent{Label: label, At: sceneNow.Add(-time.Hour)}
		}},
	}
	for _, s := range slices.Concat(statusScenes, acceptanceStarts) {
		for _, tt := range others {
			t.Run(s.label+"/"+tt.name, func(t *testing.T) {
				sc := s.scene(t)
				// An older event of the Maintainer does not count: the label was
				// removed, and the other account put it on the issue again.
				older := statusBy(s.label, theMaintainer)
				older.At = older.At.Add(-time.Hour)
				sc.repo.Issues[6].LabelEvents = []githubtest.LabelEvent{older, removedBefore(tt.event(s.label)), tt.event(s.label)}
				setNotMaintainerPermissions(sc)
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
				if got := sc.fake.Issue(sc.repo, 6).Labels; !slices.Contains(got, s.label) {
					t.Errorf("labels of #6 = %v, want %s", got, s.label)
				}
				if n := len(sc.fake.Comments(sc.repo, 6)); n != 0 {
					t.Errorf("%d comments on #6, want none", n)
				}
				if n := strings.Count(sc.logs.String(), "is not of cumin-core or of a Maintainer"); n != 1 {
					t.Errorf("%d log lines for one label event across three polls, want 1:\n%s", n, sc.logs.String())
				}
				messages := sc.messagesExceptWaiting()
				if len(messages) != 1 || !strings.Contains(messages[0], s.label) || !strings.Contains(messages[0], "is not cumin-core and not a person with write access") {
					t.Errorf("messages = %q, want one about the %s that does not count", messages, s.label)
				}
			})
		}
	}
}

// A status label that cumin-core or a Maintainer added decides as before: the
// poll requests the Planner again for the issue that no Planner works on.
func TestStatusLabel_ALabelOfCuminCoreOrOfAMaintainerDecidesAsBefore(t *testing.T) {
	counted := []struct {
		name  string
		event func(label string) githubtest.LabelEvent
	}{
		{name: "the cumin-core App", event: func(label string) githubtest.LabelEvent {
			return githubtest.LabelEvent{Label: label, At: sceneNow.Add(-time.Hour), Actor: cuminSlug, ActorType: "Bot"}
		}},
		{name: "a Maintainer", event: func(label string) githubtest.LabelEvent { return statusBy(label, theMaintainer) }},
	}
	for _, s := range statusScenes {
		for _, tt := range counted {
			t.Run(s.label+"/"+tt.name, func(t *testing.T) {
				sc := s.scene(t)
				sc.repo.Issues[6].LabelEvents = []githubtest.LabelEvent{tt.event(s.label)}
				setNotMaintainerPermissions(sc)
				service := sc.service()

				sc.pollAndWait(t, service)

				if n := sc.agentRuns(t); n != 1 {
					t.Errorf("%d agent runs, want 1", n)
				}
				if strings.Contains(sc.logs.String(), "is not of cumin-core or of a Maintainer") {
					t.Errorf("a label that counts was logged as one of another account:\n%s", sc.logs.String())
				}
			})
		}
	}
}

// "request the acceptance check" from each of its two starting states: a
// status label of cumin-core or of a Maintainer moves the requirement issue
// to cumin/status/accepting and starts the Planner once.
func TestStatusLabel_ALabelThatCountsRequestsTheAcceptanceCheck(t *testing.T) {
	actors := []githubtest.LabelEvent{
		{At: sceneNow.Add(-2 * time.Hour), Actor: cuminSlug, ActorType: "Bot"},
		{At: sceneNow.Add(-2 * time.Hour), Actor: theMaintainer, ActorType: "User"},
	}
	for _, s := range acceptanceStarts {
		for _, event := range actors {
			t.Run(s.label+"/"+event.Actor, func(t *testing.T) {
				sc := s.scene(t)
				event.Label = s.label
				sc.repo.Issues[6].LabelEvents = []githubtest.LabelEvent{event}
				setNotMaintainerPermissions(sc)
				service := sc.service()

				for i := range 3 {
					if err := service.Poll(t.Context()); err != nil {
						t.Fatalf("poll %d: %v", i+1, err)
					}
				}
				waitForAgentRun(t, sc)

				accepting := []string{githubtest.RequirementLabel, workflow.LabelAccepting}
				if got := requirementLabels(t, sc); !slices.Equal(got, accepting) {
					t.Errorf("labels of #6 = %v, want %v", got, accepting)
				}
				endAcceptanceCheck(t, sc, service)
				if n := sc.agentRuns(t); n != 1 {
					t.Errorf("%d agent runs, want 1", n)
				}
				if strings.Contains(sc.logs.String(), "is not of cumin-core or of a Maintainer") {
					t.Errorf("a label that counts was logged as one of another account:\n%s", sc.logs.String())
				}
			})
		}
	}
}

// A failed read of the account gives no start of "request the acceptance
// check" and no label change. The next poll reads again and starts.
func TestStatusLabel_AFailedReadKeepsTheAcceptanceCheckForTheNextPoll(t *testing.T) {
	for _, s := range acceptanceStarts {
		t.Run(s.label, func(t *testing.T) {
			sc := s.scene(t)
			sc.repo.Issues[6].LabelEvents = []githubtest.LabelEvent{statusBy(s.label, theMaintainer)}
			sc.fake.SetPermission(theMaintainer, "admin", "User")
			permission := "/repos/example-org/example-repo/collaborators/" + theMaintainer + "/permission"
			sc.fake.FailTimes(http.MethodGet, permission, 0, everyTry, http.StatusBadGateway)
			service := sc.service()

			sc.pollAndWait(t, service)

			if n := sc.agentRuns(t); n != 0 {
				t.Errorf("%d agent runs, want none after a failed read", n)
			}
			if n := sc.labelChanges(); n != 0 {
				t.Errorf("%d label changes, want none after a failed read", n)
			}
			if !strings.Contains(sc.logs.String(), "the actor of the newest "+s.label+" was not read") {
				t.Errorf("the log does not name the failed read:\n%s", sc.logs.String())
			}
			if strings.Contains(sc.logs.String(), "is not of cumin-core or of a Maintainer") {
				t.Errorf("a failed read is logged as a label of another account:\n%s", sc.logs.String())
			}

			if err := service.Poll(t.Context()); err != nil {
				t.Fatalf("Poll: %v", err)
			}
			waitForAgentRun(t, sc)

			if n := sc.fake.CountRequests(http.MethodGet, permission); n != everyTry+1 {
				t.Errorf("%d reads of the permission, want %d: the second poll reads again", n, everyTry+1)
			}
			accepting := []string{githubtest.RequirementLabel, workflow.LabelAccepting}
			if got := requirementLabels(t, sc); !slices.Equal(got, accepting) {
				t.Errorf("labels of #6 = %v after the second poll, want %v", got, accepting)
			}
			endAcceptanceCheck(t, sc, service)
			if n := sc.agentRuns(t); n != 1 {
				t.Errorf("%d agent runs after the second poll, want 1", n)
			}
		})
	}
}

// GitHub can record an event that adds a status label again while the
// label is on the issue, late and with another GitHub App (measured on
// 2026-10-05). The event that put the label on the issue decides: the poll
// requests the Planner as before.
func TestStatusLabel_ARepeatedEventOfAnotherGitHubAppDoesNotHideTheAccount(t *testing.T) {
	for _, s := range statusScenes {
		t.Run(s.label, func(t *testing.T) {
			sc := s.scene(t)
			sc.repo.Issues[6].LabelEvents = []githubtest.LabelEvent{
				statusBy(s.label, theMaintainer),
				{Label: s.label, At: sceneNow.Add(-30 * time.Minute), Actor: implementerSlug, ActorType: "Bot"},
			}
			setNotMaintainerPermissions(sc)
			service := sc.service()

			sc.pollAndWait(t, service)

			if n := sc.agentRuns(t); n != 1 {
				t.Errorf("%d agent runs, want 1", n)
			}
			if strings.Contains(sc.logs.String(), "is not of cumin-core or of a Maintainer") {
				t.Errorf("a label that counts was logged as one of another account:\n%s", sc.logs.String())
			}
		})
	}
}

// A poll with no issue to decide from a state reads no account: the cost of
// a normal poll stays the same. While the Planner runs, the poll decides
// nothing from cumin/status/planning, so it reads no account either.
func TestStatusLabel_APollWithNothingToDecideReadsNoAccount(t *testing.T) {
	t.Run("no issue in a state that the poll decides from", func(t *testing.T) {
		sc := newScene(t)
		sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 6, Labels: []string{githubtest.RequirementLabel, "cumin/status/awaiting-plan-review"}})
		sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Labels: []string{"risk/low"}})
		service := sc.service()

		for range 3 {
			sc.pollAndWait(t, service)
		}

		if n := sc.readyActorReads(); n != 0 {
			t.Errorf("%d reads of the account of a label, want none", n)
		}
		if n := sc.permissionReads(); n != 0 {
			t.Errorf("%d reads of a permission, want none", n)
		}
	})
	t.Run("the Planner runs", func(t *testing.T) {
		sc, _ := planningScene(t, cliOptions{fixture: "planner-done.jsonl", holds: true})
		service := sc.service()
		if err := service.Poll(t.Context()); err != nil {
			t.Fatalf("Poll: %v", err)
		}
		waitForAgentRun(t, sc)
		reads := sc.readyActorReads()

		for range 3 {
			if err := service.Poll(t.Context()); err != nil {
				t.Fatalf("Poll: %v", err)
			}
		}

		if n := sc.readyActorReads(); n != reads {
			t.Errorf("%d reads of the account of a label after three polls during the run, want %d", n, reads)
		}
		sc.release(t)
		service.Wait()
	})
}

// The waiting notification (issue-states.md): an issue whose status label
// another account added waits for the Maintainer, so it is not an issue that
// cumin moves on without the Maintainer. A label that was not read counts.
func TestStatusLabel_ALabelOfAnotherAccountDoesNotMoveOnWithoutTheMaintainer(t *testing.T) {
	// cumin/status/awaiting-plan-review moves on only with every sub-issue
	// closed ("request the acceptance check").
	for _, label := range []string{workflow.LabelPlanning, workflow.LabelAccepting, workflow.LabelAwaitingPlanReview} {
		requirement := workflow.RequirementIssue{Number: 6, Labels: []string{"cumin/type/requirement", label},
			SubIssues: []workflow.SubIssue{{Number: 10, Closed: true}}}
		snapshot := func(read, counts bool) workflow.Snapshot {
			requirement.StatusRead, requirement.StatusCounts = read, counts
			return workflow.Snapshot{RequirementIssues: []workflow.RequirementIssue{requirement}}
		}
		if snapshot(true, false).MovesWithoutMaintainer() {
			t.Errorf("%s of another account moves on without the Maintainer, want not", label)
		}
		if !snapshot(true, true).MovesWithoutMaintainer() {
			t.Errorf("%s of cumin-core or a Maintainer does not move on without the Maintainer", label)
		}
		if !snapshot(false, false).MovesWithoutMaintainer() {
			t.Errorf("%s whose account was not read does not move on without the Maintainer", label)
		}
	}
}
