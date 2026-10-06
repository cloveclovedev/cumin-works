package workflow_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/state"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
	"github.com/cloveclovedev/cumin-works/internal/workflow"
)

// stopAfterRunsScene runs the service of the scene with a short poll interval and a
// stop request file, and returns the file and the channel of the result of
// Run.
func stopAfterRunsScene(t *testing.T, sc *scene, service *workflow.Service) (string, <-chan error) {
	t.Helper()
	service.PollInterval = 10 * time.Millisecond
	service.StopRequestPath = filepath.Join(t.TempDir(), state.StopRequestFileName)
	returned := make(chan error, 1)
	go func() { returned <- service.Run(context.Background()) }()
	return service.StopRequestPath, returned
}

// requestStop writes the stop request, as `cumin stop
// --after-current-runs` does.
func requestStop(t *testing.T, path string) {
	t.Helper()
	// The real clock: cumin compares the time of the request with the real
	// start time of its process, to drop a request of before the start. A
	// fixed time would be older than that start, and cumin would drop it.
	if err := state.WriteStopRequest(path, state.StopRequest{RequestedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

// The log lines of a stop after the current runs. The tests wait for
// them: a count of the reads of the fake GitHub does not say which poll
// sent a read, since one poll sends more than one.
const (
	tookStopRequestLog = `"msg":"stop after the current runs: no new work starts; cumin exits when the agent runs have ended"`
	heldBackLog        = `"msg":"stop after the current runs: the request waits for the next start of cumin"`
)

// stopRequestExists reports whether a stop request is there.
func stopRequestExists(t *testing.T, path string) bool {
	t.Helper()
	_, found, err := state.ReadStopRequest(path)
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// A stop request with one running agent: cumin starts nothing new, lets the run
// end, applies the state that follows it, and then exits with nil, so that
// the command exits with 0.
func TestStopAfterRuns_LetsTheRunEndAppliesItsNextStateAndStartsNothingNew(t *testing.T) {
	sc := newScene(t, cliOptions{holds: true})
	sc.repo.DefaultBranch = "main"
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	service := sc.service()
	// Room for a second issue, so that only the stop request holds it back.
	service.Settings.MaxIssuesInProgress = 2
	path, returned := stopAfterRunsScene(t, sc, service)
	waitForAgentRun(t, sc)

	requestStop(t, path)
	waitForLog(t, sc, tookStopRequestLog)
	// A second issue becomes ready while cumin stops after its runs. The
	// line of a poll that read two issues tells that a poll read it.
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 11, Parent: 6, Title: "Add the logout screen", Labels: []string{"cumin/status/ready", "risk/low"}})
	waitForLog(t, sc, `"msg":"poll","repository":"example-org/example-repo","requirement_issues":1,"required_checks":0,"issues_with_pull_requests_read":2`)

	select {
	case err := <-returned:
		t.Fatalf("Run returned %v while the agent run was going on:\n%s", err, sc.logs.String())
	default:
	}
	if got := sc.fake.Issue(sc.repo, 11).Labels; !slices.Contains(got, "cumin/status/ready") {
		t.Errorf("labels of #11 = %v, want cumin/status/ready untouched", got)
	}

	sc.release(t)
	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("Run returned %v, want nil so that the command exits with 0", err)
		}
	case <-time.After(hangGuard):
		t.Fatalf("Run did not return after the run ended:\n%s", sc.logs.String())
	}

	// The end of the run was verified (verify done), and the last poll gave
	// the pull request the labels of the issue.
	want := []string{"risk/low", workflow.LabelChecking}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, want) {
		t.Errorf("labels of #10 = %v, want %v", got, want)
	}
	if got := sc.fake.PullRequestLabels(sc.repo, 21); !slices.Contains(got, workflow.LabelChecking) {
		t.Errorf("labels of the pull request = %v, want cumin/status/checking copied from the issue", got)
	}
	// No required check: without the stop request, the review would start at once.
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1: the stop request starts no review and no second issue", n)
	}
	if got := sc.fake.Issue(sc.repo, 11).Labels; !slices.Contains(got, "cumin/status/ready") {
		t.Errorf("labels of #11 = %v, want cumin/status/ready untouched", got)
	}
	if stopRequestExists(t, path) {
		t.Error("the stop request is still there after the exit")
	}
	logs := sc.logs.String()
	for _, want := range []string{
		tookStopRequestLog,
		`"in_progress":["example-org/example-repo#10"]`,
		heldBackLog,
		`"msg":"I2: verified the pull request"`,
		`"msg":"stopped","reason":"the agent runs have ended after a stop request","in_progress":[]`,
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("the log has no %s:\n%s", want, logs)
		}
	}
	// A stop after the runs is not "nothing to do": the Owner asked for it.
	if messages := sc.webhook.messagesSent(); len(messages) != 0 {
		t.Errorf("notifications = %v, want none", messages)
	}
}

