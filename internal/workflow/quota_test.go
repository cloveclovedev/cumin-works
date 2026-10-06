package workflow_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/agent"
	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/core/state"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
	"github.com/cloveclovedev/cumin-works/internal/workflow"
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
	if !strings.Contains(logs, `"msg":"stop agent starts: the quota limit is reached"`) {
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
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, "cumin/status/checking") {
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
	if len(q1) != 1 || !strings.Contains(q1[0], "The quota usage was not read before the start of an agent") {
		t.Errorf("Q1 notifications = %q, want one about the unread usage", q1)
	}

	sc.setQuota(t, 0.10, sceneNow.Add(time.Hour), 0.10, sceneNow.Add(time.Hour))
	sc.pollAndWait(t, service)
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("after a read: %d agent runs, want 1", n)
	}
}

// Q1 at the end of a run: the usage that the run reports tells the Owner
// that agent starts stop, once.
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
	if !strings.Contains(sc.logs.String(), "stop agent starts: the quota limit is reached at the end of a run") {
		t.Errorf("the log has no Q1 line for the end of the run")
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
		if logLineHoldsUsage(t, line) {
			t.Errorf("an info line holds the usage:\n%s", line)
		}
	}
	for _, m := range sc.webhook.messagesSent() {
		if strings.Contains(m, "0.9") || strings.Contains(m, "90") {
			t.Errorf("a notification holds the usage:\n%s", m)
		}
	}
}

// logLineHoldsUsage reports whether a JSON log line holds the fake usage
// 0.90 outside of its timestamp: a time such as 12:00:20.9 is not a usage.
func logLineHoldsUsage(t *testing.T, line string) bool {
	t.Helper()
	if line == "" {
		return false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &fields); err != nil {
		t.Fatalf("a log line is not JSON: %v\n%s", err, line)
	}
	delete(fields, "time")
	rest, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal the fields of a log line: %v", err)
	}
	return strings.Contains(string(rest), "utilization") || strings.Contains(string(rest), "0.9")
}

