package workflow_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/agent"
	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
	"github.com/cloveclovedev/cumin-works/internal/workflow"
)

// The tests of the poll run the real GitHub client against the fake GitHub
// (githubtest), the real worktree code against a local bare repository, and
// the real CLI adapter against a fake CLI that answers from the fixtures of
// internal/agent.

// The token of the Implementer App reaches the CLI, and no credential of
// the Host does (agent-run.md, the topic on the agent environment).
func TestPoll_TheAgentGetsItsTokenAndNoCredentialOfTheHost(t *testing.T) {
	sc := newScene(t)
	for name, value := range map[string]string{
		"GH_TOKEN": "gho_hostToken", "GITHUB_TOKEN": "ghp_hostToken",
		"ANTHROPIC_API_KEY": "sk-host", "SSH_AUTH_SOCK": "/tmp/agent.sock",
	} {
		t.Setenv(name, value)
	}
	service := sc.service()
	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	service.Wait()

	env := sc.environment(t)
	if env["GH_TOKEN"] != githubtest.Token {
		t.Errorf("GH_TOKEN = %q, want the token of the Implementer App", env["GH_TOKEN"])
	}
	basic := strings.TrimPrefix(env["GIT_CONFIG_VALUE_0"], "Authorization: Basic ")
	if decoded, err := base64.StdEncoding.DecodeString(basic); err != nil || string(decoded) != "x-access-token:"+githubtest.Token {
		t.Errorf("GIT_CONFIG_VALUE_0 = %q, want the token", env["GIT_CONFIG_VALUE_0"])
	}
	if got := env["GIT_AUTHOR_NAME"]; got != implementerSlug+"[bot]" {
		t.Errorf("GIT_AUTHOR_NAME = %q, want the bot of the Implementer App", got)
	}
	for _, name := range []string{"GITHUB_TOKEN", "ANTHROPIC_API_KEY", "SSH_AUTH_SOCK"} {
		if _, ok := env[name]; ok {
			t.Errorf("the CLI got %s of the Host", name)
		}
	}
	if strings.Contains(sc.logs.String(), "hostToken") {
		t.Error("the log holds a credential of the Host")
	}
}

func TestPoll_FailedLabelChangeStartsNoAgent(t *testing.T) {
	sc := newScene(t)
	service := sc.service()
	ctx := context.Background()

	sc.fake.FailNext(http.MethodPut, putLabelsPath, http.StatusInternalServerError)
	err := service.Poll(ctx)
	if err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("err = %v, want the failed label change", err)
	}
	service.Wait()
	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs after a failed label change, want 0", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, "cumin/status/ready") {
		t.Errorf("labels of #10 = %v, want cumin/status/ready still", got)
	}

	// The next poll decides again from the facts and claims the issue.
	if err := service.Poll(ctx); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	service.Wait()
	if n := sc.agentRuns(t); n != 2 {
		t.Errorf("%d agent runs, want 2 (the request and the second request)", n)
	}
}

// A work directory that cannot be prepared is logged with the issue number.
// The issue keeps cumin/status/implementing: v0.1 has no rule that takes it
// back (issue-states.md, the section on what v0.1 does not build).
func TestPoll_AFailedWorkDirectoryIsLoggedAndTheLabelStays(t *testing.T) {
	sc := newScene(t)
	service := sc.service()
	service.Targets[0].RemoteURL = filepath.Join(t.TempDir(), "no-such-repository.git")

	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	service.Wait()

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want 0", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, "cumin/status/implementing") {
		t.Errorf("labels of #10 = %v, want cumin/status/implementing", got)
	}
	logs := sc.logs.String()
	if !strings.Contains(logs, `"msg":"request the implementation: the work directory was not prepared"`) || !strings.Contains(logs, `"issue":10`) {
		t.Errorf("the log does not name the failure and the issue:\n%s", logs)
	}
}

func TestPoll_OneFailedRepositoryDoesNotStopTheOthers(t *testing.T) {
	sc := newScene(t)
	service := sc.service()
	// A repository that the fake does not have, before the good one.
	missing := workflow.Target{
		Repository: config.Repository{Owner: "example-org", Name: "missing-repo"},
		RemoteURL:  sc.remote,
		Token:      func(context.Context) (string, error) { return githubtest.Token, nil },
	}
	service.Targets = append([]workflow.Target{missing}, service.Targets...)

	err := service.Poll(context.Background())
	if err == nil || !strings.Contains(err.Error(), "missing-repo") {
		t.Fatalf("err = %v, want the failure of missing-repo", err)
	}
	service.Wait()
	if n := sc.agentRuns(t); n != 2 {
		t.Errorf("%d agent runs, want 2 for the good repository (the request and the second request)", n)
	}
}

