package workflow_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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

const (
	putLabelsPath = "/repos/example-org/example-repo/issues/10/labels"
	// subIssueTitle gives the branch cumin/10-add-the-login-screen
	// (the slug rule of BranchName).
	subIssueTitle   = "Add the login screen"
	wantBranch      = "cumin/10-add-the-login-screen"
	implementerSlug = "example-implementer"
)

// testKey is the private key of the Implementer App of the fake. The fake
// does not verify the signature, but the client signs the JWT with it.
var testKey = sync.OnceValue(func() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return key
})

// scene is one fake repository with the requirement issue #6, one ready
// sub-issue #10 with risk/low, the Implementer App, a local remote, and a
// fake CLI.
type scene struct {
	fake   *githubtest.Fake
	client *github.AppClient
	repo   *githubtest.Repository
	logs   *bytes.Buffer
	// remote is the bare repository that the clone of the work directory
	// reads, in place of GitHub.
	remote string
	// cliDir is where the fake CLI records each run.
	cliDir string
	// workRoot is the setting work_dir.
	workRoot string
	cliPath  string
}

func newScene(t *testing.T) *scene {
	t.Helper()
	// Keep the git configuration of the Host out of every git call, in the
	// test and in Workspace.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddApp(githubtest.App{Slug: implementerSlug, Owner: "example-org", BotID: 424242})
	fake.AddIssue(repo, &githubtest.Issue{Number: 6, Labels: []string{githubtest.RequirementLabel, "cumin/status/implementing"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Labels: []string{"cumin/status/ready", "risk/low"}})

	cliPath, cliDir := fakeCLI(t)
	return &scene{
		fake: fake, client: github.NewAppClient(server.URL, server.Client()), repo: repo,
		logs: &bytes.Buffer{}, remote: newRemote(t), cliDir: cliDir, workRoot: t.TempDir(), cliPath: cliPath,
	}
}

// service returns a new Service on the scene, as after a restart of cumin.
func (sc *scene) service() *workflow.Service {
	logger := slog.New(slog.NewJSONHandler(sc.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	agents := &agent.Service{
		Roles: map[config.Role]config.RoleSettings{
			config.RoleImplementer: {TimeLimit: time.Minute, CLI: config.CLIClaudeCode, CLIPath: sc.cliPath},
		},
		Apps: map[string]map[config.Role]github.AppCredentials{
			"example-org": {config.RoleImplementer: {ClientID: "Iv23liEXAMPLE", PrivateKey: testKey()}},
		},
		GitHub: sc.client,
		Logger: logger,
	}
	return &workflow.Service{
		GitHub:    sc.client,
		Agents:    agents,
		Workspace: agent.Workspace{Root: sc.workRoot, Logger: logger},
		Targets: []workflow.Target{{
			Repository: config.Repository{Owner: "example-org", Name: "example-repo"},
			RemoteURL:  sc.remote,
			Token:      func(context.Context) (string, error) { return githubtest.Token, nil },
		}},
		MaxIssuesInProgress: 1,
		Logger:              logger,
	}
}

// agentRuns returns the number of agent runs of the fake CLI.
func (sc *scene) agentRuns(t *testing.T) int {
	t.Helper()
	return len(readLines(t, filepath.Join(sc.cliDir, "order"), "agent"))
}

// readLines returns the lines of the file that equal want; none when the
// file does not exist.
func readLines(t *testing.T, path, want string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == want {
			lines = append(lines, line)
		}
	}
	return lines
}

// record reads one file that the fake CLI wrote for the agent run.
func (sc *scene) record(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(sc.cliDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// environment reads the environment of the agent run as a map.
func (sc *scene) environment(t *testing.T) map[string]string {
	t.Helper()
	env := map[string]string{}
	for _, line := range strings.Split(sc.record(t, "agent.env"), "\n") {
		if name, value, ok := strings.Cut(line, "="); ok {
			env[name] = value
		}
	}
	return env
}

// fakeCLI writes a fake agent CLI. The quota run is told by
// --system-prompt; each run answers from the fixture of internal/agent and
// records its arguments, its environment, and its working directory.
func fakeCLI(t *testing.T) (path, dir string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "fake-claude")
	quota, err := filepath.Abs(filepath.Join("..", "agent", "testdata", "quota-run.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	done, err := filepath.Abs(filepath.Join("..", "agent", "testdata", "done.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"n=agent; f=" + done + "\n" +
		"for a in \"$@\"; do [ \"$a\" = --system-prompt ] && { n=quota; f=" + quota + "; }; done\n" +
		"echo $n >> " + filepath.Join(dir, "order") + "\n" +
		"for a in \"$@\"; do printf '%s\\0' \"$a\"; done > " + filepath.Join(dir, "$n.args") + "\n" +
		"env > " + filepath.Join(dir, "$n.env") + "\n" +
		"pwd > " + filepath.Join(dir, "$n.cwd") + "\n" +
		"cat $f\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, dir
}

// newRemote creates a bare repository with one commit on main, in place of
// the repository on GitHub, and returns its path.
func newRemote(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	bare := filepath.Join(base, "remote.git")
	work := filepath.Join(base, "work")
	git(t, base, "init", "--quiet", "--bare", "--initial-branch=main", bare)
	git(t, base, "clone", "--quiet", bare, work)
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", "README.md")
	git(t, work, "commit", "--quiet", "-m", "add README.md")
	git(t, work, "push", "--quiet", "origin", "main")
	return bare
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-c", "user.name=cumin-test", "-c", "user.email=cumin-test@example.com"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// Core-1 (cumin-core.md): one ready implementation issue, two or more polls,
// one request to the Implementer.
func TestCore01_ReadyIssueIsRequestedOnce(t *testing.T) {
	sc := newScene(t)
	service := sc.service()
	ctx := context.Background()

	for i := range 3 {
		if err := service.Poll(ctx); err != nil {
			t.Fatalf("poll %d: %v", i+1, err)
		}
	}
	service.Wait()

	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1", n)
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

	// The CLI ran in the worktree of the issue and the role, on the branch
	// that the title gives.
	wantDir := filepath.Join(sc.workRoot, "example-org", "example-repo", "10-implementer")
	if got := strings.TrimSpace(sc.record(t, "agent.cwd")); got != realPath(t, wantDir) {
		t.Errorf("the CLI ran in %q, want %q", got, realPath(t, wantDir))
	}
	if got := branchOf(t, wantDir); got != wantBranch {
		t.Errorf("branch of the worktree = %q, want %q", got, wantBranch)
	}

	// The request text names the kind, the repository, the issue, and the
	// branch. It is the last argument of -p.
	text := promptOf(t, sc.record(t, "agent.args"))
	for _, want := range []string{"Request: implement", "example-org/example-repo", "#10", wantBranch} {
		if !strings.Contains(text, want) {
			t.Errorf("the request text has no %q:\n%s", want, text)
		}
	}

	logs := sc.logs.String()
	if strings.Contains(logs, githubtest.Token) {
		t.Error("the log holds the token")
	}
	for _, want := range []string{`"msg":"poll"`, `"rate_limit_cost"`, `"msg":"I1: claimed the issue"`,
		`"msg":"I1: requested the work"`, `"branch":"` + wantBranch + `"`,
		`"msg":"the agent run ended"`, `"result":"done"`} {
		if !strings.Contains(logs, want) {
			t.Errorf("the log has no %s", want)
		}
	}
}

// Core-8 (cumin-core.md): stop cumin and start it again; the same issue is
// not requested twice.
func TestCore08_RestartDoesNotRequestTwice(t *testing.T) {
	sc := newScene(t)
	ctx := context.Background()

	first := sc.service()
	if err := first.Poll(ctx); err != nil {
		t.Fatalf("first run: %v", err)
	}
	first.Wait()
	// A new Service holds nothing from the first one. The facts are on
	// GitHub: the label of #10 is now cumin/status/implementing.
	second := sc.service()
	if err := second.Poll(ctx); err != nil {
		t.Fatalf("second run: %v", err)
	}
	second.Wait()

	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1", n)
	}
}

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
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1", n)
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
	if !strings.Contains(logs, `"msg":"I1: the work directory was not prepared"`) || !strings.Contains(logs, `"issue":10`) {
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
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1 for the good repository", n)
	}
}

func TestRun_CreatesTheLabelsOnceAndPollsAtTheInterval(t *testing.T) {
	sc := newScene(t)
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
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the cancel")
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
		`"msg":"I1: the work directory was not prepared"`,
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
	if n := sc.fake.CountRequests(http.MethodPost, "/repos/example-org/example-repo/labels"); n != 11 {
		t.Errorf("%d labels created after the second start, want 11 still", n)
	}
}

func TestRun_RejectsAnIntervalOfZero(t *testing.T) {
	service := newScene(t).service()
	if err := service.Run(context.Background()); err == nil {
		t.Error("Run with no interval returned nil")
	}
}

// branchOf returns the branch that the worktree is on.
func branchOf(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// promptOf returns the value of -p from the recorded arguments, which are
// separated by NUL.
func promptOf(t *testing.T, args string) string {
	t.Helper()
	list := strings.Split(strings.TrimSuffix(args, "\x00"), "\x00")
	for i, arg := range list {
		if arg == "-p" && i+1 < len(list) {
			return list[i+1]
		}
	}
	t.Fatalf("the arguments have no -p: %q", list)
	return ""
}

func realPath(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return real
}