// A stop request with no running agent exits at the next poll.
func TestStopAfterRuns_WithNoRunningAgentExitsAtOnce(t *testing.T) {
	sc := newScene(t)
	// Nothing is ready, so no agent runs when the request comes.
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Labels: []string{"risk/low"}})
	service := sc.service()
	path, returned := stopAfterRunsScene(t, sc, service)
	// The first read shows that cumin run has started: the request that
	// follows is not one of before the start.
	if !sc.fake.WaitForRequests(http.MethodPost, "/graphql", 1, hangGuard) {
		t.Fatalf("no poll ran:\n%s", sc.logs.String())
	}

	requestStop(t, path)
	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("Run returned %v, want nil", err)
		}
	case <-time.After(hangGuard):
		t.Fatalf("Run did not return:\n%s", sc.logs.String())
	}
	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none", n)
	}
	if stopRequestExists(t, path) {
		t.Error("the stop request is still there after the exit")
	}
	if !strings.Contains(sc.logs.String(), `"msg":"stopped","reason":"the agent runs have ended after a stop request"`) {
		t.Errorf("the log has no stop line of the stop request:\n%s", sc.logs.String())
	}
}

// A stop request survives nothing: a request from before the start is removed, and
// cumin run works as usual.
func TestStopAfterRuns_ARequestFromBeforeTheStartIsDropped(t *testing.T) {
	// The run sleeps, so that the test sees it start; the cancel ends it.
	sc := newScene(t, cliOptions{sleeps: true})
	service := sc.service()
	service.PollInterval = 10 * time.Millisecond
	service.StopRequestPath = filepath.Join(t.TempDir(), state.StopRequestFileName)
	requestStop(t, service.StopRequestPath)

	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan error, 1)
	go func() { returned <- service.Run(ctx) }()
	waitForAgentRun(t, sc)
	cancel()
	if err := <-returned; err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stopRequestExists(t, service.StopRequestPath) {
		t.Error("the stop request of before the start is still there")
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelImplementing) {
		t.Errorf("labels of #10 = %v, want the claim of a usual start", got)
	}
}

// A stop signal after a stop request stops at once, as without one.
func TestStopAfterRuns_TheStopSignalStillStopsAtOnce(t *testing.T) {
	sc := newScene(t, cliOptions{sleeps: true})
	service := sc.service()
	service.PollInterval = 10 * time.Millisecond
	service.StopGrace = 30 * time.Second
	service.StopRequestPath = filepath.Join(t.TempDir(), state.StopRequestFileName)
	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan error, 1)
	go func() { returned <- service.Run(ctx) }()
	waitForAgentRun(t, sc)
	requestStop(t, service.StopRequestPath)
	// The signal comes after cumin took the request. A request that cumin
	// has not taken stays for the next start, which removes it.
	waitForLog(t, sc, tookStopRequestLog)

	cancel()
	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(hangGuard):
		t.Fatalf("Run did not return after the stop signal:\n%s", sc.logs.String())
	}
	logs := sc.logs.String()
	if !strings.Contains(logs, `"msg":"stopped","reason":"context canceled","in_progress":["example-org/example-repo#10"]`) {
		t.Errorf("the log has no stop line of the signal that names #10:\n%s", logs)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelImplementing) {
		t.Errorf("labels of #10 = %v, want cumin/status/implementing", got)
	}
	if stopRequestExists(t, service.StopRequestPath) {
		t.Error("the stop request is still there after the stop")
	}
}