// The check of the info logs does not depend on the clock: a timestamp that
// holds "0.9" is no usage, and a usage in any other field is found.
func TestQ1_TheCheckOfTheInfoLogsIgnoresTheTimestamp(t *testing.T) {
	for _, tc := range []struct {
		name string
		line string
		want bool
	}{
		{"a timestamp that holds 0.9", `{"time":"2026-01-01T12:00:20.9Z","level":"INFO","msg":"Q1: the quota limit is reached"}`, false},
		{"a usage in a field", `{"time":"2026-01-01T12:00:00Z","level":"INFO","msg":"Q1","five_hour":0.9}`, true},
		{"a usage in the message", `{"time":"2026-01-01T12:00:00Z","level":"INFO","msg":"usage 0.90"}`, true},
		{"a field named utilization", `{"time":"2026-01-01T12:00:00Z","level":"INFO","msg":"Q1","utilization":1}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := logLineHoldsUsage(t, tc.line); got != tc.want {
				t.Errorf("logLineHoldsUsage = %v, want %v", got, tc.want)
			}
		})
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

// Q1: a window that resumes and stops again is a new stop, even while the
// other window still stops the starts.
func TestQ1_AWindowThatStopsAgainNotifiesAgain(t *testing.T) {
	sc := newScene(t)
	weeklyReset := sceneNow.Add(6 * 24 * time.Hour) // early in the week
	sc.setQuota(t, 0.90, sceneNow.Add(time.Hour), 0.40, weeklyReset)
	service := sc.service()
	sc.pollAndWait(t, service)
	if got := len(sc.q1Messages()); got != 2 {
		t.Fatalf("%d notifications, want one for each window", got)
	}

	// The 5h window resets; the weekly window still stops the start.
	sc.setQuota(t, 0.10, sceneNow.Add(6*time.Hour), 0.40, weeklyReset)
	sc.pollAndWait(t, service)
	// The new 5h window reaches its limit again.
	sc.setQuota(t, 0.90, sceneNow.Add(6*time.Hour), 0.40, weeklyReset)
	sc.pollAndWait(t, service)

	q1 := sc.q1Messages()
	if len(q1) != 3 || !strings.Contains(q1[2], "5h") {
		t.Errorf("Q1 notifications = %q, want a second one for the 5h window", q1)
	}
}

// withState gives the service a state file, where the usage is kept.
func withState(t *testing.T, service *workflow.Service) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	service.State = state.Open(path, nil)
	return path
}

// Q3: a stop by the 5h window makes no minimal run before its reset. After
// the reset, the check before the start reads again, and the start goes on.
func TestQ3_AFiveHourStopWaitsForItsResetWithoutAMinimalRun(t *testing.T) {
	sc := newScene(t)
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	reset := sceneNow.Add(2 * time.Hour)
	sc.setQuota(t, 0.90, reset, 0.10, sceneNow.Add(time.Hour))
	service := sc.service()
	withState(t, service)
	for range 3 {
		sc.pollAndWait(t, service)
	}
	sc.clock.Set(reset.Add(-time.Minute))
	sc.pollAndWait(t, service)
	if n := sc.quotaRuns(t); n != 1 {
		t.Errorf("%d minimal runs before the reset, want 1", n)
	}
	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs before the reset, want 0", n)
	}

	sc.clock.Set(reset)
	sc.setQuota(t, 0.05, reset.Add(5*time.Hour), 0.10, reset.Add(time.Hour))
	sc.pollAndWait(t, service)
	if n := sc.quotaRuns(t); n != 2 {
		t.Errorf("%d minimal runs after the reset, want 2", n)
	}
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs after the reset, want 1", n)
	}
}

// Q3: a time band with a higher threshold ends the wait at its start.
func TestQ3_AHigherTimeBandEndsTheWait(t *testing.T) {
	sc := newScene(t)
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	local := sceneNow.In(sceneZone)
	start := time.Date(local.Year(), local.Month(), local.Day(), local.Hour()+1, 0, 0, 0, sceneZone)
	from := config.TimeOfDay(start.Hour() * 60)
	sc.quota.FiveHour.Bands = []config.TimeBand{{From: from, To: (from + 120) % (24 * 60), Threshold: 95}}
	sc.setQuota(t, 0.90, sceneNow.Add(4*time.Hour), 0.10, sceneNow.Add(time.Hour))
	service := sc.service()
	withState(t, service)
	sc.pollAndWait(t, service)
	sc.clock.Set(start.Add(-time.Minute))
	sc.pollAndWait(t, service)
	if n := sc.quotaRuns(t); n != 1 {
		t.Errorf("%d minimal runs before the band, want 1", n)
	}

	sc.clock.Set(start)
	sc.pollAndWait(t, service)
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs in the band, want 1", n)
	}
}

// Core-15 and Q3: a stop by the weekly pace resumes by time alone, with no
// minimal run while the stored usage stops the start.
func TestCore15_TheWeeklyPaceResumesByTimeAloneWithoutAMinimalRun(t *testing.T) {
	sc := newScene(t)
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	weeklyReset := sceneNow.Add(6 * 24 * time.Hour) // one day into the week
	sc.setQuota(t, 0.10, sceneNow.Add(time.Hour), 0.40, weeklyReset)
	service := sc.service()
	withState(t, service)
	sc.pollAndWait(t, service)

	// 85 x (e + 1 day) / 7 days passes 40 at e of about 2.29 days: about
	// 31 hours after sceneNow, which is one day into the week.
	sc.clock.Set(sceneNow.Add(30 * time.Hour))
	sc.pollAndWait(t, service)
	if n := sc.quotaRuns(t); n != 1 {
		t.Errorf("%d minimal runs before the pace passes the usage, want 1", n)
	}

	sc.clock.Set(sceneNow.Add(32 * time.Hour))
	sc.pollAndWait(t, service)
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs after the pace passed the usage, want 1", n)
	}
}

// Q3: after a restart, the stored usage keeps cumin stopped without a
// minimal run.
func TestQ3_ARestartWhileStoppedMakesNoMinimalRun(t *testing.T) {
	sc := newScene(t)
	sc.setQuota(t, 0.90, sceneNow.Add(2*time.Hour), 0.10, sceneNow.Add(time.Hour))
	first := sc.service()
	path := withState(t, first)
	sc.pollAndWait(t, first)

	// The restart is later than the 5 minutes of a new enough usage, so the
	// next try time keeps the stop, with no second notification. A restart
	// within the 5 minutes decides from the stored usage, and the marks of
	// the notifications are in memory, so it can notify once more.
	sc.clock.Set(sceneNow.Add(5*time.Minute + time.Second))
	again := sc.service()
	again.State = state.Open(path, nil)
	sc.pollAndWait(t, again)
	if n := sc.quotaRuns(t); n != 1 {
		t.Errorf("%d minimal runs across the restart, want 1", n)
	}
	if got := len(sc.q1Messages()); got != 1 {
		t.Errorf("%d Q1 notifications, want 1", got)
	}
}

// Q1: a usage that cumin keeps, as at the end of a run, is a read that
// succeeded, so a later unread usage is a new failure and notifies again.
func TestQ1_AKeptUsageEndsTheSilenceAfterAnUnreadUsage(t *testing.T) {
	sc := newScene(t, cliOptions{commit: true})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 11, Parent: 6, Title: "second", Labels: []string{"cumin/status/ready", "risk/low"}})
	sc.failQuota(t)
	service := sc.service()
	service.Settings.MaxIssuesInProgress = 2
	// #10 waits for a check fix, and #11 is ready: both wait for a read.
	sc.failingCheck(t, service, 0)
	sc.pollAndWait(t, service)
	if n := sc.agentRuns(t); n != 0 {
		t.Fatalf("%d agent runs without a usage, want 0", n)
	}
	// The end of a run keeps its usage. The usage is older than what is new
	// enough, so the next start reads again.
	storeUsage(service, 0.10, sceneNow.Add(-10*time.Minute))
	sc.pollAndWait(t, service)

	var unread int
	for _, m := range sc.q1Messages() {
		if strings.Contains(m, "was not read") {
			unread++
		}
	}
	if unread != 2 {
		t.Errorf("%d notifications about an unread usage, want 2 (before and after the usage of a run)", unread)
	}
}

// Q3: an older reading that arrives after a newer one never replaces it,
// and the decision uses the newer one.
func TestQ3_AnOlderReadingNeverReplacesANewerOne(t *testing.T) {
	sc := newScene(t)
	service := sc.service()
	withState(t, service)
	reset := sceneNow.Add(2 * time.Hour)
	newer := agent.QuotaUsage{
		FiveHour: agent.QuotaWindow{Utilization: 0.90, ResetsAt: reset},
		Weekly:   agent.QuotaWindow{Utilization: 0.20, ResetsAt: sceneNow.Add(time.Hour)},
	}
	older := newer
	older.FiveHour.Utilization = 0.50
	workflow.KeepUsage(service, newer)
	got := workflow.KeepUsage(service, older)
	if got.FiveHour.Utilization != 0.90 {
		t.Errorf("the decision uses %v, want the newer reading 0.90", got.FiveHour.Utilization)
	}
	if stored, _ := service.State.Quota(); stored.FiveHour.Utilization != 0.90 {
		t.Errorf("the state holds %v, want the newer reading 0.90", stored.FiveHour.Utilization)
	}
}

// allow writes the allowance file of the service, as cumin quota allow does.
func allow(t *testing.T, service *workflow.Service, until time.Time) {
	t.Helper()
	if service.AllowancePath == "" {
		service.AllowancePath = filepath.Join(t.TempDir(), state.AllowanceFileName)
	}
	if err := state.WriteAllowance(service.AllowancePath, state.Allowance{FiveHourUntil: until}); err != nil {
		t.Fatal(err)
	}
}

// Core-6 and Q2: after cumin quota allow, the next poll starts the issue
// that the 5h window stopped, without waiting for the next try time.
func TestCore06_QuotaAllowResumesTheStart(t *testing.T) {
	sc := newScene(t)
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	reset := sceneNow.Add(2 * time.Hour)
	sc.setQuota(t, 0.90, reset, 0.10, sceneNow.Add(time.Hour))
	service := sc.service()
	withState(t, service)
	sc.pollAndWait(t, service)
	if n := sc.agentRuns(t); n != 0 {
		t.Fatalf("%d agent runs before the allowance, want 0", n)
	}

	allow(t, service, reset)
	sc.pollAndWait(t, service)
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs after the allowance, want 1", n)
	}
	if q1 := sc.q1Messages(); len(q1) != 1 || !strings.Contains(q1[0], "cumin quota allow") {
		t.Errorf("Q1 notifications = %q, want one that names cumin quota allow", q1)
	}
}

// Core-16: cumin quota allow never passes the weekly pace limit.
func TestCore16_QuotaAllowDoesNotPassTheWeeklyPace(t *testing.T) {
	sc := newScene(t)
	reset := sceneNow.Add(2 * time.Hour)
	sc.setQuota(t, 0.90, reset, 0.90, sceneNow.Add(time.Hour))
	service := sc.service()
	withState(t, service)
	allow(t, service, reset)
	sc.pollAndWait(t, service)
	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want 0", n)
	}
	if n := sc.quotaRuns(t); n != 1 {
		t.Errorf("%d minimal runs, want 1: the allowance does not end the weekly wait", n)
	}
}

// Q2: the allowance ends at the reset of the 5h window that it names. A
// new window at its limit stops the start again.
func TestQ2_TheAllowanceEndsAtTheResetOfItsWindow(t *testing.T) {
	sc := newScene(t)
	reset := sceneNow.Add(time.Hour)
	sc.setQuota(t, 0.90, reset.Add(5*time.Hour), 0.10, sceneNow.Add(time.Hour))
	service := sc.service()
	allow(t, service, reset)
	sc.clock.Set(reset)
	sc.pollAndWait(t, service)
	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs after the allowance ended, want 0", n)
	}
}

// Q2: an allowance file that cannot be read is no allowance, with one
// warning across polls.
func TestQ2_ABrokenAllowanceFileWarnsOnce(t *testing.T) {
	sc := newScene(t)
	sc.setQuota(t, 0.90, sceneNow.Add(2*time.Hour), 0.10, sceneNow.Add(time.Hour))
	service := sc.service()
	service.AllowancePath = filepath.Join(t.TempDir(), state.AllowanceFileName)
	if err := os.WriteFile(service.AllowancePath, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	sc.pollAndWait(t, service)
	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want 0", n)
	}
	if n := strings.Count(sc.logs.String(), "the allowance file was not read"); n != 1 {
		t.Errorf("%d warnings, want 1", n)
	}
}

// Q3: a reading that comes in late never makes the stored time of the
// read earlier.
func TestQ3_ALateReadingKeepsTheLaterReadTime(t *testing.T) {
	sc := newScene(t)
	service := sc.service()
	withState(t, service)
	reset := sceneNow.Add(2 * time.Hour)
	newer := agent.QuotaUsage{
		FiveHour: agent.QuotaWindow{Utilization: 0.90, ResetsAt: reset},
		Weekly:   agent.QuotaWindow{Utilization: 0.20, ResetsAt: sceneNow.Add(time.Hour)},
		ReadAt:   sceneNow,
	}
	older := newer
	older.FiveHour.Utilization = 0.50
	older.ReadAt = sceneNow.Add(-10 * time.Minute)
	workflow.KeepUsage(service, newer)
	workflow.KeepUsage(service, older)
	if stored, _ := service.State.Quota(); !stored.ReadAt.Equal(sceneNow) {
		t.Errorf("read_at = %v, want the later %v", stored.ReadAt, sceneNow)
	}
}

// storeUsage keeps a usage that cumin read at readAt, as the end of an
// agent run does.
func storeUsage(service *workflow.Service, fiveHour float64, readAt time.Time) {
	workflow.KeepUsage(service, agent.QuotaUsage{
		FiveHour: agent.QuotaWindow{Utilization: fiveHour, ResetsAt: sceneNow.Add(2 * time.Hour)},
		Weekly:   agent.QuotaWindow{Utilization: 0.10, ResetsAt: sceneNow.Add(time.Hour)},
		ReadAt:   readAt,
	})
}

// A start within 5 minutes after a read makes no minimal run: the stored
// usage decides it. A minimal run would read a usage at the limit here, and
// would stop the start.
func TestQ1_AStoredUsageThatIsNewEnoughDecidesTheStartWithoutAMinimalRun(t *testing.T) {
	sc := newScene(t)
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	sc.setQuota(t, 0.90, sceneNow.Add(2*time.Hour), 0.10, sceneNow.Add(time.Hour))
	service := sc.service()
	withState(t, service)
	storeUsage(service, 0.10, sceneNow.Add(-5*time.Minute))
	sc.pollAndWait(t, service)

	if n := sc.quotaRuns(t); n != 0 {
		t.Errorf("%d minimal runs, want 0", n)
	}
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1", n)
	}
}

// A start more than 5 minutes after the read makes exactly one minimal
// run, and the usage of that run decides.
func TestQ1_AStoredUsageThatIsOlderCostsOneMinimalRun(t *testing.T) {
	sc := newScene(t)
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	sc.setQuota(t, 0.10, sceneNow.Add(2*time.Hour), 0.10, sceneNow.Add(time.Hour))
	service := sc.service()
	withState(t, service)
	storeUsage(service, 0.10, sceneNow.Add(-5*time.Minute-time.Second))
	sc.pollAndWait(t, service)

	if n := sc.quotaRuns(t); n != 1 {
		t.Errorf("%d minimal runs, want 1", n)
	}
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1", n)
	}
}

// A stored usage at a limit that is new enough stops the start with no
// minimal run, and the Owner hears once over several polls.
func TestQ1_AStoredUsageAtALimitThatIsNewEnoughStopsTheStartAndNotifiesOnce(t *testing.T) {
	sc := newScene(t)
	// A minimal run would read a usage below the limit, and would start.
	sc.setQuota(t, 0.10, sceneNow.Add(2*time.Hour), 0.10, sceneNow.Add(time.Hour))
	service := sc.service()
	withState(t, service)
	storeUsage(service, 0.90, sceneNow.Add(-time.Minute))
	for range 3 {
		sc.pollAndWait(t, service)
	}

	if n := sc.quotaRuns(t); n != 0 {
		t.Errorf("%d minimal runs, want 0", n)
	}
	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want 0", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, "cumin/status/ready") {
		t.Errorf("labels of #10 = %v, want cumin/status/ready kept", got)
	}
	q1 := sc.q1Messages()
	if len(q1) != 1 || !strings.Contains(q1[0], "The 5h quota window reached its limit") {
		t.Errorf("Q1 notifications = %q, want one about the 5h window", q1)
	}
}

// A start with no stored usage makes one minimal run.
func TestQ1_AStartWithNoStoredUsageCostsOneMinimalRun(t *testing.T) {
	sc := newScene(t)
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	sc.setQuota(t, 0.10, sceneNow.Add(2*time.Hour), 0.10, sceneNow.Add(time.Hour))
	service := sc.service()
	withState(t, service)
	sc.pollAndWait(t, service)

	if n := sc.quotaRuns(t); n != 1 {
		t.Errorf("%d minimal runs, want 1", n)
	}
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1", n)
	}
}

// A read of the usage that fails is kept for the rest of the poll: two
// waiting requests of one poll cost one minimal run, and neither changes a
// label, counts a request, or starts an agent. The next poll reads again,
// and both requests start when that read succeeds.
func TestQ1_AFailedReadOfTheUsageIsKeptForTheRestOfThePoll(t *testing.T) {
	sc := newScene(t, cliOptions{})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 11, Parent: 6, Title: "second", Labels: []string{"cumin/status/ready", "risk/low"}})
	sc.failQuota(t)
	service := sc.service()
	service.Settings.MaxIssuesInProgress = 2
	sc.failingCheck(t, service, 0)
	sc.pollAndWait(t, service)

	if n := sc.quotaRuns(t); n != 1 {
		t.Errorf("%d minimal runs in the poll, want 1 for two waiting requests", n)
	}
	sc.assertNoStartAtTheLimit(t, []string{"cumin/status/checking", "risk/low"})
	if n := service.State.Issue("example-org/example-repo", 10).CheckFixRequests; n != 0 {
		t.Errorf("the state file counts %d check fix requests, want none", n)
	}
	if got := sc.fake.Issue(sc.repo, 11).Labels; !slices.Equal(got, []string{"cumin/status/ready", "risk/low"}) {
		t.Errorf("labels of #11 = %v, want the ready issue as it was", got)
	}
	q1 := sc.q1Messages()
	if len(q1) != 1 || !strings.Contains(q1[0], "The quota usage was not read before the start of an agent") {
		t.Errorf("Q1 notifications = %q, want one about the unread usage", q1)
	}

	// The next poll reads again: the mark does not outlive the poll.
	sc.pollAndWait(t, service)
	if n := sc.quotaRuns(t); n != 2 {
		t.Errorf("%d minimal runs after two polls, want 2: one for each poll", n)
	}
	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs while the usage is not read, want 0", n)
	}

	// A read that succeeds sets no mark: both requests start in the same
	// poll. The run of #11 ends with no pull request, so cumin requests its
	// implementation again: the runs are counted for each issue.
	sc.setQuota(t, 0.10, sceneNow.Add(2*time.Hour), 0.10, sceneNow.Add(time.Hour))
	sc.pollAndWait(t, service)
	if id := service.State.Issue("example-org/example-repo", 11).SessionID; id == "" {
		t.Error("the state file holds no session of #11 after the read, want its implementation started")
	}
	if n := service.State.Issue("example-org/example-repo", 10).CheckFixRequests; n != 1 {
		t.Errorf("the state file counts %d check fix requests after the read, want 1", n)
	}
	if got := sc.fake.Issue(sc.repo, 11).Labels; slices.Contains(got, "cumin/status/ready") {
		t.Errorf("labels of #11 after the read = %v, want the issue out of cumin/status/ready", got)
	}
}

// implementerRunEnded polls once on a ready issue: one minimal run reads a
// usage below every limit, and the Implementer run ends through the fake
// CLI, whose done.jsonl reports a usage below every limit too. cumin stores
// that usage by itself (quotaAfterRun); the test writes no usage.
func implementerRunEnded(t *testing.T) (*scene, *workflow.Service) {
	t.Helper()
	sc := newScene(t, cliOptions{reviews: []string{"NONE", "APPROVE"}})
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	sc.setQuota(t, 0.10, sceneNow.Add(2*time.Hour), 0.10, sceneNow.Add(time.Hour))
	service := sc.service()
	withState(t, service)
	sc.pollAndWait(t, service)
	if n := sc.agentRuns(t); n != 1 {
		t.Fatalf("%d agent runs after the first poll, want 1: the Implementer", n)
	}
	if n := sc.quotaRuns(t); n != 1 {
		t.Fatalf("%d minimal runs before the Implementer, want 1", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, "cumin/status/checking") {
		t.Fatalf("labels of #10 after the Implementer = %v, want cumin/status/checking", got)
	}
	return sc, service
}

// assertTheReviewerStarted checks that the second agent run is the review.
func (sc *scene) assertTheReviewerStarted(t *testing.T) {
	t.Helper()
	if n := sc.agentRuns(t); n != 2 {
		t.Fatalf("%d agent runs, want 2: the Implementer and the Reviewer", n)
	}
	if text := promptOf(t, sc.record(t, "agent.args")); !strings.Contains(text, "Request: review") {
		t.Errorf("the second request is not a review:\n%s", text)
	}
}

// The usage that the end of an Implementer run reports reaches the next
// start: the checks pass, the Reviewer starts within 5 minutes, and no
// minimal run happens before it. The two starts cost one minimal run.
func TestQ1_TheUsageOfARunEndLetsTheNextStartSkipTheMinimalRun(t *testing.T) {
	sc, service := implementerRunEnded(t)
	sc.clock.Set(sceneNow.Add(5 * time.Minute))
	sc.pollAndWait(t, service)

	sc.assertTheReviewerStarted(t)
	if n := sc.quotaRuns(t); n != 1 {
		t.Errorf("%d minimal runs, want 1: none before the Reviewer", n)
	}
}

// The usage of a run end is new enough for 5 minutes only: a Reviewer that
// starts later than that costs exactly one more minimal run.
func TestQ1_TheUsageOfARunEndThatIsOlderCostsOneMinimalRun(t *testing.T) {
	sc, service := implementerRunEnded(t)
	sc.clock.Set(sceneNow.Add(5*time.Minute + time.Second))
	sc.pollAndWait(t, service)

	sc.assertTheReviewerStarted(t)
	if n := sc.quotaRuns(t); n != 2 {
		t.Errorf("%d minimal runs, want 2: exactly one before the Reviewer", n)
	}
}
