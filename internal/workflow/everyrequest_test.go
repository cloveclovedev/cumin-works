package workflow_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/state"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
	"github.com/cloveclovedev/cumin-works/internal/workflow"
)

// This file proves "stop agent starts" of issue-states.md and the stop
// after the current runs for every kind of request to an agent: the table
// of requests of quota.md, one row for each call of permitStart. While
// cumin stops after the current runs, and at a quota limit, no agent
// starts, no label changes, and nothing is counted.

// everyRequest is one kind of request to an agent.
type everyRequest struct {
	// request names the request in plain words.
	request string
	// logged is the name of the request in the log line of the check before
	// the start (permitStart).
	logged string
	// call is the call of permitStart that the row reaches: the file, the
	// function, and the name of the request in the code.
	call string
	// afterRun says that the request follows the end of an agent run. The
	// row then reaches it through the real end of that run in the fake CLI.
	afterRun bool
	// needsReadyOfOwner says that the decision gives the request only for
	// a cumin/status/ready of the Owner. While cumin stops after the
	// current runs, the poll does not read who added that label, so the
	// request waits before the check before the start, with no log line.
	needsReadyOfOwner bool
	// scene builds the state from which cumin decides the request. holds
	// makes the agent run wait until the test releases it.
	scene func(t *testing.T, holds bool) *scene
	// prepare gives the service what the scene needs in the state file.
	prepare func(t *testing.T, sc *scene, service *workflow.Service)
}

// session stores the Implementer session of #10, as an earlier run left it.
func session(t *testing.T, _ *scene, service *workflow.Service) {
	t.Helper()
	if err := service.State.Set("example-org/example-repo", 10, state.Issue{SessionID: "implementer-session"}); err != nil {
		t.Fatal(err)
	}
}

