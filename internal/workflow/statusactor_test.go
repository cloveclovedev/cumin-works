package workflow_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
	"github.com/cloveclovedev/cumin-works/internal/workflow"
)

// The account that added a status label (issue-states.md): a status label
// counts when an Owner added it or, for every label but
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
		{name: "an Owner with admin permission", label: workflow.LabelPlanning,
			actor: workflow.StatusActor{Login: "the-owner", Type: "User", Permission: "admin", UserType: "User"}, want: true},
		{name: "an Owner with write permission", label: workflow.LabelAccepting,
			actor: workflow.StatusActor{Login: "the-owner", Type: "User", Permission: "write", UserType: "User"}, want: true},
		{name: "an Owner adds cumin/status/ready", label: workflow.LabelReady,
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
// cumin is about to act from: in cumin/status/planning or in
// cumin/status/accepting, with no Planner running.
func TestStatusActorReads(t *testing.T) {
	requirement := func(number int, status string) workflow.RequirementIssue {
		return workflow.RequirementIssue{Number: number, Labels: []string{"cumin/type/requirement", status}}
	}
	snapshot := workflow.Snapshot{
		RequirementIssues: []workflow.RequirementIssue{
			requirement(9, workflow.LabelAccepting),
			requirement(8, workflow.LabelPlanning),
			requirement(7, workflow.LabelPlanning),
			requirement(6, workflow.LabelImplementing),
			requirement(5, workflow.LabelAwaitingPlanReview),
			requirement(4, workflow.LabelReady),
			requirement(3, workflow.LabelAccepting),
		},
		Running: map[int]bool{8: true, 3: true},
	}
	if got, want := workflow.StatusActorReads(snapshot), []int{7, 9}; !slices.Equal(got, want) {
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

// A status label of an account that is not cumin-core or an Owner
// (cumin-core.md, test 30): no agent starts and no label changes. Across
// three polls, cumin logs it once and tells the Owner once.
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
	for _, s := range statusScenes {
		for _, tt := range others {
			t.Run(s.label+"/"+tt.name, func(t *testing.T) {
				sc := s.scene(t)
				// An older event of the Owner does not count: the newest one decides.
				older := statusBy(s.label, theOwner)
				older.At = older.At.Add(-time.Hour)
				sc.repo.Issues[6].LabelEvents = []githubtest.LabelEvent{older, tt.event(s.label)}
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
				if got := sc.fake.Issue(sc.repo, 6).Labels; !slices.Contains(got, s.label) {
					t.Errorf("labels of #6 = %v, want %s", got, s.label)
				}
				if n := len(sc.fake.Comments(sc.repo, 6)); n != 0 {
					t.Errorf("%d comments on #6, want none", n)
				}
				if n := strings.Count(sc.logs.String(), "is not of cumin-core or of an Owner"); n != 1 {
					t.Errorf("%d log lines for one label event across three polls, want 1:\n%s", n, sc.logs.String())
				}
				messages := sc.messagesExceptQ4()
				if len(messages) != 1 || !strings.Contains(messages[0], s.label) || !strings.Contains(messages[0], "is not cumin-core or an Owner") {
					t.Errorf("messages = %q, want one about the %s that does not count", messages, s.label)
				}
			})
		}
	}
}

// A status label that cumin-core or an Owner added decides as before: the
// poll requests the Planner again for the issue that no Planner works on.
func TestStatusLabel_ALabelOfCuminCoreOrOfAnOwnerDecidesAsBefore(t *testing.T) {
	counted := []struct {
		name  string
		event func(label string) githubtest.LabelEvent
	}{
		{name: "the cumin-core App", event: func(label string) githubtest.LabelEvent {
			return githubtest.LabelEvent{Label: label, At: sceneNow.Add(-time.Hour), Actor: cuminSlug, ActorType: "Bot"}
		}},
		{name: "an Owner", event: func(label string) githubtest.LabelEvent { return statusBy(label, theOwner) }},
	}
	for _, s := range statusScenes {
		for _, tt := range counted {
			t.Run(s.label+"/"+tt.name, func(t *testing.T) {
				sc := s.scene(t)
				sc.repo.Issues[6].LabelEvents = []githubtest.LabelEvent{tt.event(s.label)}
				setNotOwnerPermissions(sc)
				service := sc.service()

				sc.pollAndWait(t, service)

				if n := sc.agentRuns(t); n != 1 {
					t.Errorf("%d agent runs, want 1", n)
				}
				if strings.Contains(sc.logs.String(), "is not of cumin-core or of an Owner") {
					t.Errorf("a label that counts was logged as one of another account:\n%s", sc.logs.String())
				}
			})
		}
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
// another account added waits for the Owner, so it is not an issue that
// cumin moves on without the Owner. A label that was not read counts.
func TestStatusLabel_ALabelOfAnotherAccountDoesNotMoveOnWithoutTheOwner(t *testing.T) {
	for _, label := range []string{workflow.LabelPlanning, workflow.LabelAccepting} {
		requirement := workflow.RequirementIssue{Number: 6, Labels: []string{"cumin/type/requirement", label}}
		snapshot := func(read, counts bool) workflow.Snapshot {
			requirement.StatusRead, requirement.StatusCounts = read, counts
			return workflow.Snapshot{RequirementIssues: []workflow.RequirementIssue{requirement}}
		}
		if snapshot(true, false).MovesWithoutOwner() {
			t.Errorf("%s of another account moves on without the Owner, want not", label)
		}
		if !snapshot(true, true).MovesWithoutOwner() {
			t.Errorf("%s of cumin-core or an Owner does not move on without the Owner", label)
		}
		if !snapshot(false, false).MovesWithoutOwner() {
			t.Errorf("%s whose account was not read does not move on without the Owner", label)
		}
	}
}
