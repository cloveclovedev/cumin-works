package workflow_test

import (
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/notify"
	"github.com/cloveclovedev/cumin-works/internal/workflow"
)

// A blocked result is logged with the question of blocked_reason. The label
// stays; #81 posts the comment and asks the Maintainer.
// "stop the implementation" with a blocked result: cumin posts the
// blocked_reason on the issue, replaces the label with
// cumin/status/awaiting-decision, and notifies the Maintainer once. It
// does not retry.
func TestBlockedStopsTheIssueForTheMaintainer(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "blocked.jsonl"})
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	service := sc.service()

	sc.pollAndWait(t, service)

	const question = "## Decision needed: which sign-in method does the login screen use?"
	comments := sc.fake.Comments(sc.repo, 10)
	if len(comments) != 1 || comments[0].Body != question {
		t.Fatalf("the comments of #10 = %+v, want one with the blocked reason", comments)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", "cumin/status/awaiting-decision"}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/awaiting-decision", got)
	}

	messages := sc.webhook.messagesSent()
	if len(messages) != 1 {
		t.Fatalf("%d notifications, want 1: %v", len(messages), messages)
	}
	for _, want := range []string{"stop the implementation", question, "example-org/example-repo", "issue #10", "issuecomment-"} {
		if !strings.Contains(messages[0], want) {
			t.Errorf("the notification has no %q:\n%s", want, messages[0])
		}
	}

	// A blocked result is never retried.
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1", n)
	}
	logs := sc.logs.String()
	for _, want := range []string{`"msg":"stop the implementation: the agent returned blocked"`, `"msg":"stop the implementation: wrote the reason on the issue"`,
		`"msg":"stop the implementation: the issue waits for a Maintainer"`, `"msg":"the notification was sent"`, `"action":"stop the implementation"`} {
		if !strings.Contains(logs, want) {
			t.Errorf("the log has no %s:\n%s", want, logs)
		}
	}
}

// A webhook that fails changes nothing on GitHub: the comment and the
// label stay, and the failure is logged at error level.
func TestBlockedWithAFailedWebhookKeepsTheCommentAndTheLabel(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "blocked.jsonl"})
	sc.webhook.fails(http.StatusInternalServerError)
	service := sc.service()

	sc.pollAndWait(t, service)

	if n := len(sc.fake.Comments(sc.repo, 10)); n != 1 {
		t.Errorf("%d comments on #10, want 1", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, "cumin/status/awaiting-decision") {
		t.Errorf("labels of #10 = %v, want cumin/status/awaiting-decision", got)
	}
	logs := sc.logs.String()
	if !strings.Contains(logs, `"level":"ERROR","msg":"the notification was not sent"`) {
		t.Errorf("the log does not report the failed notification at error level:\n%s", logs)
	}
	if !strings.Contains(logs, "500") {
		t.Errorf("the log does not name the status of the webhook:\n%s", logs)
	}
	// The address of the webhook never reaches a log.
	if strings.Contains(logs, "fake-token") {
		t.Errorf("the log holds a part of the webhook address:\n%s", logs)
	}
}

// A repository that turns the notifications off still gets the comment and
// the label; nothing is sent.
func TestBlockedWithNotificationsOffWritesOnlyOnGitHub(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "blocked.jsonl"})
	sc.notifications = false
	service := sc.service()

	sc.pollAndWait(t, service)

	if n := len(sc.fake.Comments(sc.repo, 10)); n != 1 {
		t.Errorf("%d comments on #10, want 1", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, "cumin/status/awaiting-decision") {
		t.Errorf("labels of #10 = %v, want cumin/status/awaiting-decision", got)
	}
	if messages := sc.webhook.messagesSent(); len(messages) != 0 {
		t.Errorf("%d notifications, want none: %v", len(messages), messages)
	}
	if !strings.Contains(sc.logs.String(), `"msg":"the notification is off for this repository"`) {
		t.Errorf("the log does not say that the notifications are off:\n%s", sc.logs.String())
	}
}

// Without a channel (no webhook URL on the Host), the stop still happens on
// GitHub and the missing channel is logged at error level.
func TestBlockedWithoutAChannelIsLoggedAtErrorLevel(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "blocked.jsonl"})
	sc.notifier = notify.New(nil)
	service := sc.service()

	sc.pollAndWait(t, service)

	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, "cumin/status/awaiting-decision") {
		t.Errorf("labels of #10 = %v, want cumin/status/awaiting-decision", got)
	}
	if !strings.Contains(sc.logs.String(), `"level":"ERROR","msg":"the notification was not sent"`) {
		t.Errorf("the log does not report the missing channel at error level:\n%s", sc.logs.String())
	}
}

// assertVerificationFailed checks that the label of #10 stayed at
// cumin/status/implementing and that the log names the failure.

