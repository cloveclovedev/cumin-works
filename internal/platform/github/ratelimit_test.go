package github_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
)

var (
	limitFound = time.Date(2026, 10, 3, 11, 20, 0, 0, time.UTC)
	limitReset = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
)

// limitScene is a retryScene whose client reads a clock that the test moves.
type limitScene struct {
	*retryScene
	now time.Time
}

func newLimitScene(t *testing.T) *limitScene {
	t.Helper()
	sc := &limitScene{retryScene: newRetryScene(t), now: limitFound}
	sc.client.SetNowForTest(func() time.Time { return sc.now })
	return sc
}

func (sc *limitScene) writeComment() error {
	_, err := sc.client.CreateIssueComment(context.Background(), githubtest.Token, "example-org", "example-repo", 12, "stopped")
	return err
}

// limitLines returns the warn lines of a full rate limit in the log.
func (sc *limitScene) limitLines() []string {
	var lines []string
	for _, line := range strings.Split(sc.logs.String(), "\n") {
		if strings.Contains(line, "the rate limit of GitHub is full") {
			lines = append(lines, line)
		}
	}
	return lines
}

// wantRateLimit fails the test unless err is the typed error of a full rate
// limit with the resource and the reset time, and counts as temporary.
func wantRateLimit(t *testing.T, err error, resource string) {
	t.Helper()
	var limit *github.RateLimitError
	if !errors.As(err, &limit) {
		t.Fatalf("err = %v, want a RateLimitError", err)
	}
	if !limit.Reset.Equal(limitReset) || limit.Resource != resource {
		t.Errorf("the limit = %s until %s, want %s until %s", limit.Resource, limit.Reset, resource, limitReset)
	}
	if !github.IsTemporary(err) {
		t.Errorf("err = %v, want a temporary error", err)
	}
	if strings.Contains(err.Error(), githubtest.Token) {
		t.Errorf("err = %v, holds the token", err)
	}
}

// A REST answer of a full primary rate limit returns the typed error with
// the reset time. The read is not tried again at once. Before the reset
// time the fake receives no call; after it, the next call is sent.
func TestRateLimit_ARESTCallIsNotSentBeforeTheReset(t *testing.T) {
	for _, code := range []int{http.StatusForbidden, http.StatusTooManyRequests} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			sc := newLimitScene(t)
			sc.fake.LimitTimes(http.MethodGet, labelsPath, 1, code, limitReset)

			wantRateLimit(t, sc.readLabels(), "core")

			if got := sc.fake.CountRequests(http.MethodGet, labelsPath); got != 1 {
				t.Errorf("%d requests, want 1: a full rate limit is not tried again at once", got)
			}
			if len(sc.waits) != 0 || len(sc.retryLines()) != 0 {
				t.Errorf("waits = %v, retry lines = %v, want none", sc.waits, sc.retryLines())
			}
			lines := sc.limitLines()
			if len(lines) != 1 || !strings.Contains(lines[0], "level=WARN") ||
				!strings.Contains(lines[0], "resource=core") || !strings.Contains(lines[0], "reset=2026-10-03T12:00:00Z") {
				t.Errorf("limit lines = %v, want one warn line with the resource and the reset time", lines)
			}
			if strings.Contains(sc.logs.String(), githubtest.Token) {
				t.Errorf("the log holds the token:\n%s", sc.logs.String())
			}

			sc.now = limitReset.Add(-time.Second)
			received := len(sc.fake.Requests())
			wantRateLimit(t, sc.readLabels(), "core")
			wantRateLimit(t, sc.writeComment(), "core")
			wantRateLimit(t, sc.readSnapshot(), "core")
			if got := len(sc.fake.Requests()); got != received {
				t.Errorf("%d requests before the reset time, want none", got-received)
			}
			if got := len(sc.limitLines()); got != 1 {
				t.Errorf("%d limit lines, want 1: a call that is not sent logs nothing", got)
			}

			sc.now = limitReset
			if err := sc.readLabels(); err != nil {
				t.Fatalf("the read at the reset time: %v", err)
			}
			if got := sc.fake.CountRequests(http.MethodGet, labelsPath); got != 2 {
				t.Errorf("%d requests, want 2: the read at the reset time is sent", got)
			}
		})
	}
}

