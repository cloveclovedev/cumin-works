package workflow_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
)

// q1Messages returns the notifications of Q1.
func (sc *scene) q1Messages() []string {
	var q1 []string
	for _, m := range sc.webhook.messagesSent() {
		if strings.HasPrefix(m, "cumin: Q1: ") {
			q1 = append(q1, m)
		}
	}
	return q1
}

// Core-6 and Q1 (issue-states.md): a 5h usage at the limit of the time
// band stops I1 before the label changes. Each poll reads the usage again,
// and the Owner hears once.
func TestCore06_AFiveHourLimitStopsTheStartAndNotifiesOnce(t *testing.T) {
	sc := newScene(t)
	sc.setQuota(t, 0.90, sceneNow.Add(2*time.Hour), 0.10, sceneNow.Add(time.Hour))
	service := sc.service()
	for range 3 {
		sc.pollAndWait(t, service)
	}

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want 0", n)
	}
	if n := sc.quotaRuns(t); n != 3 {
		t.Errorf("%d minimal runs, want one for each poll", n)
	}
	if n := sc.fake.CountRequests(http.MethodPut, putLabelsPath); n != 0 {
		t.Errorf("%d label changes of #10, want none", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, "cumin/status/ready") {
		t.Errorf("labels of #10 = %v, want cumin/status/ready kept", got)
	}
	q1 := sc.q1Messages()
	if len(q1) != 1 || !strings.Contains(q1[0], "The 5h quota window reached its limit") || !strings.Contains(q1[0], "issue #10") {
		t.Errorf("Q1 notifications = %q, want one about the 5h window", q1)
	}
	logs := sc.logs.String()
	if !strings.Contains(logs, `"msg":"Q1: no start; the quota limit is reached"`) {
		t.Errorf("the log has no Q1 line:\n%s", logs)
	}
}

// Core-6: a time band with a higher threshold lets the same usage start.
func TestCore06_AHigherTimeBandLetsTheStartGoOn(t *testing.T) {
	sc := newScene(t)
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	sc.setQuota(t, 0.90, sceneNow.Add(2*time.Hour), 0.10, sceneNow.Add(time.Hour))
	sc.quota.FiveHour.Bands = bandOverTheWholeDay(95)
	sc.pollAndWait(t, sc.service())

	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1", n)
	}
	if q1 := sc.q1Messages(); len(q1) != 0 {
		t.Errorf("Q1 notifications = %q, want none", q1)
	}
}

// Core-15: the same weekly usage stops a start early in the week and lets
// it go later, by time alone. After the start, a new stop notifies again.
func TestCore15_TheWeeklyPaceStopsEarlyAndResumesLater(t *testing.T) {
	sc := newScene(t)
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	// One day into the week: the pace limit is 85 x 2 / 7, about 24.
	weeklyReset := sceneNow.Add(6 * 24 * time.Hour)
	sc.setQuota(t, 0.10, sceneNow.Add(time.Hour), 0.40, weeklyReset)
	service := sc.service()
	sc.pollAndWait(t, service)
	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("early in the week: %d agent runs, want 0", n)
	}
	q1 := sc.q1Messages()
	if len(q1) != 1 || !strings.Contains(q1[0], "The weekly quota window reached its pace limit") {
		t.Errorf("Q1 notifications = %q, want one about the weekly window", q1)
	}

	// Two days later the pace limit is 85 x 4 / 7, about 49.
	sc.clock.Set(sceneNow.Add(2 * 24 * time.Hour))
	sc.setQuota(t, 0.10, sceneNow.Add(49*time.Hour), 0.40, weeklyReset)
	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("later in the week: %d agent runs, want 1", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, "cumin/status/awaiting-checks") {
		t.Errorf("labels of #10 = %v, want the issue claimed and verified", got)
	}
}

// Core-17: usage that cannot be read stops the start, with one
// notification across polls. A read that succeeds lets the start go on.
func TestCore17_UnreadableUsageStopsTheStartWithOneNotification(t *testing.T) {
	sc := newScene(t)
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	sc.failQuota(t)
	service := sc.service()
	sc.pollAndWait(t, service)
	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want 0", n)
	}
	if n := sc.fake.CountRequests(http.MethodPut, putLabelsPath); n != 0 {
		t.Errorf("%d label changes of #10, want none", n)
	}
	q1 := sc.q1Messages()
	if len(q1) != 1 || !strings.Contains(q1[0], "The quota usage was not read before a start") {
		t.Errorf("Q1 notifications = %q, want one about the unread usage", q1)
	}

	sc.setQuota(t, 0.10, sceneNow.Add(time.Hour), 0.10, sceneNow.Add(time.Hour))
	sc.pollAndWait(t, service)
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("after a read: %d agent runs, want 1", n)
	}
}