// A stalled connection: the fake never answers the snapshot read. The
// timeout of the client ends the call, the poll logs the failed read, and
// the next poll reads the repository.
func TestPoll_StalledGitHubCallEndsAtTheTimeoutAndTheNextPollRuns(t *testing.T) {
	sc := newScene(t)
	// The stalled poll runs with a short timeout, so that the test stays fast.
	httpClient := &http.Client{Timeout: 300 * time.Millisecond}
	sc.client = github.NewAppClient(sc.serverURL, httpClient)
	sc.client.SetRetryWait(noWait)
	service := sc.service()
	// Every try of the read stalls.
	sc.fake.HangTimes(http.MethodPost, "/graphql", everyTry)

	done := make(chan error, 1)
	go func() { done <- service.Poll(context.Background()) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "Client.Timeout") {
			t.Fatalf("err = %v, want the timeout of the client", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Poll did not return after the timeout of the client")
	}
	service.Wait()
	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs after the stalled poll, want 0", n)
	}
	logs := sc.logs.String()
	if !strings.Contains(logs, `"msg":"poll failed"`) || !strings.Contains(logs, "Client.Timeout") {
		t.Errorf("the log has no failed poll with the timeout:\n%s", logs)
	}

	// The next poll must succeed on a loaded machine too, so its timeout is
	// only a guard against a hang. No request runs at this moment.
	httpClient.Timeout = hangGuard
	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("the next Poll: %v", err)
	}
	service.Wait()
	if n := sc.agentRuns(t); n != 2 {
		t.Errorf("%d agent runs after the next poll, want 2 (the request and the second request)", n)
	}
}

// A temporary failure of GitHub: the fake answers the snapshot read once
// with 502. The client sends the read again, and the poll goes on as if the
// read had not failed.
func TestPoll_AReadThatFailsOnceIsSentAgainAndThePollGoesOn(t *testing.T) {
	sc := newScene(t)
	service := sc.service()
	sc.fake.FailNext(http.MethodPost, "/graphql", http.StatusBadGateway)

	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	service.Wait()

	if n := sc.agentRuns(t); n != 2 {
		t.Errorf("%d agent runs, want 2 (the request and the second request): the poll goes on after the retry", n)
	}
}

func TestRun_CreatesTheLabelsOnceAndPollsAtTheInterval(t *testing.T) {
	sc := newScene(t)
	service := sc.service()
	service.PollInterval = 10 * time.Millisecond
	service.Labels = workflow.RepositoryLabels()
	// The fake answers every try of the first snapshot read with 500: a
	// failed poll does not stop the loop.
	sc.fake.FailTimes(http.MethodPost, "/graphql", 0, everyTry, http.StatusInternalServerError)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()

	// The first poll sends the failed read on every try. The second poll
	// sends two queries. The GraphQL request after them is the read of the
	// login of the Issue Owner in the second poll, before the claim. The one
	// after it comes after the claim.
	sc.fake.WaitForRequests(http.MethodPost, "/graphql", everyTry+4, hangGuard)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run: %v", err)
		}
	case <-time.After(hangGuard):
		t.Fatal("Run did not return after the cancel")
	}

	if n := sc.fake.CountRequests(http.MethodPost, "/graphql"); n < 3 {
		t.Errorf("%d snapshot reads, want 3 or more", n)
	}
	// The 16 labels of the start, and the 4 default priority labels of the
	// first poll: the settings of the repository name no priority labels.
	if n := sc.fake.CountRequests(http.MethodPost, "/repos/example-org/example-repo/labels"); n != 20 {
		t.Errorf("%d labels created, want 20", n)
	}
	if got := sc.fake.LabelNames(sc.repo); len(got) != 20 || !slices.Contains(got, "cumin/status/ready") || !slices.Contains(got, "cumin/priority/P0") {
		t.Errorf("labels of the repository = %v", got)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, "cumin/status/implementing") {
		t.Errorf("labels of #10 = %v, want the claim of the second poll", got)
	}
	logs := sc.logs.String()
	for _, want := range []string{`"msg":"created the label"`, `"msg":"poll failed"`, `"msg":"stopped"`} {
		if !strings.Contains(logs, want) {
			t.Errorf("the log has no %s", want)
		}
	}
	// Run waits for the agents before it says that it stopped. The cancel
	// ends the run, so how far the run came is not fixed; whichever line
	// ends the goroutine must come before the stop line.
	stopped := strings.Index(logs, `"msg":"stopped"`)
	end := -1
	for _, msg := range []string{
		`"msg":"the agent run ended"`,
		`"msg":"the agent run ended abnormally"`,
		`"msg":"the agent was not started"`,
		`"msg":"request the implementation: the work directory was not prepared"`,
	} {
		if i := strings.Index(logs, msg); i >= 0 && (end < 0 || i < end) {
			end = i
		}
	}
	if end < 0 || end > stopped {
		t.Errorf("the stop line does not come after the end of the agent run:\n%s", logs)
	}

	// A second start creates nothing: every label exists.
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	second := sc.service()
	second.PollInterval, second.Labels = service.PollInterval, service.Labels
	if err := second.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if n := sc.fake.CountRequests(http.MethodPost, "/repos/example-org/example-repo/labels"); n != 20 {
		t.Errorf("%d labels created after the second start, want 20 still", n)
	}
}