// GraphQL answers a full primary rate limit with the status 200 and an
// error. The client returns the same typed error and sends no call before
// the reset time.
func TestRateLimit_AGraphQLCallIsNotSentBeforeTheReset(t *testing.T) {
	sc := newLimitScene(t)
	sc.fake.LimitTimes(http.MethodPost, "/graphql", 1, 0, limitReset)

	wantRateLimit(t, sc.readSnapshot(), "graphql")

	if got := sc.fake.CountRequests(http.MethodPost, "/graphql"); got != 1 {
		t.Errorf("%d requests, want 1: a full rate limit is not tried again at once", got)
	}
	if len(sc.waits) != 0 {
		t.Errorf("waits = %v, want none", sc.waits)
	}
	if lines := sc.limitLines(); len(lines) != 1 || !strings.Contains(lines[0], "resource=graphql") {
		t.Errorf("limit lines = %v, want one line with the resource graphql", lines)
	}

	sc.now = limitReset.Add(-time.Second)
	received := len(sc.fake.Requests())
	wantRateLimit(t, sc.readSnapshot(), "graphql")
	wantRateLimit(t, sc.readLabels(), "graphql")
	if got := len(sc.fake.Requests()); got != received {
		t.Errorf("%d requests before the reset time, want none", got-received)
	}

	sc.now = limitReset.Add(time.Second)
	if err := sc.readSnapshot(); err != nil {
		t.Fatalf("ReadSnapshot after the reset time: %v", err)
	}
}

// The job log is read as text. That read finds a full rate limit too.
func TestRateLimit_TheJobLogReadFindsAFullRateLimit(t *testing.T) {
	sc := newLimitScene(t)
	sc.fake.AddCheckRun(sc.repo, headSHA, githubtest.CheckRun{ID: 7, Name: "ci", Conclusion: "failure", JobID: 42, JobLog: "the reason of the failure\n"})
	const logPath = "/repos/example-org/example-repo/actions/jobs/42/logs"
	sc.fake.LimitTimes(http.MethodGet, logPath, 1, http.StatusForbidden, limitReset)

	sc.client.FailedCheckContent(context.Background(), githubtest.Token,
		"example-org", "example-repo", headSHA, []github.RequiredCheck{{Name: "ci"}}, logger(sc.logs))

	if got := sc.fake.CountRequests(http.MethodGet, logPath); got != 1 {
		t.Errorf("%d requests of the job log, want 1", got)
	}
	received := len(sc.fake.Requests())
	wantRateLimit(t, sc.readLabels(), "core")
	if got := len(sc.fake.Requests()); got != received {
		t.Errorf("%d requests before the reset time, want none", got-received)
	}
}

// A 403 without the rate limit signals is a lasting failure as before: a
// StatusError, sent once, and the next call is sent.
func TestRateLimit_A403WithoutTheSignalsStaysAStatusError(t *testing.T) {
	sc := newLimitScene(t)
	sc.fake.FailTimes(http.MethodGet, labelsPath, 0, 1, http.StatusForbidden)

	err := sc.readLabels()

	var status *github.StatusError
	var limit *github.RateLimitError
	if !errors.As(err, &status) || status.Status != http.StatusForbidden || errors.As(err, &limit) || github.IsTemporary(err) {
		t.Fatalf("err = %v, want a lasting StatusError with the status 403", err)
	}
	if got := sc.fake.CountRequests(http.MethodGet, labelsPath); got != 1 {
		t.Errorf("%d requests, want 1", got)
	}
	if len(sc.limitLines()) != 0 {
		t.Errorf("limit lines = %v, want none", sc.limitLines())
	}
	if err := sc.readLabels(); err != nil {
		t.Fatalf("the next read: %v", err)
	}
	if got := sc.fake.CountRequests(http.MethodGet, labelsPath); got != 2 {
		t.Errorf("%d requests, want 2: the next read is sent", got)
	}
}