// Q1 at the end of a run: the usage that the run reports tells the Owner
// that new starts stop, once.
func TestQ1_TheEndOfARunAtALimitNotifiesOnce(t *testing.T) {
	sc := newScene(t)
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	// The minimal run passes; the agent run reports a weekly usage of 0.51
	// (done.jsonl) against a target of 50.
	sc.setQuota(t, 0.10, sceneNow.Add(time.Hour), 0.10, sceneNow.Add(time.Hour))
	sc.quota.Weekly.Target = 50
	service := sc.service()
	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 1 {
		t.Fatalf("%d agent runs, want 1", n)
	}
	q1 := sc.q1Messages()
	if len(q1) != 1 || !strings.Contains(q1[0], "weekly") {
		t.Errorf("Q1 notifications = %q, want one about the weekly window", q1)
	}
	if !strings.Contains(sc.logs.String(), "Q1: the quota limit is reached at the end of a run") {
		t.Errorf("the log has no Q1 line for the end of the run")
	}
}

// Q1 stops new starts only: a check fix (I4) goes on over a limit, and
// makes no minimal run.
func TestQ1_ACheckFixGoesOnOverALimitWithoutAMinimalRun(t *testing.T) {
	sc := newScene(t, cliOptions{commit: true})
	sc.setQuota(t, 0.99, sceneNow.Add(time.Hour), 0.99, sceneNow.Add(time.Hour))
	service := sc.service()
	sc.failingCheck(t, service, 0)
	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want the check fix", n)
	}
	if n := sc.quotaRuns(t); n != 0 {
		t.Errorf("%d minimal runs, want none", n)
	}
}

// Q1 for R1: a requirement issue over a limit keeps cumin/status/ready.
func TestQ1_ASplitOverALimitKeepsTheRequirementIssue(t *testing.T) {
	sc := newPlanScene(t)
	sc.setQuota(t, 0.90, sceneNow.Add(time.Hour), 0.10, sceneNow.Add(time.Hour))
	sc.pollAndWait(t, sc.service())

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want 0", n)
	}
	if n := sc.fake.CountRequests(http.MethodPut, putRequirementLabelsPath); n != 0 {
		t.Errorf("%d label changes of #6, want none", n)
	}
	if q1 := sc.q1Messages(); len(q1) != 1 || !strings.Contains(q1[0], "issue #6") {
		t.Errorf("Q1 notifications = %q, want one that links #6", q1)
	}
}

// Info logs hold no usage number: the fake usage 0.90 appears only in the
// debug line of the decision.
func TestQ1_InfoLogsHoldNoUsageNumber(t *testing.T) {
	sc := newScene(t)
	sc.setQuota(t, 0.9, sceneNow.Add(2*time.Hour), 0.1, sceneNow.Add(time.Hour))
	sc.pollAndWait(t, sc.service())

	for _, line := range strings.Split(sc.logs.String(), "\n") {
		if strings.Contains(line, `"level":"DEBUG"`) {
			continue
		}
		if strings.Contains(line, "utilization") || strings.Contains(line, "0.9") {
			t.Errorf("an info line holds the usage:\n%s", line)
		}
	}
	for _, m := range sc.webhook.messagesSent() {
		if strings.Contains(m, "0.9") || strings.Contains(m, "90") {
			t.Errorf("a notification holds the usage:\n%s", m)
		}
	}
}

// bandOverTheWholeDay is one time band from 00:00 to 00:00 minus a minute,
// so that it holds every time of the tests.
func bandOverTheWholeDay(threshold int) []config.TimeBand {
	return []config.TimeBand{{From: 0, To: 23*60 + 59, Threshold: threshold}}
}

// Q1: a notification that the channel did not take is sent again at the
// next poll that is still stopped, and then no more.
func TestQ1_AFailedNotificationIsSentAgain(t *testing.T) {
	sc := newScene(t)
	sc.setQuota(t, 0.90, sceneNow.Add(2*time.Hour), 0.10, sceneNow.Add(time.Hour))
	sc.webhook.fails(http.StatusBadRequest)
	service := sc.service()
	sc.pollAndWait(t, service)
	failed := len(sc.q1Messages())
	if failed == 0 {
		t.Fatal("the first poll sent nothing")
	}

	sc.webhook.fails(0)
	sc.pollAndWait(t, service)
	sc.pollAndWait(t, service)
	if got := len(sc.q1Messages()) - failed; got != 1 {
		t.Errorf("%d notifications after the channel recovered, want 1", got)
	}
}
