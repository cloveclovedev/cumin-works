package github_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
)

const (
	labelsPath   = "/repos/example-org/example-repo/labels"
	commentsPath = "/repos/example-org/example-repo/issues/12/comments"
	// everyTry is the first try and the 3 retries of a read.
	everyTry = 4
)

// retryScene is a fake GitHub and a client that records its waits and its
// log, and does not sleep.
type retryScene struct {
	fake   *githubtest.Fake
	repo   *githubtest.Repository
	client *github.AppClient
	waits  []time.Duration
	logs   *bytes.Buffer
	url    string
}

func newRetryScene(t *testing.T) *retryScene {
	t.Helper()
	fake, server := githubtest.New(t)
	sc := &retryScene{fake: fake, repo: fake.AddRepository("example-org", "example-repo"), logs: &bytes.Buffer{}, url: server.URL}
	fake.AddIssue(sc.repo, &githubtest.Issue{Number: 12})
	sc.client = github.NewAppClient(server.URL, server.Client())
	sc.client.SetLogger(logger(sc.logs))
	sc.client.SetRetryWait(func(_ context.Context, d time.Duration) error {
		sc.waits = append(sc.waits, d)
		return nil
	})
	return sc
}

func (sc *retryScene) readSnapshot() error {
	_, err := sc.client.ReadSnapshot(context.Background(), githubtest.Token, "example-org", "example-repo")
	return err
}

func (sc *retryScene) readLabels() error {
	_, err := sc.client.EnsureLabels(context.Background(), githubtest.Token, "example-org", "example-repo", nil)
	return err
}

// retryLines returns the warn lines of the retries in the log.
func (sc *retryScene) retryLines() []string {
	var lines []string
	for _, line := range strings.Split(sc.logs.String(), "\n") {
		if strings.Contains(line, "try a call to GitHub again") {
			lines = append(lines, line)
		}
	}
	return lines
}

// A read whose first try gets a 502 and whose second try gets a closed
// connection succeeds at the third try. Each retry waits and logs one line
// at warn level, without the token and without the address.
func TestRetry_AReadSucceedsAfterA502AndAClosedConnection(t *testing.T) {
	sc := newRetryScene(t)
	if err := sc.readSnapshot(); err != nil {
		t.Fatalf("ReadSnapshot without a failure: %v", err)
	}
	usual := sc.fake.CountRequests(http.MethodPost, "/graphql")
	sc.fake.FailTimes(http.MethodPost, "/graphql", 0, 1, http.StatusBadGateway)
	sc.fake.CloseTimes(http.MethodPost, "/graphql", 1)

	if err := sc.readSnapshot(); err != nil {
		t.Fatalf("ReadSnapshot after two temporary failures: %v", err)
	}

	if got := sc.fake.CountRequests(http.MethodPost, "/graphql") - usual; got != usual+2 {
		t.Errorf("%d GraphQL requests, want %d: the usual ones and 2 retries", got, usual+2)
	}
	if len(sc.waits) != 2 || sc.waits[0] <= 0 || sc.waits[0] > 10*time.Second {
		t.Errorf("waits = %v, want 2 waits of a few seconds", sc.waits)
	}
	lines := sc.retryLines()
	if len(lines) != 2 {
		t.Fatalf("%d retry lines, want 2:\n%s", len(lines), sc.logs.String())
	}
	if !strings.Contains(lines[0], "level=WARN") || !strings.Contains(lines[0], `request="POST /graphql"`) ||
		!strings.Contains(lines[0], "try=1") || !strings.Contains(lines[0], `reason="status 502"`) {
		t.Errorf("the first retry line = %s, want warn, the request, try 1 and status 502", lines[0])
	}
	if !strings.Contains(lines[1], "try=2") || !strings.Contains(lines[1], "reason=") {
		t.Errorf("the second retry line = %s, want try 2 and a reason", lines[1])
	}
	host := strings.TrimPrefix(sc.url, "http://")
	if logs := sc.logs.String(); strings.Contains(logs, githubtest.Token) || strings.Contains(logs, host) {
		t.Errorf("the log holds the token or the address:\n%s", logs)
	}
}