// A request of before the start that cannot be removed does not stop the
// new process either: every start would otherwise exit at once, and launchd
// leaves a process that exits with 0 stopped.
func TestStopAfterRuns_ARequestThatCannotBeRemovedAtTheStartIsPassedOver(t *testing.T) {
	sc := newScene(t, cliOptions{sleeps: true})
	service := sc.service()
	service.PollInterval = 10 * time.Millisecond
	dir := t.TempDir()
	service.StopRequestPath = filepath.Join(dir, state.StopRequestFileName)
	requestStop(t, service.StopRequestPath)
	// The directory cannot be written, so the file cannot be removed.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan error, 1)
	go func() { returned <- service.Run(ctx) }()
	// The claim and the agent run show that the first poll passed over the
	// request: a poll that takes a request starts no new work.
	waitForAgentRun(t, sc)
	select {
	case err := <-returned:
		t.Fatalf("Run returned %v on the request of before the start:\n%s", err, sc.logs.String())
	default:
	}
	cancel()
	if err := <-returned; err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !stopRequestExists(t, service.StopRequestPath) {
		t.Fatal("the request was removed; the test did not reach the case")
	}
	logs := sc.logs.String()
	if !strings.Contains(logs, `"msg":"the stop request of an earlier start was not removed"`) {
		t.Errorf("the log has no warning for the request that stays:\n%s", logs)
	}
	if strings.Contains(logs, tookStopRequestLog) {
		t.Errorf("cumin took the request of before the start:\n%s", logs)
	}
}

// A request that is not older than the start is for the process that is
// starting: the start keeps it, and the first poll takes it.
func TestStopAfterRuns_ARequestOfTheStartIsKept(t *testing.T) {
	sc := newScene(t)
	service := sc.service()
	service.PollInterval = 10 * time.Millisecond
	service.StopRequestPath = filepath.Join(t.TempDir(), state.StopRequestFileName)
	// The time of a request that comes just after the start. The real
	// clock: cumin compares this time with the real start time of its
	// process, and keeps only a request that is not older than that start.
	if err := state.WriteStopRequest(service.StopRequestPath, state.StopRequest{RequestedAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}

	if err := service.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none: the first poll takes it", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, "cumin/status/ready") {
		t.Errorf("labels of #10 = %v, want cumin/status/ready untouched", got)
	}
	if stopRequestExists(t, service.StopRequestPath) {
		t.Error("the stop request is still there after the exit")
	}
}

// runWithAStopRequestOfTheStart runs the service to its end with a stop
// request that the first poll takes
// (TestStopAfterRuns_ARequestOfTheStartIsKept): every poll of the run
// decides while cumin stops after the current runs.
func runWithAStopRequestOfTheStart(t *testing.T, service *workflow.Service) {
	t.Helper()
	service.PollInterval = 10 * time.Millisecond
	service.StopRequestPath = filepath.Join(t.TempDir(), state.StopRequestFileName)
	if err := state.WriteStopRequest(service.StopRequestPath, state.StopRequest{RequestedAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := service.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// While cumin stops after the current runs, a Planner run that ends with
// no acceptance check comment starts no second request: no agent starts,
// the request is not counted, and the issue keeps cumin/status/accepting.
// The last poll decides the same second request, and starts nothing either.
func TestStopAfterRuns_APlannerRunWithNoAcceptanceCheckCommentStartsNoSecondRequest(t *testing.T) {
	sc, _ := newAcceptanceScene(t, cliOptions{fixture: "planner-done.jsonl", holds: true})
	service := sc.service()
	path, returned := stopAfterRunsScene(t, sc, service)
	waitForAgentRun(t, sc)
	requestStop(t, path)
	waitForLog(t, sc, tookStopRequestLog)
	changes := sc.labelChanges()

	sc.release(t)
	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("Run returned %v, want nil", err)
		}
	case <-time.After(hangGuard):
		t.Fatalf("Run did not return after the run ended:\n%s", sc.logs.String())
	}

	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1: the second request of the acceptance check waits for the next start", n)
	}
	want := []string{githubtest.RequirementLabel, "cumin/status/accepting"}
	if got := requirementLabels(t, sc); !slices.Equal(got, want) {
		t.Errorf("labels of #6 = %v, want %v", got, want)
	}
	if n := sc.labelChanges(); n != changes {
		t.Errorf("%d label changes, want %d: no label changes for a start that waits", n, changes)
	}
	if got := service.State.Issue("example-org/example-repo", 6).AcceptanceRequests; got != 0 {
		t.Errorf("the count of the second request = %d, want 0", got)
	}
	logs := sc.logs.String()
	for _, want := range []string{heldBackLog, `"request":"acceptance check again"`} {
		if !strings.Contains(logs, want) {
			t.Errorf("the log has no %s:\n%s", want, logs)
		}
	}
}

