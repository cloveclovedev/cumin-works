package workflow_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
	"github.com/cloveclovedev/cumin-works/internal/workflow"
)

// fakeCommand writes a script that appends its arguments, on one line, to
// the record file, and exits with exitCode.
func fakeCommand(t *testing.T, exitCode int) (path, record string) {
	t.Helper()
	dir := t.TempDir()
	path = filepath.Join(dir, "fake-request")
	record = filepath.Join(dir, "record")
	// The script also records its environment, so a test can check what
	// reaches the command.
	script := "#!/bin/sh\necho \"$*\" >> " + record + "\nenv > " + record + ".env\nexit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, record
}

// recorded returns the lines of the record file; none when the file does
// not exist.
func recorded(t *testing.T, record string) []string {
	t.Helper()
	data, err := os.ReadFile(record)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

const (
	putLabelsPath = "/repos/example-org/example-repo/issues/10/labels"
)

// scene is the fake with one repository, one requirement issue #6, and one
// ready sub-issue #10 with risk/low.
type scene struct {
	fake    *githubtest.Fake
	client  *github.AppClient
	repo    *githubtest.Repository
	logs    *bytes.Buffer
	command string
	record  string
}

func newScene(t *testing.T, exitCode int) *scene {
	t.Helper()
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddIssue(repo, &githubtest.Issue{Number: 6, Labels: []string{"cumin/type/requirement", "cumin/status/implementing"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 10, Parent: 6, Labels: []string{"cumin/status/ready", "risk/low"}})
	command, record := fakeCommand(t, exitCode)
	return &scene{fake: fake, client: github.NewAppClient(server.URL, server.Client()), repo: repo, logs: &bytes.Buffer{}, command: command, record: record}
}

// service returns a new Service on the scene, as after a restart of cumin.
func (sc *scene) service() *workflow.Service {
	return &workflow.Service{
		GitHub: sc.client,
		Targets: []workflow.Target{{
			Repository: config.Repository{Owner: "example-org", Name: "example-repo"},
			Token:      func(context.Context) (string, error) { return githubtest.Token, nil },
		}},
		MaxIssuesInProgress: 1,
		RequestCommand:      sc.command,
		Logger:              slog.New(slog.NewJSONHandler(sc.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
}

// Core-1 (cumin-core.md): one ready implementation issue, two or more polls,
// one request to the Implementer.
func TestCore01_ReadyIssueIsRequestedOnce(t *testing.T) {
	sc := newScene(t, 0)
	service := sc.service()
	ctx := context.Background()

	for i := range 3 {
		if err := service.Poll(ctx); err != nil {
			t.Fatalf("poll %d: %v", i+1, err)
		}
	}

	if got := recorded(t, sc.record); !slices.Equal(got, []string{"example-org/example-repo 10"}) {
		t.Errorf("the command ran with %q, want once with the repository and the issue number", got)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", "cumin/status/implementing"}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/implementing", got)
	}
	if n := sc.fake.CountRequests(http.MethodPut, putLabelsPath); n != 1 {
		t.Errorf("%d label changes, want 1", n)
	}
	if n := sc.fake.CountRequests(http.MethodPost, "/graphql"); n != 3 {
		t.Errorf("%d snapshot reads, want 3", n)
	}
	if strings.Contains(sc.logs.String(), githubtest.Token) {
		t.Error("the log holds the token")
	}
	for _, want := range []string{`"msg":"poll"`, `"rate_limit_cost"`, `"rate_limit_remaining"`, `"msg":"I1: claimed the issue"`, `"msg":"I1: requested the work"`} {
		if !strings.Contains(sc.logs.String(), want) {
			t.Errorf("the log has no %s", want)
		}
	}
}

// Core-8 (cumin-core.md): stop cumin and start it again; the same issue is
// not requested twice.
func TestCore08_RestartDoesNotRequestTwice(t *testing.T) {
	sc := newScene(t, 0)
	ctx := context.Background()

	if err := sc.service().Poll(ctx); err != nil {
		t.Fatalf("first run: %v", err)
	}
	// A new Service holds nothing from the first one. The facts are on
	// GitHub: the label of #10 is now cumin/status/implementing.
	if err := sc.service().Poll(ctx); err != nil {
		t.Fatalf("second run: %v", err)
	}

	if got := recorded(t, sc.record); !slices.Equal(got, []string{"example-org/example-repo 10"}) {
		t.Errorf("the command ran with %q, want once", got)
	}
}

func TestPoll_FailedLabelChangeRunsNoCommand(t *testing.T) {
	sc := newScene(t, 0)
	service := sc.service()
	ctx := context.Background()

	sc.fake.FailNext(http.MethodPut, putLabelsPath, http.StatusInternalServerError)
	err := service.Poll(ctx)
	if err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("err = %v, want the failed label change", err)
	}
	if got := recorded(t, sc.record); got != nil {
		t.Errorf("the command ran with %q after a failed label change", got)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, "cumin/status/ready") {
		t.Errorf("labels of #10 = %v, want cumin/status/ready still", got)
	}

	// The next poll decides again from the facts and claims the issue.
	if err := service.Poll(ctx); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	if got := recorded(t, sc.record); !slices.Equal(got, []string{"example-org/example-repo 10"}) {
		t.Errorf("the command ran with %q, want once", got)
	}
}

func TestPoll_FailedCommandIsLoggedAndNotRunAgain(t *testing.T) {
	sc := newScene(t, 1)
	service := sc.service()
	ctx := context.Background()

	err := service.Poll(ctx)
	if err == nil || !strings.Contains(err.Error(), "exit status 1") {
		t.Fatalf("err = %v, want the failed command", err)
	}
	if !strings.Contains(sc.logs.String(), "exit status 1") {
		t.Error("the log has no line for the failed command")
	}
	// The issue is claimed. v0.1 has no recovery for it (issue-states.md).
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, "cumin/status/implementing") {
		t.Errorf("labels of #10 = %v", got)
	}
	if err := service.Poll(ctx); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	if got := recorded(t, sc.record); len(got) != 1 {
		t.Errorf("the command ran %d times, want 1", len(got))
	}
}

func TestPoll_CommandGetsNoCredentialOfTheHost(t *testing.T) {
	sc := newScene(t, 0)
	// Credentials in the environment of cumin, and one variable of the list.
	for name, value := range map[string]string{"GH_TOKEN": "gho_hostToken", "GITHUB_TOKEN": "ghp_hostToken", "ANTHROPIC_API_KEY": "sk-host", "SSH_AUTH_SOCK": "/tmp/agent.sock", "LANG": "en_US.UTF-8"} {
		t.Setenv(name, value)
	}

	if err := sc.service().Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	env, err := os.ReadFile(sc.record + ".env")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN", "ANTHROPIC_API_KEY", "SSH_AUTH_SOCK"} {
		if strings.Contains(string(env), name+"=") {
			t.Errorf("the command got %s", name)
		}
	}
	for _, want := range []string{"PATH=", "LANG=en_US.UTF-8"} {
		if !strings.Contains(string(env), want) {
			t.Errorf("the command did not get %s", want)
		}
	}
	if strings.Contains(sc.logs.String(), "hostToken") {
		t.Error("the log holds a credential of the Host")
	}
}

func TestPoll_EmptyRequestCommandLogsAndRunsNothing(t *testing.T) {
	sc := newScene(t, 0)
	service := sc.service()
	service.RequestCommand = ""

	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, "cumin/status/implementing") {
		t.Errorf("labels of #10 = %v", got)
	}
	if got := recorded(t, sc.record); got != nil {
		t.Errorf("the command ran with %q", got)
	}
	if !strings.Contains(sc.logs.String(), "no request command is configured") {
		t.Error("the log does not say that no command is configured")
	}
}