// Two tokens of one installation share the rate limit: the second token
// sends nothing. A token of another installation is sent.
func TestRateLimit_AnotherTokenOfTheSameInstallationIsNotSent(t *testing.T) {
	sc := newLimitScene(t)
	expiry := limitFound.Add(time.Hour)
	sc.client.RememberInstallationForTest(githubtest.Token, 7, expiry)
	sc.client.RememberInstallationForTest("second-token", 7, expiry)
	sc.client.RememberInstallationForTest("other-token", 8, expiry)
	sc.fake.LimitTimes(http.MethodGet, labelsPath, 1, http.StatusForbidden, limitReset)
	wantRateLimit(t, sc.readLabels(), "core")

	ctx := context.Background()
	_, err := sc.client.EnsureLabels(ctx, "second-token", "example-org", "example-repo", nil)
	wantRateLimit(t, err, "core")
	if got := sc.fake.CountRequests(http.MethodGet, labelsPath); got != 1 {
		t.Errorf("%d requests, want 1: the second token of the installation sends nothing", got)
	}

	// The fake knows one token only, so it answers the other one with 401.
	_, err = sc.client.EnsureLabels(ctx, "other-token", "example-org", "example-repo", nil)
	var status *github.StatusError
	if !errors.As(err, &status) || status.Status != http.StatusUnauthorized {
		t.Fatalf("err = %v, want the answer 401 of the fake", err)
	}
	if got := sc.fake.CountRequests(http.MethodGet, labelsPath); got != 2 {
		t.Errorf("%d requests, want 2: the token of another installation is sent", got)
	}
}

// The last GraphQL call that the limit allows has the header with 0 and no
// error. It succeeds, and the next call is sent.
func TestRateLimit_TheLastGraphQLCallOfTheLimitSucceeds(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("x-ratelimit-remaining", "0")
		w.Header().Set("x-ratelimit-reset", "1791028800")
		w.Header().Set("x-ratelimit-resource", "graphql")
		_, _ = w.Write([]byte(`{"data":{"repository":{"issue":{"closedByPullRequestsReferences":{"nodes":[]}}}}}`))
	}))
	t.Cleanup(server.Close)
	client := github.NewAppClient(server.URL, server.Client())
	client.SetNowForTest(func() time.Time { return limitFound })

	for try := 1; try <= 2; try++ {
		if _, _, err := client.ReadLinkedPullRequests(context.Background(), githubtest.Token, "example-org", "example-repo", 12); err != nil {
			t.Fatalf("call %d: %v", try, err)
		}
	}
	if got := requests.Load(); got != 2 {
		t.Errorf("%d requests, want 2", got)
	}
}

// secondaryLines returns the warn lines of a secondary rate limit in the log.
func (sc *limitScene) secondaryLines() []string {
	var lines []string
	for _, line := range strings.Split(sc.logs.String(), "\n") {
		if strings.Contains(line, "GitHub answered a secondary rate limit") {
			lines = append(lines, line)
		}
	}
	return lines
}

// wantSecondaryLimit fails the test unless err is the typed error of a
// secondary rate limit with the end of the wait, and counts as temporary.
func wantSecondaryLimit(t *testing.T, err error, until time.Time) {
	t.Helper()
	var limit *github.RateLimitError
	if !errors.As(err, &limit) {
		t.Fatalf("err = %v, want a RateLimitError", err)
	}
	if !limit.Reset.Equal(until) || limit.Resource != "secondary" {
		t.Errorf("the limit = %s until %s, want secondary until %s", limit.Resource, limit.Reset, until)
	}
	if !github.IsTemporary(err) {
		t.Errorf("err = %v, want a temporary error", err)
	}
	if strings.Contains(err.Error(), githubtest.Token) {
		t.Errorf("err = %v, holds the token", err)
	}
}