func TestRun_RejectsAnIntervalOfZero(t *testing.T) {
	service := newScene(t).service()
	if err := service.Run(context.Background()); err == nil {
		t.Error("Run with no interval returned nil")
	}
}

// A repository decides a few settings in .cumin/config.toml on its default
// branch. The poll applies them over the Host settings, and the settings of
// the role reach the agent run.
func TestPoll_AppliesTheSettingsOfTheRepository(t *testing.T) {
	sc := newScene(t)
	sc.fake.SetFile(sc.repo, ".cumin/config.toml", githubtest.File{
		Content: "max_review_rounds = 2\nmerge_method = \"rebase\"\nprotected_paths = [\".cumin/\"]\n\n[roles.implementer]\nmodel = \"sonnet\"\n",
	})
	service := sc.service()

	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	service.Wait()

	if n := sc.agentRuns(t); n != 2 {
		t.Fatalf("%d agent runs, want 2 (the request and the second request)", n)
	}
	// The model of the repository reached the CLI of the agent. The
	// arguments are separated by NUL.
	if args := sc.record(t, "agent.args"); !strings.Contains(args, "--model\x00sonnet\x00") {
		t.Errorf("the agent did not run with the model of the repository:\n%q", args)
	}
	logs := sc.logs.String()
	for _, want := range []string{`"settings":"repository"`, `"risk_criteria":"default"`} {
		if !strings.Contains(logs, want) {
			t.Errorf("the poll log does not hold %s:\n%s", want, logs)
		}
	}
}

// A repository that has no .cumin/config.toml runs with the Host settings.
func TestPoll_WithoutTheFileUsesTheHostSettings(t *testing.T) {
	sc := newScene(t)
	service := sc.service()

	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	service.Wait()

	if args := sc.record(t, "agent.args"); strings.Contains(args, "--model\x00") {
		t.Errorf("the agent ran with a model, want the Host settings (none):\n%q", args)
	}
	if logs := sc.logs.String(); !strings.Contains(logs, `"settings":"host"`) {
		t.Errorf("the poll log does not say that the settings are the Host ones:\n%s", logs)
	}
}

