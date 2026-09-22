package workflow_test

import (
	"context"
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
	"github.com/cloveclovedev/cumin-works/internal/workflow"
)

// wrongFile makes every poll of the repository of the scene fail with the
// same reason: the file of the repository names a key of the Host.
func wrongFile(key string) githubtest.File {
	return githubtest.File{Content: key + "\nmax_review_rounds = 2\n"}
}

// pollTimes polls the repository of the scene n times, and fails the test
// when a poll succeeds while it should not.
func pollTimes(t *testing.T, service *workflow.Service, n int, wantFailure bool) {
	t.Helper()
	for i := range n {
		err := service.Poll(context.Background())
		service.Wait()
		if wantFailure && err == nil {
			t.Fatalf("poll %d succeeded, want the failure of the repository file", i+1)
		}
		if !wantFailure && err != nil {
			t.Fatalf("poll %d: %v", i+1, err)
		}
	}
}

// A poll of one repository that fails three times in a row with the same
// reason gives one notification. The fourth failure gives none
// (cumin-core.md, the last line of the table of notifications).
func TestPoll_ThreeFailuresWithTheSameReasonNotifyOnce(t *testing.T) {
	sc := newScene(t)
	sc.fake.SetFile(sc.repo, ".cumin/config.toml", wrongFile(`work_dir = "/tmp/elsewhere"`))
	service := sc.service()

	pollTimes(t, service, 2, true)
	if messages := sc.webhook.messagesSent(); len(messages) != 0 {
		t.Fatalf("%d notifications after two failures, want none: %v", len(messages), messages)
	}

	pollTimes(t, service, 1, true)
	messages := sc.webhook.messagesSent()
	if len(messages) != 1 {
		t.Fatalf("%d notifications after three failures, want 1: %v", len(messages), messages)
	}
	for _, want := range []string{"example-org/example-repo", "work_dir", "3 times", "github.com/example-org/example-repo"} {
		if !strings.Contains(messages[0], want) {
			t.Errorf("the notification has no %q:\n%s", want, messages[0])
		}
	}
	// The reason of a repository file names every key on a line of its
	// own; the message keeps its own shape.
	if n := strings.Count(messages[0], "\n"); n > 2 {
		t.Errorf("the message has %d line breaks, want the reason on one line:\n%s", n, messages[0])
	}

	pollTimes(t, service, 1, true)
	if messages := sc.webhook.messagesSent(); len(messages) != 1 {
		t.Errorf("%d notifications after four failures, want 1: %v", len(messages), messages)
	}
}

// After a poll of the repository succeeds, the count starts again, and the
// next run of three failures is told again.
func TestPoll_ASuccessfulPollLetsTheNextRunOfFailuresNotifyAgain(t *testing.T) {
	sc := newScene(t)
	// The poll that succeeds claims the ready sub-issue and starts the
	// agent. The pull request lets the verification pass, so that run
	// tells the Owner nothing and the notifications below are only the
	// ones of the failed polls.
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	sc.fake.SetFile(sc.repo, ".cumin/config.toml", wrongFile(`work_dir = "/tmp/elsewhere"`))
	service := sc.service()

	pollTimes(t, service, 3, true)
	if n := len(sc.webhook.messagesSent()); n != 1 {
		t.Fatalf("%d notifications, want 1", n)
	}

	// The Owner merged a fix.
	sc.fake.SetFile(sc.repo, ".cumin/config.toml", githubtest.File{Content: "max_review_rounds = 2\n"})
	pollTimes(t, service, 1, false)
	if n := len(sc.webhook.messagesSent()); n != 1 {
		t.Errorf("%d notifications after the successful poll, want 1", n)
	}

	sc.fake.SetFile(sc.repo, ".cumin/config.toml", wrongFile(`work_dir = "/tmp/elsewhere"`))
	pollTimes(t, service, 2, true)
	if n := len(sc.webhook.messagesSent()); n != 1 {
		t.Errorf("%d notifications after two new failures, want 1", n)
	}
	pollTimes(t, service, 1, true)
	if n := len(sc.webhook.messagesSent()); n != 2 {
		t.Errorf("%d notifications after three new failures, want 2", n)
	}
}

// A failure with another reason is another problem: the count starts
// again, so the notification comes on the third failure with that reason.
func TestPoll_AnotherReasonStartsTheCountAgain(t *testing.T) {
	sc := newScene(t)
	sc.fake.SetFile(sc.repo, ".cumin/config.toml", wrongFile(`work_dir = "/tmp/elsewhere"`))
	service := sc.service()

	pollTimes(t, service, 2, true)
	sc.fake.SetFile(sc.repo, ".cumin/config.toml", wrongFile(`poll_interval = "10s"`))
	pollTimes(t, service, 2, true)
	if n := len(sc.webhook.messagesSent()); n != 0 {
		t.Fatalf("%d notifications, want none: the reason changed at the third failure", n)
	}

	pollTimes(t, service, 1, true)
	messages := sc.webhook.messagesSent()
	if len(messages) != 1 {
		t.Fatalf("%d notifications, want 1", len(messages))
	}
	if !strings.Contains(messages[0], "poll_interval") {
		t.Errorf("the notification names the older reason:\n%s", messages[0])
	}
}

// The failures of one repository are counted apart from the failures of
// another, and a repository that fails does not stop the others.
func TestPoll_TheCountOfOneRepositoryIsItsOwn(t *testing.T) {
	sc := newScene(t)
	other := sc.fake.AddRepository("example-org", "other-repo")
	sc.fake.AddIssue(other, &githubtest.Issue{Number: 1, Labels: []string{githubtest.RequirementLabel}})
	sc.fake.SetFile(other, ".cumin/config.toml", wrongFile(`work_dir = "/tmp/elsewhere"`))
	sc.fake.SetFile(sc.repo, ".cumin/config.toml", wrongFile(`work_dir = "/tmp/elsewhere"`))

	service := sc.service()
	service.Targets = append(service.Targets, target(sc, "other-repo"))

	// Two polls: each repository failed twice.
	pollTimes(t, service, 2, true)
	if n := len(sc.webhook.messagesSent()); n != 0 {
		t.Fatalf("%d notifications, want none", n)
	}

	// The second repository is fixed; the first one is not.
	sc.fake.SetFile(other, ".cumin/config.toml", githubtest.File{Content: "max_review_rounds = 2\n"})
	pollTimes(t, service, 1, true)
	messages := sc.webhook.messagesSent()
	if len(messages) != 1 {
		t.Fatalf("%d notifications, want 1 for the repository that keeps failing: %v", len(messages), messages)
	}
	if !strings.Contains(messages[0], "example-org/example-repo") || strings.Contains(messages[0], "other-repo") {
		t.Errorf("the notification is about the wrong repository:\n%s", messages[0])
	}
}

// A repository that turns the notifications off is not told about its own
// failed polls either.
func TestPoll_FailuresOfARepositoryWithNotificationsOffAreOnlyLogged(t *testing.T) {
	sc := newScene(t)
	sc.notifications = false
	sc.fake.SetFile(sc.repo, ".cumin/config.toml", wrongFile(`work_dir = "/tmp/elsewhere"`))
	service := sc.service()

	pollTimes(t, service, 3, true)

	if n := len(sc.webhook.messagesSent()); n != 0 {
		t.Errorf("%d notifications, want none", n)
	}
	if !strings.Contains(sc.logs.String(), `"msg":"the poll of the repository keeps failing"`) {
		t.Errorf("the log does not say that the poll keeps failing:\n%s", sc.logs.String())
	}
}