// An answer of a secondary rate limit returns the typed error at once, for
// REST and for GraphQL: the fake receives one call, and the client does not
// wait. With retry-after: 30 the fake receives no call of the installation
// for 30 seconds; without the header, for 1 minute. The next call after
// that is sent.
func TestRateLimit_NoCallIsSentDuringASecondaryRateLimit(t *testing.T) {
	for _, tc := range []struct {
		name, method, path string
		status             int
		retryAfter, pause  time.Duration
	}{
		{"REST 403 with retry-after", http.MethodGet, labelsPath, http.StatusForbidden, 30 * time.Second, 30 * time.Second},
		{"REST 429 with retry-after", http.MethodGet, labelsPath, http.StatusTooManyRequests, 30 * time.Second, 30 * time.Second},
		{"REST 403 without retry-after", http.MethodGet, labelsPath, http.StatusForbidden, 0, time.Minute},
		{"GraphQL 200 with retry-after", http.MethodPost, "/graphql", http.StatusOK, 30 * time.Second, 30 * time.Second},
		{"GraphQL 200 without retry-after", http.MethodPost, "/graphql", http.StatusOK, 0, time.Minute},
		{"GraphQL 403 without retry-after", http.MethodPost, "/graphql", http.StatusForbidden, 0, time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc := newLimitScene(t)
			sc.fake.SecondaryLimitTimes(tc.method, tc.path, 1, tc.status, tc.retryAfter)
			call := sc.readLabels
			if tc.path == "/graphql" {
				call = sc.readSnapshot
			}
			until := limitFound.Add(tc.pause)

			wantSecondaryLimit(t, call(), until)

			if got := sc.fake.CountRequests(tc.method, tc.path); got != 1 {
				t.Errorf("%d requests, want 1: a secondary rate limit is not tried again inside the call", got)
			}
			if len(sc.waits) != 0 || len(sc.retryLines()) != 0 {
				t.Errorf("waits = %v, retry lines = %v, want none", sc.waits, sc.retryLines())
			}
			lines := sc.secondaryLines()
			if len(lines) != 1 || !strings.Contains(lines[0], "level=WARN") ||
				!strings.Contains(lines[0], "resource=secondary") || !strings.Contains(lines[0], "reset="+until.Format(time.RFC3339)) {
				t.Errorf("secondary lines = %v, want one warn line with the kind and the time", lines)
			}
			if strings.Contains(sc.logs.String(), githubtest.Token) {
				t.Errorf("the log holds the token:\n%s", sc.logs.String())
			}

			sc.now = until.Add(-time.Second)
			received := len(sc.fake.Requests())
			wantSecondaryLimit(t, sc.readLabels(), until)
			wantSecondaryLimit(t, sc.writeComment(), until)
			wantSecondaryLimit(t, sc.readSnapshot(), until)
			if got := len(sc.fake.Requests()); got != received {
				t.Errorf("%d requests during the pause, want none", got-received)
			}
			if got := len(sc.secondaryLines()); got != 1 {
				t.Errorf("%d secondary lines, want 1: a call that is not sent logs nothing", got)
			}

			sc.now = until
			if err := call(); err != nil {
				t.Fatalf("the call after the pause: %v", err)
			}
			if got := len(sc.fake.Requests()); got == received {
				t.Errorf("no request after the pause, want the call to be sent")
			}
		})
	}
}

// The job log is read as text. That read finds a secondary rate limit too.
func TestRateLimit_TheJobLogReadFindsASecondaryRateLimit(t *testing.T) {
	sc := newLimitScene(t)
	sc.fake.AddCheckRun(sc.repo, headSHA, githubtest.CheckRun{ID: 7, Name: "ci", Conclusion: "failure", JobID: 42, JobLog: "the reason of the failure\n"})
	const logPath = "/repos/example-org/example-repo/actions/jobs/42/logs"
	sc.fake.SecondaryLimitTimes(http.MethodGet, logPath, 1, http.StatusForbidden, 0)

	sc.client.FailedCheckContent(context.Background(), githubtest.Token,
		"example-org", "example-repo", headSHA, []github.RequiredCheck{{Name: "ci"}}, logger(sc.logs))

	if got := sc.fake.CountRequests(http.MethodGet, logPath); got != 1 {
		t.Errorf("%d requests of the job log, want 1", got)
	}
	received := len(sc.fake.Requests())
	wantSecondaryLimit(t, sc.readLabels(), limitFound.Add(time.Minute))
	if got := len(sc.fake.Requests()); got != received {
		t.Errorf("%d requests during the pause, want none", got-received)
	}
}

// A write that gets a secondary rate limit returns the typed error, and
// the pause holds a later read too.
func TestRateLimit_AWriteFindsASecondaryRateLimit(t *testing.T) {
	sc := newLimitScene(t)
	sc.fake.SecondaryLimitTimes(http.MethodPost, commentsPath, 1, http.StatusForbidden, 30*time.Second)
	until := limitFound.Add(30 * time.Second)

	wantSecondaryLimit(t, sc.writeComment(), until)

	received := len(sc.fake.Requests())
	wantSecondaryLimit(t, sc.readLabels(), until)
	if got := len(sc.fake.Requests()); got != received {
		t.Errorf("%d requests during the pause, want none", got-received)
	}
}