// A REST read is tried again as a GraphQL query is.
func TestRetry_ARESTReadSucceedsAfterA5xx(t *testing.T) {
	sc := newRetryScene(t)
	sc.fake.FailTimes(http.MethodGet, labelsPath, 0, 1, http.StatusServiceUnavailable)
	if err := sc.readLabels(); err != nil {
		t.Fatalf("the read after one 503: %v", err)
	}
	if got := sc.fake.CountRequests(http.MethodGet, labelsPath); got != 2 {
		t.Errorf("%d requests, want 2", got)
	}
}

// The job log is read as text, not as JSON. That read is tried again too.
func TestRetry_TheJobLogIsReadAfterA5xx(t *testing.T) {
	sc := newRetryScene(t)
	sc.fake.AddCheckRun(sc.repo, headSHA, githubtest.CheckRun{ID: 7, Name: "ci", Conclusion: "failure", JobID: 42, JobLog: "the reason of the failure\n"})
	const logPath = "/repos/example-org/example-repo/actions/jobs/42/logs"
	sc.fake.FailTimes(http.MethodGet, logPath, 0, 1, http.StatusBadGateway)

	content := sc.client.FailedCheckContent(context.Background(), githubtest.Token,
		"example-org", "example-repo", headSHA, []github.RequiredCheck{{Name: "ci"}}, logger(sc.logs))

	if text := contentOf(t, content, "ci"); !strings.Contains(text, "the reason of the failure") {
		t.Errorf("content = %q, want the job log", text)
	}
	if got := sc.fake.CountRequests(http.MethodGet, logPath); got != 2 {
		t.Errorf("%d requests of the job log, want 2", got)
	}
}

// A read that fails on every try returns a temporary error after the first
// try and 3 retries.
func TestRetry_AReadThatFailsOnEveryTryReturnsATemporaryError(t *testing.T) {
	t.Run("a 5xx answer", func(t *testing.T) {
		sc := newRetryScene(t)
		sc.fake.FailTimes(http.MethodPost, "/graphql", 0, 10, http.StatusBadGateway)
		err := sc.readSnapshot()
		var status *github.StatusError
		if !github.IsTemporary(err) || !errors.As(err, &status) || status.Status != http.StatusBadGateway {
			t.Fatalf("err = %v, want a temporary error with the status 502", err)
		}
		if got := sc.fake.CountRequests(http.MethodPost, "/graphql"); got != everyTry {
			t.Errorf("%d requests, want %d", got, everyTry)
		}
		if len(sc.waits) != everyTry-1 || len(sc.retryLines()) != everyTry-1 {
			t.Errorf("%d waits and %d retry lines, want %d of each", len(sc.waits), len(sc.retryLines()), everyTry-1)
		}
	})
	t.Run("a closed connection", func(t *testing.T) {
		sc := newRetryScene(t)
		sc.fake.CloseTimes(http.MethodPost, "/graphql", 10)
		err := sc.readSnapshot()
		if !github.IsTemporary(err) {
			t.Fatalf("err = %v, want a temporary error", err)
		}
		if strings.Contains(err.Error(), sc.url) {
			t.Errorf("err = %v, holds the address of the request", err)
		}
		if got := sc.fake.CountRequests(http.MethodPost, "/graphql"); got != everyTry {
			t.Errorf("%d requests, want %d", got, everyTry)
		}
	})
}