// A key that a repository may not set is an error of that repository: it is
// logged with the key name and nothing is claimed there, while the other
// repositories go on.
func TestPoll_AWrongRepositoryFileSkipsOnlyThatRepository(t *testing.T) {
	sc := newScene(t)
	// A second repository with a ready sub-issue, whose file names a key of
	// the Host.
	other := sc.fake.AddRepository("example-org", "other-repo")
	sc.fake.AddIssue(other, &githubtest.Issue{Number: 1, Labels: []string{githubtest.RequirementLabel}})
	sc.fake.AddIssue(other, &githubtest.Issue{Number: 2, Parent: 1, Title: subIssueTitle, Labels: []string{"cumin/status/ready", "risk/low"}})
	sc.fake.SetFile(other, ".cumin/config.toml", githubtest.File{Content: "work_dir = \"/tmp/elsewhere\"\nmax_review_rounds = 2\n"})

	service := sc.service()
	service.Targets = append([]workflow.Target{{
		Repository: config.Repository{Owner: "example-org", Name: "other-repo"},
		RemoteURL:  sc.remote,
		Token:      func(context.Context) (string, error) { return githubtest.Token, nil },
	}}, service.Targets...)

	err := service.Poll(context.Background())
	if err == nil || !strings.Contains(err.Error(), "work_dir") || !strings.Contains(err.Error(), ".cumin/config.toml") {
		t.Fatalf("err = %v, want the file and the key", err)
	}
	service.Wait()

	// The good repository claimed its issue; the wrong one claimed nothing.
	if n := sc.agentRuns(t); n != 2 {
		t.Errorf("%d agent runs, want 2 for the repository whose file is right (the request and the second request)", n)
	}
	if got := sc.fake.Issue(other, 2).Labels; !slices.Contains(got, "cumin/status/ready") {
		t.Errorf("labels of the sub-issue of the wrong repository = %v, want the ready label untouched", got)
	}
	logs := sc.logs.String()
	if !strings.Contains(logs, "other-repo") || !strings.Contains(logs, "work_dir") {
		t.Errorf("the log does not name the repository and the key:\n%s", logs)
	}

	// The file is read again on every poll, so a merged fix takes effect by
	// itself.
	sc.fake.SetFile(other, ".cumin/config.toml", githubtest.File{Content: "max_review_rounds = 2\n"})
	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("the poll after the fix: %v", err)
	}
	service.Wait()
	if got := sc.fake.Issue(other, 2).Labels; slices.Contains(got, "cumin/status/ready") {
		t.Errorf("labels of the sub-issue = %v, want the claim after the fix", got)
	}
}

// The files are parsed again only when a blob changed.
func TestPoll_ReadsTheRepositoryFilesAgainOnlyAfterAChange(t *testing.T) {
	sc := newScene(t)
	sc.fake.SetFile(sc.repo, ".cumin/config.toml", githubtest.File{Content: "max_review_rounds = 2\n"})
	service := sc.service()
	ctx := context.Background()

	for i := range 3 {
		if err := service.Poll(ctx); err != nil {
			t.Fatalf("poll %d: %v", i+1, err)
		}
	}
	service.Wait()
	const read = "the settings of the repository were read"
	if n := strings.Count(sc.logs.String(), read); n != 1 {
		t.Errorf("the files were read %d times over three polls, want 1", n)
	}

	sc.fake.SetFile(sc.repo, ".cumin/config.toml", githubtest.File{Content: "max_review_rounds = 3\n"})
	if err := service.Poll(ctx); err != nil {
		t.Fatalf("the poll after the change: %v", err)
	}
	service.Wait()
	if n := strings.Count(sc.logs.String(), read); n != 2 {
		t.Errorf("the files were read %d times, want 2 after the change", n)
	}
}

// The risk criteria comes from the strongest file that exists. The poll
// logs where it came from, never the text.
func TestPoll_LogsWhereTheRiskCriteriaCameFrom(t *testing.T) {
	const repositoryCriteria = "# Risk criteria of the repository\n"
	const hostCriteria = "# Risk criteria of the Host\n"
	tests := []struct {
		name       string
		repository string
		host       string
		want       string
	}{
		{"the default", "", "", "default"},
		{"the file of the Host", "", hostCriteria, "host"},
		{"the file of the repository", repositoryCriteria, hostCriteria, "repository"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := newScene(t)
			if tt.repository != "" {
				sc.fake.SetFile(sc.repo, ".cumin/risk-criteria.md", githubtest.File{Content: tt.repository})
			}
			if tt.host != "" {
				if err := os.WriteFile(filepath.Join(sc.settingsDir, "risk-criteria.md"), []byte(tt.host), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			service := sc.service()

			if err := service.Poll(context.Background()); err != nil {
				t.Fatalf("poll: %v", err)
			}
			service.Wait()

			logs := sc.logs.String()
			if !strings.Contains(logs, `"risk_criteria":"`+tt.want+`"`) {
				t.Errorf("the poll log does not say %q:\n%s", tt.want, logs)
			}
			// The text of the criteria never reaches a log.
			for _, text := range []string{"Risk criteria of the repository", "Risk criteria of the Host", "risk/medium"} {
				if strings.Contains(logs, text) {
					t.Errorf("the log holds the text of the risk criteria (%q):\n%s", text, logs)
				}
			}
		})
	}
}

// The stop of cumin: SIGINT or SIGTERM ends the context, the request that
// is going on is cancelled, Run waits only for the grace, logs the issues
// that were in progress, and returns nil so that the command exits with 0.
// The labels stay as they are; the Maintainer restarts an issue with
// cumin/status/ready.
func TestStopCancelsTheRunningRequestAndLogsTheIssue(t *testing.T) {
	// A CLI that ignores SIGTERM: the adapter sends SIGKILL after its
	// grace, which is 200 ms in the scene.
	sc := newScene(t, cliOptions{sleeps: true, ignoresTerm: true})
	service := sc.service()
	service.PollInterval = 10 * time.Millisecond
	// The grace is far above the hang guard: a Run that returns in the test
	// returned because the request ended, which the stop log says too.
	service.StopGrace = 10 * hangGuard

	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan error, 1)
	go func() { returned <- service.Run(ctx) }()
	waitForAgentRun(t, sc)

	cancel()
	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("Run returned %v, want nil so that the command exits with 0", err)
		}
	case <-time.After(hangGuard):
		t.Fatalf("Run did not return after the stop:\n%s", sc.logs.String())
	}

	logs := sc.logs.String()
	for _, want := range []string{
		`"msg":"stopped"`,
		`"in_progress":["example-org/example-repo#10"]`,
		`"ended_within_grace":true`,
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("the stop log does not hold %s:\n%s", want, logs)
		}
	}
	// The issue keeps the label of the claim. cumin changes no label on
	// the way out.
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", "cumin/status/implementing"}) {
		t.Errorf("labels of #10 = %v, want the labels of the claim", got)
	}
}