func TestPoll_OneFailedRepositoryDoesNotStopTheOthers(t *testing.T) {
	sc := newScene(t, 0)
	service := sc.service()
	// A repository that the fake does not have, before the good one.
	missing := workflow.Target{
		Repository: config.Repository{Owner: "example-org", Name: "missing-repo"},
		Token:      func(context.Context) (string, error) { return githubtest.Token, nil },
	}
	service.Targets = append([]workflow.Target{missing}, service.Targets...)

	err := service.Poll(context.Background())
	if err == nil || !strings.Contains(err.Error(), "missing-repo") {
		t.Fatalf("err = %v, want the failure of missing-repo", err)
	}
	if got := recorded(t, sc.record); !slices.Equal(got, []string{"example-org/example-repo 10"}) {
		t.Errorf("the command ran with %q, want once for the good repository", got)
	}
}

func TestRun_CreatesTheLabelsOnceAndPollsAtTheInterval(t *testing.T) {
	sc := newScene(t, 0)
	service := sc.service()
	service.PollInterval = 10 * time.Millisecond
	service.Labels = workflow.RepositoryLabels()
	// The fake answers the first snapshot read with 500: a failed poll does
	// not stop the loop.
	sc.fake.FailNext(http.MethodPost, "/graphql", http.StatusInternalServerError)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for sc.fake.CountRequests(http.MethodPost, "/graphql") < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return within one second after the cancel")
	}

	if n := sc.fake.CountRequests(http.MethodPost, "/graphql"); n < 3 {
		t.Errorf("%d snapshot reads, want 3 or more", n)
	}
	if n := sc.fake.CountRequests(http.MethodPost, "/repos/example-org/example-repo/labels"); n != 11 {
		t.Errorf("%d labels created, want 11", n)
	}
	if got := sc.fake.LabelNames(sc.repo); len(got) != 11 || !slices.Contains(got, "cumin/status/ready") {
		t.Errorf("labels of the repository = %v", got)
	}
	if got := recorded(t, sc.record); !slices.Equal(got, []string{"example-org/example-repo 10"}) {
		t.Errorf("the command ran with %q, want once", got)
	}
	for _, want := range []string{`"msg":"created the label"`, `"msg":"poll failed"`, `"msg":"stopped"`} {
		if !strings.Contains(sc.logs.String(), want) {
			t.Errorf("the log has no %s", want)
		}
	}

	// A second start creates nothing: every label exists.
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	second := sc.service()
	second.PollInterval, second.Labels = service.PollInterval, service.Labels
	if err := second.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if n := sc.fake.CountRequests(http.MethodPost, "/repos/example-org/example-repo/labels"); n != 11 {
		t.Errorf("%d labels created after the second start, want 11 still", n)
	}
}

func TestRun_RejectsAnIntervalOfZero(t *testing.T) {
	service := newScene(t, 0).service()
	if err := service.Run(context.Background()); err == nil {
		t.Error("Run with no interval returned nil")
	}
}
