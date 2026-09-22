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
	// reads, in place of GitHub. remoteHead is the commit at its main.
	remote     string
	remoteHead string
	// cliDir is where the fake CLI records each run.
	cliDir string
	// workRoot is the setting work_dir.
	workRoot string
	cliPath  string
	// settingsDir is the directory of the Host settings file. A test puts a
	// risk-criteria.md of the Host in it.
	settingsDir string
}

func newScene(t *testing.T, opts ...cliOptions) *scene {
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

	var options cliOptions
	if len(opts) > 0 {
		options = opts[0]
	}
	cliPath, cliDir := fakeCLI(t, options)
	remote, head := newRemote(t)
	return &scene{
		fake: fake, client: github.NewAppClient(server.URL, server.Client()), repo: repo,
		logs: &bytes.Buffer{}, remote: remote, remoteHead: head, cliDir: cliDir,
		workRoot: t.TempDir(), cliPath: cliPath, settingsDir: t.TempDir(),
	}
}

// cliOptions change what the fake CLI does on the agent run.
type cliOptions struct {
	// fixture is the file under ../agent/testdata that the agent run
	// prints. Empty means done.jsonl.
	fixture string
	// commit makes the agent run add one commit in the work directory and
	// not push it, as an Implementer that forgot to push.
	commit bool
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
		Settings:    sc.settings(),
		SettingsDir: sc.settingsDir,
		Logger:      logger,
	}
}

