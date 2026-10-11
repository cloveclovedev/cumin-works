package workflow_test

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
)

// This file proves "stop agent starts" and "resume agent starts" of
// issue-states.md for the tool checks of a repository (cumin-core.md, tests
// 40 and 42 of the top-level requirements): while a check fails, no agent
// starts for that repository, and everything else goes on.

// withAToolCheck gives the repository of the scene one tool check that
// passes only while a file exists, and returns the path of that file. The
// check fails until the test writes the file.
func (sc *scene) withAToolCheck(t *testing.T) string {
	t.Helper()
	tool := filepath.Join(t.TempDir(), "tool")
	sc.fake.SetFile(sc.repo, ".cumin/config.toml", githubtest.File{
		Content: "[[tool_checks]]\ncommand = [\"test\", \"-e\", \"" + tool + "\"]\n",
	})
	return tool
}

// While a tool check of the repository fails, a ready issue gets no label
// change and no agent, and each refused start gives one log line that
// starts with the name of the action and names the failed check. When the
// check passes again, the next poll starts the agent with no other step.
func TestAFailedToolCheckStartsNoAgentUntilTheCheckPassesAgain(t *testing.T) {
	sc := newScene(t)
	// The pull request that the Implementer opens, so that the run ends on
	// the success path of the verification, with no second request.
	sc.addUnlinkedPullRequest(21, sc.remoteHead)
	tool := sc.withAToolCheck(t)
	service := sc.service()

	sc.pollTimes(t, service, 3)

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs while the tool check fails, want 0", n)
	}
	if n := sc.quotaRuns(t); n != 0 {
		t.Errorf("%d minimal runs while the tool check fails, want 0", n)
	}
	if n := sc.fake.CountRequests(http.MethodPut, putLabelsPath); n != 0 {
		t.Errorf("%d label changes of #10 while the tool check fails, want none", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"cumin/status/ready", "risk/low"}) {
		t.Errorf("labels of #10 = %v, want cumin/status/ready and risk/low", got)
	}
	refused := 0
	for _, line := range strings.Split(sc.logs.String(), "\n") {
		if !strings.Contains(line, `"msg":"stop agent starts: a tool check of the repository fails; the request waits`) {
			continue
		}
		refused++
		if !strings.Contains(line, `"failed_checks":["test -e `+tool+`"]`) || !strings.Contains(line, `"request":"claim"`) {
			t.Errorf("the log line of the refused start does not name the request and the failed check: %s", line)
		}
	}
	if refused != 3 {
		t.Errorf("%d log lines of a refused start over three polls, want 3", refused)
	}

	if err := os.WriteFile(tool, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs after the tool check passed again, want 1", n)
	}
	if n := sc.fake.CountRequests(http.MethodPut, putLabelsPath); n == 0 {
		t.Error("no label change of #10 after the tool check passed again")
	}
	if !strings.Contains(sc.logs.String(), `"msg":"resume agent starts: every tool check of the repository passes"`) {
		t.Error("no log line of resume agent starts")
	}
}

// In the poll where a tool check of a repository fails, cumin still merges
// and changes labels for that repository, and another repository starts
// its agent.
func TestAFailedToolCheckStopsOnlyTheAgentStartsOfItsRepository(t *testing.T) {
	sc := mergingScene(t, "risk/low")
	sc.withAToolCheck(t)
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 11, Parent: 6, Title: "Add the logout screen", Labels: []string{"cumin/status/ready", "risk/low"}})
	other := sc.fake.AddRepository("example-org", "other-repo")
	sc.fake.AddIssue(other, &githubtest.Issue{Number: 1, Labels: []string{githubtest.RequirementLabel, "cumin/status/implementing"}})
	sc.fake.AddIssue(other, &githubtest.Issue{Number: 2, Parent: 1, Title: subIssueTitle, Labels: []string{"cumin/status/ready", "risk/low"}})
	service := sc.service()
	service.Targets = append(service.Targets, target(sc, "other-repo"))

	sc.pollTimes(t, service, 2)

	if !sc.repo.PullRequests[21].Merged {
		t.Error("pull request #21 is not merged while the tool check fails")
	}
	if issue := sc.fake.Issue(sc.repo, 10); !issue.Closed || issue.StateReason != "completed" {
		t.Errorf("issue #10: closed %v, reason %q; want closed as completed", issue.Closed, issue.StateReason)
	}
	if got := sc.fake.Issue(sc.repo, 11).Labels; !slices.Equal(got, []string{"cumin/status/ready", "risk/low"}) {
		t.Errorf("labels of #11 = %v, want cumin/status/ready and risk/low", got)
	}
	if n := sc.fake.CountRequests(http.MethodPut, "/repos/example-org/example-repo/issues/11/labels"); n != 0 {
		t.Errorf("%d label changes of #11 while the tool check fails, want none", n)
	}
	if slices.Contains(sc.fake.Issue(other, 2).Labels, "cumin/status/ready") {
		t.Error("issue #2 of the other repository is still ready, want its claim")
	}
	// Every agent run is one of the other repository: #11 kept its labels.
	// The fake Implementer opens no pull request there, so its request may
	// be sent a second time at the end of the run.
	if n := sc.agentRuns(t); n == 0 {
		t.Error("no agent run, want the one of the other repository")
	}
}

// A tool check that begins to fail while an agent runs stops the request
// at the end of that run too: the Implementer ended without a pull request,
// and "request the implementation again" gets no permit.
func TestAFailedToolCheckStartsNoAgentAtTheEndOfARun(t *testing.T) {
	sc := newScene(t, cliOptions{holds: true})
	tool := sc.withAToolCheck(t)
	if err := os.WriteFile(tool, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	service := sc.service()
	if err := service.Poll(t.Context()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	waitForAgentRun(t, sc)

	// The tool stops to work while the agent runs, and a poll sees it.
	if err := os.Remove(tool); err != nil {
		t.Fatal(err)
	}
	if err := service.Poll(t.Context()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	sc.release(t)
	service.Wait()

	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1: no second request while the tool check fails", n)
	}
	if !strings.Contains(sc.logs.String(), `"request":"implementation again","failed_checks":["test -e `+tool+`"]`) {
		t.Errorf("no log line of the refused second request with the failed check:\n%s", sc.logs.String())
	}
}