// A 4xx answer is a lasting failure: the read is sent once, and the error
// is the StatusError as before.
func TestRetry_A4xxAnswerIsSentOnce(t *testing.T) {
	for _, code := range []int{http.StatusNotFound, http.StatusUnprocessableEntity, http.StatusMethodNotAllowed, http.StatusConflict} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			sc := newRetryScene(t)
			sc.fake.FailTimes(http.MethodGet, labelsPath, 0, 10, code)
			err := sc.readLabels()
			var status *github.StatusError
			if !errors.As(err, &status) || status.Status != code || github.IsTemporary(err) {
				t.Fatalf("err = %v, want a lasting StatusError with the status %d", err, code)
			}
			if got := sc.fake.CountRequests(http.MethodGet, labelsPath); got != 1 {
				t.Errorf("%d requests, want 1", got)
			}
			if len(sc.waits) != 0 || len(sc.retryLines()) != 0 {
				t.Errorf("waits = %v, retry lines = %v, want none", sc.waits, sc.retryLines())
			}
		})
	}
}

// A write is sent once, also after a 5xx answer or a closed connection: it
// may have happened. The error says that the failure is temporary.
func TestRetry_AWriteIsSentOnceAndReturnsATemporaryError(t *testing.T) {
	ctx := context.Background()
	t.Run("a REST write with a 502", func(t *testing.T) {
		sc := newRetryScene(t)
		sc.fake.FailTimes(http.MethodPost, commentsPath, 0, 10, http.StatusBadGateway)
		_, err := sc.client.CreateIssueComment(ctx, githubtest.Token, "example-org", "example-repo", 12, "stopped")
		if !github.IsTemporary(err) {
			t.Fatalf("err = %v, want a temporary error", err)
		}
		if got := sc.fake.CountRequests(http.MethodPost, commentsPath); got != 1 {
			t.Errorf("%d requests, want 1", got)
		}
		if len(sc.waits) != 0 {
			t.Errorf("waits = %v, want none", sc.waits)
		}
	})
	t.Run("a REST write with a closed connection", func(t *testing.T) {
		sc := newRetryScene(t)
		sc.fake.CloseTimes(http.MethodPost, commentsPath, 10)
		_, err := sc.client.CreateIssueComment(ctx, githubtest.Token, "example-org", "example-repo", 12, "stopped")
		if !github.IsTemporary(err) {
			t.Fatalf("err = %v, want a temporary error", err)
		}
		if got := sc.fake.CountRequests(http.MethodPost, commentsPath); got != 1 {
			t.Errorf("%d requests, want 1", got)
		}
	})
	t.Run("a GraphQL mutation with a 502", func(t *testing.T) {
		sc := newRetryScene(t)
		sc.fake.AddPullRequest(sc.repo, &githubtest.PullRequest{Number: 21})
		sc.fake.FailTimes(http.MethodPost, "/graphql", 0, 10, http.StatusBadGateway)
		err := sc.client.AddClosingLink(ctx, githubtest.Token, githubtest.IssueNodeID(sc.repo, 12), githubtest.PullRequestNodeID(sc.repo, 21))
		if !github.IsTemporary(err) {
			t.Fatalf("err = %v, want a temporary error", err)
		}
		if got := sc.fake.CountRequests(http.MethodPost, "/graphql"); got != 1 {
			t.Errorf("%d requests, want 1", got)
		}
	})
}

// A context that ends during the wait ends the call at once: no other try
// is sent, and the error is not a temporary failure of GitHub.
func TestRetry_ACancelledContextEndsTheWait(t *testing.T) {
	sc := newRetryScene(t)
	ctx, cancel := context.WithCancel(context.Background())
	sc.client.SetRetryWait(func(ctx context.Context, _ time.Duration) error {
		cancel()
		return ctx.Err()
	})
	sc.fake.FailTimes(http.MethodPost, "/graphql", 0, 10, http.StatusBadGateway)

	_, err := sc.client.ReadSnapshot(ctx, githubtest.Token, "example-org", "example-repo")

	if err == nil || github.IsTemporary(err) || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("err = %v, want the cancelled context", err)
	}
	if got := sc.fake.CountRequests(http.MethodPost, "/graphql"); got != 1 {
		t.Errorf("%d requests, want 1", got)
	}
}