// While cumin stops after the current runs, the steps that start no agent
// still run. A merged pull request closes its issue.
func TestStopAfterRuns_AMergedPullRequestStillClosesItsIssue(t *testing.T) {
	sc := mergingScene(t, "risk/low")
	pr := sc.repo.PullRequests[21]
	pr.Closed, pr.Merged = true, true
	service := sc.service()

	runWithAStopRequestOfTheStart(t, service)

	assertMergedAndClosedOnce(t, sc, service, 0)
}

// While cumin stops after the current runs, the steps that start no agent
// still run. A failed required check at the limit of check fix requests
// stops the issue for the Owner, with the label and the comment, and no
// Implementer starts.
func TestStopAfterRuns_AFailedCheckAtTheLimitStillStopsForTheOwner(t *testing.T) {
	sc := newScene(t)
	service := sc.service()
	// 3 is max_check_fix_requests of the scene.
	sc.failingCheck(t, service, 3)

	runWithAStopRequestOfTheStart(t, service)

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelAwaitingDecision) {
		t.Errorf("labels of #10 = %v, want %s", got, workflow.LabelAwaitingDecision)
	}
	if n := len(sc.fake.Comments(sc.repo, 10)); n != 1 {
		t.Errorf("%d comments on #10, want 1 with the reason of the stop", n)
	}
}

// While cumin stops after the current runs, a failed required check below
// the limit starts no check fix: no label changes, and the count of check
// fix requests stays.
func TestStopAfterRuns_AFailedCheckStartsNoCheckFixAndCountsNothing(t *testing.T) {
	sc := newScene(t)
	service := sc.service()
	sc.failingCheck(t, service, 1)
	before := slices.Clone(sc.fake.Issue(sc.repo, 10).Labels)

	runWithAStopRequestOfTheStart(t, service)

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, before) {
		t.Errorf("labels of #10 = %v, want %v untouched", got, before)
	}
	if got := service.State.Issue("example-org/example-repo", 10).CheckFixRequests; got != 1 {
		t.Errorf("check fix requests = %d, want still 1", got)
	}
	if logs := sc.logs.String(); !strings.Contains(logs, heldBackLog) || !strings.Contains(logs, `"request":"check fix"`) {
		t.Errorf("the log does not say that the check fix waits:\n%s", logs)
	}
}

// Every start of an agent passes the one check: Agents.Start has one caller
// in the package, the function startAgent, which takes the permit of the
// start. A new request that reaches Agents.Start by itself, or that builds
// its own permit, fails here.
func TestAgentsStartHasOneCallerInTheWorkflowPackage(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var callers, waysAround []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		found, err := findStartsOfAnAgent(name, string(source))
		if err != nil {
			t.Fatal(err)
		}
		callers = append(callers, found.callers...)
		waysAround = append(waysAround, found.waysAround...)
	}
	if want := []string{"service.go: startAgent"}; !slices.Equal(callers, want) {
		t.Errorf("callers of Agents.Start = %v, want %v: every start of an agent goes through startAgent with a permit", callers, want)
	}
	if len(waysAround) != 0 {
		t.Errorf("ways around the one check before a start:\n%s", strings.Join(waysAround, "\n"))
	}
}

