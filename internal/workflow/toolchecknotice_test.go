package workflow_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
)

// This file proves the notification of "stop agent starts" of
// issue-states.md for the tool checks of a repository (cumin-core.md, tests
// 41 and 42 of the top-level requirements): one notification when the
// checks begin to fail, and a new one only after every check passed.

// Three polls with the same failed checks send one notification. It starts
// with the name of the action, and names the repository and each failed
// check. The issue that waits only for the tool checks brings no "tell
// that cumin waits" notification: "stop agent starts" named the cause.
func TestFailedToolChecksNotifyOnceAndNameEachCheck(t *testing.T) {
	sc := newScene(t)
	missing := filepath.Join(t.TempDir(), "tool")
	sc.fake.SetFile(sc.repo, ".cumin/config.toml", githubtest.File{
		Content: "[[tool_checks]]\ncommand = [\"test\", \"-e\", \"" + missing + "\"]\n\n" +
			"[[tool_checks]]\ncommand = [\"true\"]\n\n" +
			"[[tool_checks]]\ncommand = [\"false\"]\n",
	})
	service := sc.service()

	sc.pollTimes(t, service, 3)

	want := "cumin: stop agent starts: The tool checks of this repository fail: test -e " + missing + "; false. " +
		"cumin starts no agent for this repository until every check passes. The agent runs that are going on end as usual.\n" +
		"example-org/example-repo\n" +
		"https://github.com/example-org/example-repo"
	if got := sc.webhook.messagesSent(); len(got) != 1 || got[0] != want {
		t.Errorf("notifications over three polls = %q, want one:\n%s", got, want)
	}
	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs while the tool checks fail, want 0", n)
	}
}

// With only an issue that waits for a tool check, and no agent at work,
// cumin sends no "tell that cumin waits" notification.
func TestAToolCheckStopIsNotWaiting(t *testing.T) {
	sc := newScene(t)
	sc.withAToolCheck(t)
	service := sc.service()

	sc.pollTimes(t, service, 3)

	if got := len(sc.quotaStopMessages()); got != 1 {
		t.Errorf("%d notifications of stop agent starts, want 1", got)
	}
	if got := sc.waitingMessages(); len(got) != 0 {
		t.Errorf("notifications that cumin waits = %q, want none", got)
	}
}

// A poll where every check passes forgets the mark: fail, pass, fail again
// sends two notifications, and the pass itself sends none.
func TestToolChecksThatFailAgainAfterAPassNotifyAgain(t *testing.T) {
	sc := newScene(t)
	// No ready issue: the polls only run the tool checks.
	if err := sc.fake.SetLabels(sc.repo, 10, []string{"risk/low"}); err != nil {
		t.Fatal(err)
	}
	tool := sc.withAToolCheck(t)
	service := sc.service()

	sc.pollTimes(t, service, 2)
	if got := len(sc.quotaStopMessages()); got != 1 {
		t.Fatalf("%d notifications of stop agent starts while the check fails, want 1", got)
	}

	if err := os.WriteFile(tool, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	sc.pollTimes(t, service, 2)
	if got := len(sc.quotaStopMessages()); got != 1 {
		t.Fatalf("%d notifications of stop agent starts after the check passed, want still 1", got)
	}

	if err := os.Remove(tool); err != nil {
		t.Fatal(err)
	}
	sc.pollTimes(t, service, 2)
	if got := len(sc.quotaStopMessages()); got != 2 {
		t.Errorf("%d notifications of stop agent starts after the check failed again, want 2", got)
	}
}

// With notify.discord.enabled = false in the file of the repository, a
// failed tool check sends nothing, and still starts no agent.
func TestFailedToolChecksSendNothingWithNotificationsOffForTheRepository(t *testing.T) {
	sc := newScene(t)
	sc.fake.SetFile(sc.repo, ".cumin/config.toml", githubtest.File{
		Content: "[notify.discord]\nenabled = false\n\n[[tool_checks]]\ncommand = [\"false\"]\n",
	})
	service := sc.service()

	sc.pollTimes(t, service, 3)

	if got := sc.quotaStopMessages(); len(got) != 0 {
		t.Errorf("notifications of stop agent starts = %q, want none", got)
	}
	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs while the tool check fails, want 0", n)
	}
	if !strings.Contains(sc.logs.String(), `"msg":"the notification is off for this repository"`) {
		t.Error("the log does not say that the notifications are off")
	}
}

// A send that the channel refused takes the mark back: the next poll sends
// the notification again, and the poll after a send that worked sends none.
func TestAFailedSendOfTheToolCheckNotificationIsTriedAgain(t *testing.T) {
	sc := newScene(t)
	sc.withAToolCheck(t)
	service := sc.service()

	sc.webhook.fails(http.StatusInternalServerError)
	sc.pollAndWait(t, service)
	// The fake webhook records the message that it refused.
	if got := len(sc.quotaStopMessages()); got != 1 {
		t.Fatalf("%d tries of the notification in the first poll, want 1", got)
	}

	sc.webhook.fails(http.StatusNoContent)
	sc.pollAndWait(t, service)
	if got := len(sc.quotaStopMessages()); got != 2 {
		t.Fatalf("%d tries of the notification after the second poll, want 2: the failed send is tried again", got)
	}

	sc.pollAndWait(t, service)
	if got := len(sc.quotaStopMessages()); got != 2 {
		t.Errorf("%d tries of the notification after the third poll, want still 2", got)
	}
}