// everyRequestRows are the requests of the table of requests of quota.md.
var everyRequestRows = []everyRequest{
	{
		request: "split", logged: "split", needsReadyOfOwner: true,
		call:  "plan.go: plan: split",
		scene: func(t *testing.T, _ bool) *scene { return newPlanScene(t) },
	},
	{
		request: "split again", logged: "split again", afterRun: true,
		call: "plan.go: runSplit: split again",
		// The Planner run ends with no sub-issue of #6.
		scene: func(t *testing.T, holds bool) *scene {
			sc := newScene(t, cliOptions{fixture: "planner-done.jsonl", holds: holds})
			sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 6, Labels: []string{githubtest.RequirementLabel, "cumin/status/ready"}})
			sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Title: subIssueTitle})
			return sc
		},
	},
	{
		request: "acceptance check at a poll", logged: "acceptance check",
		call: "plan.go: checkAcceptance: acceptance check",
		scene: func(t *testing.T, _ bool) *scene {
			sc, _ := newAcceptanceScene(t, cliOptions{fixture: "planner-done.jsonl"})
			return sc
		},
	},
	{
		request: "acceptance check at the end of the split run", logged: "acceptance check", afterRun: true,
		call: "plan.go: runSplit: acceptance check",
		// The split run ends, and every sub-issue of #6 is closed.
		scene: func(t *testing.T, holds bool) *scene {
			sc := newScene(t, cliOptions{fixture: "planner-done.jsonl", holds: holds})
			sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 6, Labels: []string{githubtest.RequirementLabel, "cumin/status/ready"}})
			sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Closed: true, ClosedAt: sceneNow.Add(-time.Hour), Labels: []string{"risk/low"}})
			return sc
		},
	},
	{
		request: "acceptance check again", logged: "acceptance check again", afterRun: true,
		call: "plan.go: runAcceptanceCheck: acceptance check again",
		// The acceptance check run ends with no comment of the Planner.
		scene: func(t *testing.T, holds bool) *scene {
			sc, _ := newAcceptanceScene(t, cliOptions{fixture: "planner-done.jsonl", holds: holds})
			return sc
		},
	},
	{
		request: "claim", logged: "claim", needsReadyOfOwner: true,
		call:  "service.go: claim: claim",
		scene: func(t *testing.T, _ bool) *scene { return newScene(t) },
	},
	{
		request: "implementation again at the end of the run", logged: "implementation again", afterRun: true,
		call: "service.go: runImplementer: implementation again",
		// The Implementer run ends with no pull request.
		scene: func(t *testing.T, holds bool) *scene { return newScene(t, cliOptions{holds: holds}) },
	},
	{
		request: "implementation again at a poll", logged: "implementation again",
		call:  "service.go: requestImplementationAgain: implementation again",
		scene: func(t *testing.T, _ bool) *scene { return implementingWithoutAnAgent(t, implementingByCumin()) },
	},
	{
		request: "check fix", logged: "check fix",
		call:  "service.go: fixChecks: check fix",
		scene: func(t *testing.T, _ bool) *scene { return newScene(t) },
		prepare: func(t *testing.T, sc *scene, service *workflow.Service) {
			sc.failingCheck(t, service, 1)
		},
	},
	{
		request: "review fix", logged: "review fix", afterRun: true,
		call: "review.go: applyReviewEnd: review fix",
		// The Reviewer run ends with REQUEST_CHANGES.
		scene: func(t *testing.T, holds bool) *scene {
			return newScene(t, cliOptions{reviews: []string{"REQUEST_CHANGES", "NONE"}, holds: holds})
		},
		prepare: func(t *testing.T, sc *scene, service *workflow.Service) {
			sc.reviewing(t, service, state.Issue{SessionID: "implementer-session"})
		},
	},
	{
		request: "conflict resolution from cumin/status/merging", logged: "conflict resolution",
		call:    "merge.go: resolveConflict: conflict resolution",
		scene:   func(t *testing.T, _ bool) *scene { return knownConflictScene(t) },
		prepare: session,
	},
	{
		request: "conflict resolution from cumin/status/checking", logged: "conflict resolution",
		call: "merge.go: resolveConflictAtPoll: conflict resolution",
		scene: func(t *testing.T, _ bool) *scene {
			sc := conflictingBeforeChecks(t, cliOptions{}, "CONFLICTING")
			sc.repo.Issues[10].LabelEvents = []githubtest.LabelEvent{readyBy(theOwner, 30)}
			sc.fake.SetPermission(theOwner, "admin", "User")
			return sc
		},
		prepare: session,
	},
	{
		request: "conflict resolution from cumin/status/awaiting-merge-decision", logged: "conflict resolution",
		call: "merge.go: resolveConflictAtPoll: conflict resolution",
		scene: func(t *testing.T, _ bool) *scene {
			sc := awaitingOwner(t)
			sc.review(implementerSlug, true, "APPROVED", sc.remoteHead, 30)
			sc.fake.SetPullRequestMergeable(sc.repo, 21, "CONFLICTING")
			sc.repo.Issues[10].LabelEvents = append([]githubtest.LabelEvent{readyBy(theOwner, 60)}, sc.repo.Issues[10].LabelEvents...)
			return sc
		},
		prepare: session,
	},
	{
		request: "fix of the review of the Owner", logged: "owner review fix",
		call: "merge.go: fixOwnerReview: owner review fix",
		scene: func(t *testing.T, _ bool) *scene {
			sc := awaitingOwner(t)
			sc.review(implementerSlug, true, "APPROVED", sc.remoteHead, 30)
			sc.review(theOwner, false, "CHANGES_REQUESTED", sc.remoteHead, 5)
			sc.repo.Issues[10].LabelEvents = append([]githubtest.LabelEvent{readyBy(theOwner, 60)}, sc.repo.Issues[10].LabelEvents...)
			return sc
		},
		prepare: session,
	},
	{
		request: "review", logged: "review",
		call:  "review.go: startReview: review",
		scene: func(t *testing.T, _ bool) *scene { return approved(t, "risk/low") },
	},
	{
		request: "review again", logged: "review again", afterRun: true,
		call: "review.go: applyReviewEnd: a variable",
		// The Reviewer run ends with no review.
		scene: func(t *testing.T, holds bool) *scene {
			return newScene(t, cliOptions{reviews: []string{"NONE", "APPROVE"}, holds: holds})
		},
		prepare: func(t *testing.T, sc *scene, service *workflow.Service) {
			sc.reviewing(t, service, state.Issue{})
		},
	},
	{
		request: "cause", logged: "cause", afterRun: true,
		call: "review.go: applyReviewEnd: a variable",
		// The Reviewer run ends with REQUEST_CHANGES at the limit of rounds.
		scene: func(t *testing.T, holds bool) *scene {
			return newScene(t, cliOptions{reviews: []string{"REQUEST_CHANGES", "NONE"}, comments: []string{"NONE", "DECISION"}, holds: holds})
		},
		prepare: func(t *testing.T, sc *scene, service *workflow.Service) {
			sc.reviewing(t, service, state.Issue{SessionID: "implementer-session"})
			sc.atTheLimit(t)
		},
	},
}