// The test of a top-level requirement in cumin-core.md: a result that does
// not match the schema leaves
// no pull request, so the implementation is requested again exactly once.
// After the second run the issue goes to the Maintainer, with one comment that
// names what is missing on GitHub, the label, and exactly one notification.
func TestAnInvalidResultIsRetriedOnceAndThenGoesToTheMaintainer(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "invalid-result.jsonl"})
	service := sc.service()

	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 2 {
		t.Fatalf("%d agent runs, want 2 (the request and one retry)", n)
	}
	comments := sc.fake.Comments(sc.repo, 10)
	if len(comments) != 1 {
		t.Fatalf("%d comments on #10, want 1: %+v", len(comments), comments)
	}
	body := comments[0].Body
	for _, want := range []string{"## Stopped for a Maintainer", "Step: stop the implementation", workflow.VerificationReason(workflow.FailureNoOpenPullRequest), "Retried: once", "Pull request: None"} {
		if !strings.Contains(body, want) {
			t.Errorf("the comment has no %q:\n%s", want, body)
		}
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", "cumin/status/awaiting-decision"}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/awaiting-decision", got)
	}
	messages := sc.webhook.messagesSent()
	if len(messages) != 1 {
		t.Fatalf("%d notifications, want 1: %v", len(messages), messages)
	}
	for _, want := range []string{"stop the implementation", workflow.VerificationReason(workflow.FailureNoOpenPullRequest), "example-org/example-repo", "issue #10"} {
		if !strings.Contains(messages[0], want) {
			t.Errorf("the notification has no %q:\n%s", want, messages[0])
		}
	}
	logs := sc.logs.String()
	if !strings.Contains(logs, `"msg":"request the implementation again: the pull request does not pass the check; the same request runs again in the same work directory"`) {
		t.Errorf("the log does not say that the request ran again:\n%s", logs)
	}
}

// The retry is the same request in the same work directory, and it starts
// a new session: no session of the first run is resumed.
func TestTheRetryIsTheSameRequestInANewSession(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "no-result.jsonl"})
	service := sc.service()

	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 2 {
		t.Fatalf("%d agent runs, want 2", n)
	}
	// The records of the fake CLI hold the last run.
	wantDir := filepath.Join(sc.workRoot, "example-org", "example-repo", "10-implementer")
	if got := strings.TrimSpace(sc.record(t, "agent.cwd")); got != realPath(t, wantDir) {
		t.Errorf("the retry ran in %q, want the same work directory %q", got, realPath(t, wantDir))
	}
	args := sc.record(t, "agent.args")
	if strings.Contains(args, "--resume") {
		t.Errorf("the retry resumed a session:\n%q", args)
	}
	// The request text is the one of the first run: the same kind, the
	// same issue, the same branch, and the same work directory that cumin
	// prepared (which the Workspace gives without resolving symlinks). The
	// facts of the run stand before it, with the end time of the retry.
	if got, want := promptOf(t, args), workflow.ImplementRequestText("example-org/example-repo", 10, wantBranch, wantDir); !strings.HasSuffix(got, "\n\n"+want) {
		t.Errorf("the request text of the retry = %q, want %q", got, want)
	}
}

// assertVerificationFailed checks the whole failed path of the verification: the log
// names the check that failed, the issue holds one comment in the form of
// templates/stop-note.md with the sentence of that check, the label is
// cumin/status/awaiting-decision, and exactly one notification went
// out with the same sentence. pullRequest is the number that the comment
// must name, or 0 for "None".
func assertVerificationFailed(t *testing.T, sc *scene, failure string, kind workflow.VerificationFailure, pullRequest int) {
	t.Helper()
	if got := kind.String(); got != failure {
		t.Errorf("the failure is named %q, want %q", got, failure)
	}
	logs := sc.logs.String()
	for _, want := range []string{`"msg":"request the implementation again: the pull request does not pass the check; the same request runs again in the same work directory"`,
		`"msg":"stop the implementation: the implementation stops for a Maintainer"`, `"retried":true`} {
		if !strings.Contains(logs, want) {
			t.Errorf("the log has no %s:\n%s", want, logs)
		}
	}
	if n := sc.agentRuns(t); n != 2 {
		t.Errorf("%d agent runs, want 2 (the request and the second request)", n)
	}

	assertStopped(t, sc, workflow.VerificationReason(kind), pullRequest, "once")
}

// assertImplementationStopped checks the stop step of "stop the
// implementation" for #10: one comment in the
// form of templates/stop-note.md with the reason, the label
// cumin/status/awaiting-decision, and exactly one notification with
// the same reason.
func assertImplementationStopped(t *testing.T, sc *scene, reason string, pullRequest int) {
	t.Helper()
	assertStopped(t, sc, reason, pullRequest, "no")
}

// assertStopped is assertImplementationStopped with what the comment says about the
// second request: "no", or "once" after the implementation was requested
// again.
func assertStopped(t *testing.T, sc *scene, reason string, pullRequest int, retried string) {
	t.Helper()
	comments := sc.fake.Comments(sc.repo, 10)
	if len(comments) != 1 {
		t.Fatalf("%d comments on #10, want 1: %+v", len(comments), comments)
	}
	body := comments[0].Body
	want := []string{"## Stopped for a Maintainer", "Step: stop the implementation", "Reason: " + reason, "Retried: " + retried, "cumin/status/ready"}
	if pullRequest > 0 {
		want = append(want, fmt.Sprintf("Pull request: #%d", pullRequest))
	} else {
		want = append(want, "Pull request: None")
	}
	for _, line := range want {
		if !strings.Contains(body, line) {
			t.Errorf("the comment has no %q:\n%s", line, body)
		}
	}

	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", "cumin/status/awaiting-decision"}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/awaiting-decision", got)
	}
	if n := sc.fake.CountRequests(http.MethodPut, putLabelsPath); n != 2 {
		t.Errorf("%d label changes, want 2 (the claim and the stop)", n)
	}

	messages := sc.webhook.messagesSent()
	if len(messages) != 1 {
		t.Fatalf("%d notifications, want 1: %v", len(messages), messages)
	}
	for _, line := range []string{"stop the implementation", reason, "example-org/example-repo", "issue #10"} {
		if !strings.Contains(messages[0], line) {
			t.Errorf("the notification has no %q:\n%s", line, messages[0])
		}
	}
}