// settings are the Host settings of the scene: the defaults, with the fake
// CLI as the executable of every role.
func (sc *scene) settings() *config.Settings {
	roleSettings := config.RoleSettings{TimeLimit: time.Minute, CLI: config.CLIClaudeCode, CLIPath: sc.cliPath}
	return &config.Settings{
		Repositories:        []config.Repository{{Owner: "example-org", Name: "example-repo"}},
		PollInterval:        time.Minute,
		MaxIssuesInProgress: 1,
		WorkDir:             sc.workRoot,
		MaxReviewRounds:     3,
		MaxCheckFixRequests: 3,
		MergeMethod:         config.MergeSquash,
		Roles: map[config.Role]config.RoleSettings{
			config.RoleChiefEngineer: roleSettings,
			config.RoleImplementer:   roleSettings,
			config.RoleReviewer:      roleSettings,
		},
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
func fakeCLI(t *testing.T, o cliOptions) (path, dir string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "fake-claude")
	if o.fixture == "" {
		o.fixture = "done.jsonl"
	}
	quota := fixturePath(t, "quota-run.jsonl")
	agentFixture := fixturePath(t, o.fixture)
	// The git output goes to standard error: standard output carries the
	// events that the adapter reads.
	commit := ""
	if o.commit {
		commit = "if [ $n = agent ]; then\n" +
			"echo change > local.txt\n" +
			"git add local.txt 1>&2\n" +
			"git commit --quiet -m \"a local commit that is not pushed\" 1>&2\n" +
			"fi\n"
	}
	script := "#!/bin/sh\n" +
		"n=agent; f=" + agentFixture + "\n" +
		"for a in \"$@\"; do [ \"$a\" = --system-prompt ] && { n=quota; f=" + quota + "; }; done\n" +
		"echo $n >> " + filepath.Join(dir, "order") + "\n" +
		"for a in \"$@\"; do printf '%s\\0' \"$a\"; done > " + filepath.Join(dir, "$n.args") + "\n" +
		"env > " + filepath.Join(dir, "$n.env") + "\n" +
		"pwd > " + filepath.Join(dir, "$n.cwd") + "\n" +
		commit +
		"cat $f\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, dir
}

func fixturePath(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "agent", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// newRemote creates a bare repository with one commit on main, in place of
// the repository on GitHub. It returns the path of the bare repository and
// the commit at its main, which a test registers as the head of a pull
// request.
func newRemote(t *testing.T) (bare, head string) {
	t.Helper()
	base := t.TempDir()
	bare = filepath.Join(base, "remote.git")
	work := filepath.Join(base, "work")
	git(t, base, "init", "--quiet", "--bare", "--initial-branch=main", bare)
	git(t, base, "clone", "--quiet", bare, work)
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", "README.md")
	git(t, work, "commit", "--quiet", "-m", "add README.md")
	git(t, work, "push", "--quiet", "origin", "main")
	return bare, git(t, work, "rev-parse", "HEAD")
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.name=cumin-test", "-c", "user.email=cumin-test@example.com"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
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
	// Three polls, and one read again at the end of the run (I2). The
	// verification fails here, because the scene has no pull request; the
	// label of #10 therefore stays cumin/status/implementing.
	if n := sc.fake.CountRequests(http.MethodPost, "/graphql"); n != 4 {
		t.Errorf("%d snapshot reads, want 4", n)
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

// addPullRequest registers one open pull request that closes #10.
func (sc *scene) addPullRequest(number int, head, author string, isBot bool) {
	sc.fake.AddPullRequest(sc.repo, &githubtest.PullRequest{
		Number: number, HeadCommit: head, Author: author, AuthorIsBot: isBot, Closes: []int{10},
	})
}

// pollAndWait does one poll and waits for the agent run that it started.
func (sc *scene) pollAndWait(t *testing.T, service *workflow.Service) {
	t.Helper()
	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	service.Wait()
}

// I2 (issue-states.md): after done, an open pull request closes the issue,
// its author is the Implementer App, and the head commit of the worktree is
// pushed. Then the label becomes cumin/status/awaiting-checks.
func TestI2_DoneWithTheVerifiedPullRequestMovesTheIssueToAwaitingChecks(t *testing.T) {
	sc := newScene(t)
	// The agent makes no commit, so the head of the worktree is the head of
	// main of the remote. The pull request is at the same commit.
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	service := sc.service()

	sc.pollAndWait(t, service)

	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", "cumin/status/awaiting-checks"}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/awaiting-checks", got)
	}
	if n := sc.fake.CountRequests(http.MethodPut, putLabelsPath); n != 2 {
		t.Errorf("%d label changes, want 2 (the claim and I2)", n)
	}
	// The end of the run reads the snapshot again, so that a pull request
	// that the agent opened just before it ended is seen.
	if n := sc.fake.CountRequests(http.MethodPost, "/graphql"); n != 2 {
		t.Errorf("%d snapshot reads, want 2 (the poll and the read after the run)", n)
	}
	logs := sc.logs.String()
	for _, want := range []string{`"msg":"I2: verified the pull request"`, `"pull_request":21`, `"issue":10`} {
		if !strings.Contains(logs, want) {
			t.Errorf("the log has no %s:\n%s", want, logs)
		}
	}
}

// Of two open pull requests that close the issue, the one with the highest
// number is checked (cumin-core.md, the topic on the end of a run).
func TestI2_DoneChecksThePullRequestWithTheHighestNumber(t *testing.T) {
	sc := newScene(t)
	sc.addPullRequest(21, "0000000000000000000000000000000000000000", implementerSlug, true)
	sc.addPullRequest(22, sc.remoteHead, implementerSlug, true)
	service := sc.service()

	sc.pollAndWait(t, service)

	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, "cumin/status/awaiting-checks") {
		t.Errorf("labels of #10 = %v, want cumin/status/awaiting-checks", got)
	}
	if !strings.Contains(sc.logs.String(), `"pull_request":22`) {
		t.Errorf("the log does not name pull request 22:\n%s", sc.logs.String())
	}
}

func TestI2_DoneWithoutAPullRequestLeavesTheLabel(t *testing.T) {
	sc := newScene(t)
	service := sc.service()

	sc.pollAndWait(t, service)

	assertVerificationFailed(t, sc, "no open pull request closes the issue")
}

func TestI2_DoneWithAPullRequestOfAnotherAuthorLeavesTheLabel(t *testing.T) {
	sc := newScene(t)
	sc.addPullRequest(21, sc.remoteHead, "another-person", false)
	service := sc.service()

	sc.pollAndWait(t, service)

	assertVerificationFailed(t, sc, "the author of the pull request is not the Implementer App")
	if !strings.Contains(sc.logs.String(), `"pull_request":21`) {
		t.Errorf("the log does not name the pull request that was checked:\n%s", sc.logs.String())
	}
}

// The agent commits in the worktree and does not push. The head of the pull
// request is then behind the head of the worktree.
func TestI2_DoneWithACommitThatIsNotPushedLeavesTheLabel(t *testing.T) {
	sc := newScene(t, cliOptions{commit: true})
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	service := sc.service()

	sc.pollAndWait(t, service)

	assertVerificationFailed(t, sc, "the head commit of the worktree is not pushed")
}

// A blocked result is logged with the question of blocked_reason. The label
// stays; #81 posts the comment and asks the Owner.
func TestI2_BlockedLeavesTheLabelAndLogsTheQuestion(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "blocked.jsonl"})
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	service := sc.service()

	sc.pollAndWait(t, service)

	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, "cumin/status/implementing") {
		t.Errorf("labels of #10 = %v, want cumin/status/implementing", got)
	}
	// No verification runs, so the snapshot is not read again.
	if n := sc.fake.CountRequests(http.MethodPost, "/graphql"); n != 1 {
		t.Errorf("%d snapshot reads, want 1", n)
	}
	logs := sc.logs.String()
	for _, want := range []string{`"msg":"the agent returned blocked"`, "## Decision needed:", `"result":"blocked"`} {
		if !strings.Contains(logs, want) {
			t.Errorf("the log has no %s:\n%s", want, logs)
		}
	}
	// Only the first line of blocked_reason reaches the log field.
	if strings.Contains(logs, `"msg":"I2`) {
		t.Errorf("I2 ran on a blocked result:\n%s", logs)
	}
}

// assertVerificationFailed checks that the label of #10 stayed at
// cumin/status/implementing and that the log names the failure.
func assertVerificationFailed(t *testing.T, sc *scene, failure string) {
	t.Helper()
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", "cumin/status/implementing"}) {
		t.Errorf("labels of #10 = %v, want cumin/status/implementing still", got)
	}
	if n := sc.fake.CountRequests(http.MethodPut, putLabelsPath); n != 1 {
		t.Errorf("%d label changes, want 1 (the claim only)", n)
	}
	logs := sc.logs.String()
	if !strings.Contains(logs, `"msg":"I2: the verification failed"`) {
		t.Errorf("the log does not say that the verification failed:\n%s", logs)
	}
	if !strings.Contains(logs, `"failure":"`+failure+`"`) {
		t.Errorf("the log does not name the failure %q:\n%s", failure, logs)
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

	if n := sc.agentRuns(t); n != 1 {
		t.Fatalf("%d agent runs, want 1", n)
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
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1 for the repository whose file is right", n)
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
