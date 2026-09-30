package workflow_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// q4Messages returns the notifications of Q4.
func (sc *scene) q4Messages() []string {
	var q4 []string
	for _, m := range sc.webhook.messagesSent() {
		if strings.HasPrefix(m, "cumin: Q4: ") {
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
	if n := sc.agentRuns(t); n != 1 {
		t.Fatalf("%d agent runs, want 1", n)
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
	sc.fake.FailNext(http.MethodPost, "/graphql", http.StatusBadGateway)
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