// factsOfARequest are what a request that waits must leave as it is: the
// labels of the requirement issue #6 and of the issue #10, the number of
// label changes of the two issues, and the counts of the state file.
type factsOfARequest struct {
	requirementLabels, labels []string
	labelChanges              int
	requirementCounts, counts state.Issue
}

// factsOf reads the facts of the scene.
func factsOf(sc *scene, service *workflow.Service) factsOfARequest {
	counts := func(number int) state.Issue {
		stored := service.State.Issue("example-org/example-repo", number)
		// The end of a run stores its session and the reviewed head. They
		// are no counts.
		stored.SessionID, stored.ReviewerSessionID, stored.ReviewHead = "", "", ""
		return stored
	}
	return factsOfARequest{
		requirementLabels: slices.Clone(sc.fake.Issue(sc.repo, 6).Labels),
		labels:            slices.Clone(sc.fake.Issue(sc.repo, 10).Labels),
		labelChanges:      sc.fake.CountRequests(http.MethodPut, putRequirementLabelsPath) + sc.fake.CountRequests(http.MethodPut, putLabelsPath),
		requirementCounts: counts(6),
		counts:            counts(10),
	}
}

// assertNoStartOfTheRequest checks that the request of the row waited: no
// agent started after before was read, the labels and the counts stayed,
// and the log names the request beside one of the messages of the check
// before the start. No message means that the request waits before that
// check.
func assertNoStartOfTheRequest(t *testing.T, sc *scene, service *workflow.Service, row everyRequest, before factsOfARequest, messages ...string) {
	t.Helper()
	runs := 0
	if row.afterRun {
		runs = 1
	}
	if n := sc.agentRuns(t); n != runs {
		t.Errorf("%d agent runs, want %d: the request %q starts no agent", n, runs, row.request)
	}
	after := factsOf(sc, service)
	if !slices.Equal(after.requirementLabels, before.requirementLabels) {
		t.Errorf("labels of #6 = %v, want %v untouched", after.requirementLabels, before.requirementLabels)
	}
	if !slices.Equal(after.labels, before.labels) {
		t.Errorf("labels of #10 = %v, want %v untouched", after.labels, before.labels)
	}
	if after.labelChanges != before.labelChanges {
		t.Errorf("%d label changes, want %d: no label changes for a start that waits", after.labelChanges, before.labelChanges)
	}
	if after.requirementCounts != before.requirementCounts {
		t.Errorf("counts of #6 in the state file = %+v, want %+v", after.requirementCounts, before.requirementCounts)
	}
	if after.counts != before.counts {
		t.Errorf("counts of #10 in the state file = %+v, want %+v", after.counts, before.counts)
	}
	logs := sc.logs.String()
	waited := len(messages) == 0
	for line := range strings.SplitSeq(logs, "\n") {
		if !strings.Contains(line, `"request":"`+row.logged+`"`) {
			continue
		}
		if slices.ContainsFunc(messages, func(message string) bool { return strings.Contains(line, message) }) {
			waited = true
			break
		}
	}
	if !waited {
		t.Errorf("the log does not say that the request %q waits; the row did not reach its request:\n%s", row.logged, logs)
	}
}

// waitForTheRuns waits for the agent runs of the service. A run that
// started against the rule holds, so the guard ends the test.
func waitForTheRuns(t *testing.T, sc *scene, service *workflow.Service) {
	t.Helper()
	ended := make(chan struct{})
	go func() {
		service.Wait()
		close(ended)
	}()
	select {
	case <-ended:
	case <-time.After(hangGuard):
		t.Fatalf("an agent run did not end:\n%s", sc.logs.String())
	}
}