// A request that ends on SIGTERM does not make the stop wait for the whole
// grace.
func TestRun_StopReturnsAsSoonAsTheRequestEnds(t *testing.T) {
	sc := newScene(t, cliOptions{sleeps: true})
	service := sc.service()
	service.PollInterval = 10 * time.Millisecond
	// The grace is far above the hang guard: a Run that returns in the test
	// did not wait for the whole grace.
	service.StopGrace = 10 * hangGuard

	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan error, 1)
	go func() { returned <- service.Run(ctx) }()
	waitForAgentRun(t, sc)

	cancel()
	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(hangGuard):
		t.Fatalf("Run did not return after the stop:\n%s", sc.logs.String())
	}
	logs := sc.logs.String()
	if !strings.Contains(logs, `"ended_within_grace":true`) {
		t.Errorf("the stop log does not say that the request ended:\n%s", logs)
	}
	// The issue was in progress when the signal came, so the line names it
	// even though its run ended at once. The issue keeps
	// cumin/status/implementing, and the Maintainer has to restart it.
	if !strings.Contains(logs, `"in_progress":["example-org/example-repo#10"]`) {
		t.Errorf("the stop log does not name the issue that was in progress:\n%s", logs)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, "cumin/status/implementing") {
		t.Errorf("labels of #10 = %v, want cumin/status/implementing", got)
	}
}

// After the stop signal, a poll that is still running starts no new work.
func TestPoll_StartsNothingAfterTheStopSignal(t *testing.T) {
	sc := newScene(t)
	service := sc.service()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := service.Poll(ctx); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	service.Wait()

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none after the signal", n)
	}
	if n := sc.fake.CountRequests(http.MethodPut, putLabelsPath); n != 0 {
		t.Errorf("%d label changes, want none after the signal", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, "cumin/status/ready") {
		t.Errorf("labels of #10 = %v, want the ready label untouched", got)
	}
}

// The stop must not end before the adapter has killed the process group of
// the CLI, or a run would stay alive with the token of its role. The wait
// of the stop is therefore longer than the grace of the adapter.
func TestStopGrace_IsLongerThanTheGraceOfTheAdapter(t *testing.T) {
	const grace = 2 * time.Second
	agents := &agent.Service{Grace: grace}
	if budget := agents.StopBudget(); budget <= grace {
		t.Errorf("the budget of one run is %s, want more than the grace (%s)", budget, grace)
	}
	// The service takes the value of the agent service when it has one.
	sc := newScene(t)
	service := sc.service()
	service.Agents.Grace = grace
	if got := workflow.StopGraceOf(service); got != agents.StopBudget() {
		t.Errorf("the stop waits %s, want the budget of one run (%s)", got, agents.StopBudget())
	}
	// Without an agent service, the default is longer than the default
	// grace of the adapter.
	empty := &workflow.Service{}
	if got := workflow.StopGraceOf(empty); got != workflow.DefaultStopGrace {
		t.Errorf("the stop waits %s, want the default (%s)", got, workflow.DefaultStopGrace)
	}
	if workflow.DefaultStopGrace <= (&agent.Service{}).StopBudget()-time.Second {
		t.Errorf("the default stop grace (%s) is not longer than the grace of the adapter", workflow.DefaultStopGrace)
	}
}