// The check of the single caller finds each way around the one check, and
// reports nothing for the uses of Agents that cannot reach Start.
func TestAgentsStartCheckFindsEachWayAroundTheOneCheck(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		body        string
		wantCallers []string
		wantAround  []string
	}{
		{
			name:        "a direct call in another function",
			body:        "func (s *Service) other(ctx context.Context, r agent.StartRequest) { s.Agents.Start(ctx, r) }",
			wantCallers: []string{"case.go: other"},
		},
		{
			name:       "a call through a local variable",
			body:       "func (s *Service) other(ctx context.Context, r agent.StartRequest) { a := s.Agents; a.Start(ctx, r) }",
			wantAround: []string{"case.go: other: a copy of the field Agents"},
		},
		{
			name:       "a call through a method value",
			body:       "func (s *Service) other(ctx context.Context, r agent.StartRequest) { f := s.Agents.Start; f(ctx, r) }",
			wantAround: []string{"case.go: other: a method value of Agents.Start"},
		},
		{
			name:       "a method value inside startAgent",
			body:       "func (s *Service) startAgent(ctx context.Context, r agent.StartRequest) { f := (s.Agents).Start; f(ctx, r) }",
			wantAround: []string{"case.go: startAgent: a method value of Agents.Start"},
		},
		{
			name:       "the field as an argument",
			body:       "func (s *Service) other() { run(s.Agents) }",
			wantAround: []string{"case.go: other: a copy of the field Agents"},
		},
		{
			name:       "the field in a variable of the package",
			body:       "var agents = service.Agents",
			wantAround: []string{"case.go: package level: a copy of the field Agents"},
		},
		{
			name:       "a granted permit outside PermitStart",
			body:       "func (s *Service) other() StartPermit { return StartPermit{granted: true} }",
			wantAround: []string{"case.go: other: a composite literal of StartPermit"},
		},
		{
			name:       "an empty permit outside PermitStart",
			body:       "func (s *Service) other() StartPermit { return StartPermit{} }",
			wantAround: []string{"case.go: other: a composite literal of StartPermit"},
		},
		{
			name:       "a permit as an element of a literal",
			body:       "var permits = []StartPermit{{granted: true}}",
			wantAround: []string{"case.go: package level: a composite literal of StartPermit"},
		},
		{
			name:       "a permit in a method that has the name PermitStart",
			body:       "func (s *Service) PermitStart() StartPermit { return StartPermit{granted: true} }",
			wantAround: []string{"case.go: PermitStart: a composite literal of StartPermit"},
		},
		{
			name: "the permits of PermitStart",
			body: "func PermitStart(ok bool) (StartPermit, bool) { if !ok { return StartPermit{}, false }; return StartPermit{granted: true}, true }",
		},
		{
			name:        "the call of startAgent",
			body:        "func (s *Service) startAgent(ctx context.Context, r agent.StartRequest) { s.Agents.Start(ctx, r) }",
			wantCallers: []string{"case.go: startAgent"},
		},
		{
			name: "the other methods and the checks for nil",
			body: `func (s *Service) other(ctx context.Context, permit StartPermit) {
	if s.Agents == nil || nil != s.Agents { return }
	s.Agents.ReadQuota(ctx, role)
	login := s.Agents.BotLogin
	s.Agents = nil
	var kept StartPermit
	_, _, _ = login, kept, permit
}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			found, err := findStartsOfAnAgent("case.go", "package workflow\n\n"+c.body+"\n")
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(found.callers, c.wantCallers) {
				t.Errorf("callers of Agents.Start = %v, want %v", found.callers, c.wantCallers)
			}
			if !slices.Equal(found.waysAround, c.wantAround) {
				t.Errorf("ways around the one check = %v, want %v", found.waysAround, c.wantAround)
			}
		})
	}
}

// startsOfAnAgent is what one source file holds of the start of an agent.
type startsOfAnAgent struct {
	// callers are the functions that call Agents.Start directly, as
	// "<file>: <function>". Only startAgent of service.go may be one.
	callers []string
	// waysAround are the uses that reach a start without the one check, as
	// "<file>: <function>: <what>".
	waysAround []string
}

// findStartsOfAnAgent reads the source text of one file of the package. It
// works on the syntax only, so it takes every selector with the name Agents
// as the field of the service. Such a selector may be compared with nil, be
// assigned to, and select a method other than Start. A direct call of Start
// is a caller. Every other use can reach Start outside startAgent: a method
// value of Start, and a copy of the field to a variable or to an argument.
// A composite literal that names StartPermit is a permit that the one check
// did not give, except in the function PermitStart.
func findStartsOfAnAgent(name, source string) (startsOfAnAgent, error) {
	var found startsOfAnAgent
	file, err := parser.ParseFile(token.NewFileSet(), name, source, 0)
	if err != nil {
		return found, err
	}
	for _, decl := range file.Decls {
		function, permitStart := "package level", false
		if fn, ok := decl.(*ast.FuncDecl); ok {
			function = fn.Name.Name
			permitStart = fn.Recv == nil && function == "PermitStart"
		}
		where := name + ": " + function
		// The stack holds the nodes from the declaration to the current
		// node, without the parentheses.
		var stack []ast.Node
		ast.Inspect(decl, func(n ast.Node) bool {
			if n == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			if _, ok := n.(*ast.ParenExpr); ok {
				// Inspect reports the end of this node too.
				stack = append(stack, stack[len(stack)-1])
				return true
			}
			stack = append(stack, n)
			switch node := n.(type) {
			case *ast.CompositeLit:
				if !permitStart && node.Type != nil && namesStartPermit(node.Type) {
					found.waysAround = append(found.waysAround, where+": a composite literal of StartPermit")
				}
			case *ast.SelectorExpr:
				if node.Sel.Name != "Agents" {
					return true
				}
				switch use := useOfAgents(stack); use {
				case "":
				case "call":
					found.callers = append(found.callers, where)
				default:
					found.waysAround = append(found.waysAround, where+": "+use)
				}
			}
			return true
		})
	}
	return found, nil
}

// useOfAgents says what the code does with the selector Agents at the end of
// the stack: "" for a use that cannot reach Start, "call" for a direct call
// of Start, and a description for a way around startAgent.
func useOfAgents(stack []ast.Node) string {
	agents := stack[len(stack)-1]
	if len(stack) < 2 {
		return "a copy of the field Agents"
	}
	switch parent := stack[len(stack)-2].(type) {
	case *ast.SelectorExpr:
		if parent.Sel.Name != "Start" {
			return ""
		}
		if len(stack) >= 3 {
			if call, ok := stack[len(stack)-3].(*ast.CallExpr); ok && ast.Unparen(call.Fun) == parent {
				return "call"
			}
		}
		return "a method value of Agents.Start"
	case *ast.BinaryExpr:
		other := parent.X
		if ast.Unparen(other) == agents {
			other = parent.Y
		}
		if id, ok := ast.Unparen(other).(*ast.Ident); ok && id.Name == "nil" && (parent.Op == token.EQL || parent.Op == token.NEQ) {
			return ""
		}
	case *ast.AssignStmt:
		for _, left := range parent.Lhs {
			if ast.Unparen(left) == agents {
				return ""
			}
		}
	}
	return "a copy of the field Agents"
}

// namesStartPermit reports whether the type of a composite literal names
// StartPermit: the type itself, or the type of its elements.
func namesStartPermit(typ ast.Expr) bool {
	names := false
	ast.Inspect(typ, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == "StartPermit" {
			names = true
		}
		return !names
	})
	return names
}