// Every kind of request to an agent waits while cumin stops after the
// current runs, and at a quota limit: no agent starts, the labels stay, and
// the counts of the state file stay. A request that follows the end of a
// run is reached through the end of that run, so that run is the only one.
func TestEveryRequestStartsNoAgentWhileCuminStopsOrAtAQuotaLimit(t *testing.T) {
	for _, row := range everyRequestRows {
		t.Run(row.request+"/while cumin stops after the current runs", func(t *testing.T) {
			sc := row.scene(t, row.afterRun)
			service := sc.service()
			withState(t, service)
			if row.prepare != nil {
				row.prepare(t, sc, service)
			}
			var before factsOfARequest
			if row.afterRun {
				path, returned := stopAfterRunsScene(t, sc, service)
				waitForAgentRun(t, sc)
				requestStop(t, path)
				waitForLog(t, sc, tookStopRequestLog)
				before = factsOf(sc, service)
				sc.release(t)
				select {
				case err := <-returned:
					if err != nil {
						t.Fatalf("Run returned %v, want nil", err)
					}
				case <-time.After(hangGuard):
					t.Fatalf("Run did not return after the run ended:\n%s", sc.logs.String())
				}
			} else {
				before = factsOf(sc, service)
				runWithAStopRequestOfTheStart(t, service)
			}

			if row.needsReadyOfOwner {
				assertNoStartOfTheRequest(t, sc, service, row, before)
				return
			}
			assertNoStartOfTheRequest(t, sc, service, row, before, heldBackLog)
		})
		t.Run(row.request+"/at a quota limit", func(t *testing.T) {
			sc := row.scene(t, row.afterRun)
			if row.afterRun {
				// The run starts below the limit, and the usage that it
				// reports reaches the limit.
				sc.limitReachedByARun(t)
			} else {
				sc.atAQuotaLimit(t)
			}
			service := sc.service()
			withState(t, service)
			if row.prepare != nil {
				row.prepare(t, sc, service)
			}
			var before factsOfARequest
			if row.afterRun {
				if err := service.Poll(context.Background()); err != nil {
					t.Fatalf("Poll: %v", err)
				}
				waitForAgentRun(t, sc)
				before = factsOf(sc, service)
				sc.release(t)
				waitForTheRuns(t, sc, service)
			} else {
				before = factsOf(sc, service)
			}
			// The polls after the first decide the same request again.
			for range 2 {
				if err := service.Poll(context.Background()); err != nil {
					t.Fatalf("Poll: %v", err)
				}
				waitForTheRuns(t, sc, service)
			}

			// The first check at the limit logs the limit. A check after
			// it, and a check after the end of a run that reached the
			// limit, logs the wait for the next try time.
			assertNoStartOfTheRequest(t, sc, service, row, before,
				`"msg":"stop agent starts: the quota limit is reached"`,
				`"msg":"agent starts stopped: the request waits for the next try time"`)
		})
	}
}

// The table has a row for each call of the check before the start
// (permitStart) in the package. A new request fails here until it has a
// row.
func TestEveryRequestHasARowForEachCallOfTheCheckBeforeTheStart(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if permit, ok := call.Fun.(*ast.SelectorExpr); !ok || permit.Sel.Name != "permitStart" || len(call.Args) < 3 {
					return true
				}
				// The third argument names the request: a string, or a
				// variable that holds one of two names.
				request := "a variable"
				if literal, ok := call.Args[2].(*ast.BasicLit); ok {
					request = strings.Trim(literal.Value, `"`)
				}
				calls = append(calls, name+": "+fn.Name.Name+": "+request)
				return true
			})
		}
	}
	var rows []string
	for _, row := range everyRequestRows {
		if !slices.Contains(rows, row.call) {
			rows = append(rows, row.call)
		}
	}
	slices.Sort(calls)
	slices.Sort(rows)
	if !slices.Equal(calls, rows) {
		t.Errorf("calls of permitStart = %q,\nthe rows of the table name %q", calls, rows)
	}
}
