package workflow_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
	"github.com/cloveclovedev/cumin-works/internal/core/state"
	"github.com/cloveclovedev/cumin-works/internal/notify"
	"github.com/cloveclovedev/cumin-works/internal/platform/discord"
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
	// webhook is the fake Discord that the notifier of the scene sends to.
	webhook *fakeWebhook
	// notifier is what the service of the scene uses. A test may replace
	// it, for example with a notifier without a channel.
	notifier *notify.Notifier
	// notifications is the Host setting notify.discord.enabled.
	notifications bool
	// clock is the time of the quota decisions (Q1).
	clock *testClock
	// quota are the Host settings of the quota limits: the defaults of the
	// settings table.
	quota config.QuotaSettings
}

// sceneNow is the default time of the quota decisions: one hour before the
// weekly reset of the fixtures of internal/agent, so that the pace limit is
// the target and their usage stops nothing. Their 5h window reset earlier,
// so it stops nothing either.
var sceneNow = time.Unix(1900300000, 0).Add(-time.Hour)

// testClock is a clock that a test moves between polls.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// setQuota makes every later minimal run of the fake CLI report this usage.
func (sc *scene) setQuota(t *testing.T, fiveHour float64, fiveHourReset time.Time, weekly float64, weeklyReset time.Time) {
	t.Helper()
	event := fmt.Sprintf(`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","unifiedWindows":{"five_hour":{"utilization":%v,"resetsAt":%d},"seven_day":{"utilization":%v,"resetsAt":%d}}},"session_id":"22222222-2222-4333-8444-555555555555"}`,
		fiveHour, fiveHourReset.Unix(), weekly, weeklyReset.Unix())
	content := `{"type":"system","subtype":"init","skills":[],"session_id":"22222222-2222-4333-8444-555555555555","cwd":"/example/quota","model":"example-small-model","tools":[],"plugins":[],"mcp_servers":[]}` + "\n" +
		event + "\n" +
		`{"type":"result","subtype":"success","is_error":false,"session_id":"22222222-2222-4333-8444-555555555555","num_turns":1,"result":"OK"}` + "\n"
	if err := os.WriteFile(filepath.Join(sc.cliDir, "quota-override.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// failQuota makes every later minimal run print no rate_limit_event.
func (sc *scene) failQuota(t *testing.T) {
	t.Helper()
	data, err := os.ReadFile(fixturePath(t, "no-quota.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sc.cliDir, "quota-override.jsonl"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// quotaRuns returns the number of minimal runs of the fake CLI.
func (sc *scene) quotaRuns(t *testing.T) int {
	t.Helper()
	return len(readLines(t, filepath.Join(sc.cliDir, "order"), "quota"))
}

// fakeWebhook is the Discord of the tests: it records the messages and can
// answer with a failure. The real client of internal/platform/discord
// sends to it, so the tests cover the request as well.
type fakeWebhook struct {
	server *httptest.Server
	mu     sync.Mutex
	// status is the answer of the webhook. 0 means 204.
	status   int
	messages []string
}

func newFakeWebhook(t *testing.T) *fakeWebhook {
	t.Helper()
	f := &fakeWebhook{}
	// TLS, because the client refuses an address that is not https.
	f.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Content string `json:"content"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.messages = append(f.messages, body.Content)
		status := f.status
		f.mu.Unlock()
		if status == 0 {
			status = http.StatusNoContent
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(f.server.Close)
	return f
}

// messagesSent returns the messages that the webhook received.
func (f *fakeWebhook) messagesSent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.messages)
}

// messagesExceptQ4 returns the messages other than Q4 (waiting). A test of
// another row that ends with nothing to do also gets one Q4 notification.
func (sc *scene) messagesExceptQ4() []string {
	var messages []string
	for _, m := range sc.webhook.messagesSent() {
		if !strings.HasPrefix(m, "cumin: Q4: ") {
			messages = append(messages, m)
		}
	}
	return messages
}

// fails makes the webhook answer with status from now on.
func (f *fakeWebhook) fails(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = status
}

func (f *fakeWebhook) notifier() *notify.Notifier {
	return notify.New(discord.Webhook{
		URL:        f.server.URL + "/api/webhooks/1/fake-token",
		HTTPClient: f.server.Client(),
	})
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
	options.serverURL = server.URL
	cliPath, cliDir := fakeCLI(t, options)
	remote, head := newRemote(t)
	webhook := newFakeWebhook(t)
	return &scene{
		fake: fake, client: github.NewAppClient(server.URL, server.Client()), repo: repo,
		logs: &bytes.Buffer{}, remote: remote, remoteHead: head, cliDir: cliDir,
		workRoot: t.TempDir(), cliPath: cliPath, settingsDir: t.TempDir(),
		webhook: webhook, notifier: webhook.notifier(), notifications: true,
		clock: &testClock{now: sceneNow},
		quota: config.QuotaSettings{
			FiveHour: config.FiveHourQuota{Threshold: 85},
			Weekly:   config.WeeklyQuota{Target: 85, Lead: 24 * time.Hour},
		},
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
	// sleeps makes the agent run wait instead of ending, so that a test can
	// stop cumin while a request is going on.
	sleeps bool
	// ignoresTerm makes the sleeping agent run ignore SIGTERM, as a CLI
	// that does not end by itself. The adapter then sends SIGKILL after its
	// grace.
	ignoresTerm bool
	// secondFixture is what the second agent run prints, when cumin runs
	// the same request again after an abnormal end. Empty means that every
	// run prints fixture.
	secondFixture string
	// reviews are what each agent run submits on the pull request #21, one
	// entry for each run in order: an event of the review API (APPROVE,
	// REQUEST_CHANGES, COMMENT), or NONE for no review. The review is on
	// the commit that the work directory holds, as a Reviewer submits it.
	// A run past the end of the list submits nothing.
	reviews []string
	// movesHead makes the first agent run push a new commit and move the
	// head of the pull request #21 to it, as a push during the review.
	movesHead bool
	// comments are what each agent run writes on the pull request #21, one
	// entry for each run in order: DECISION writes a decision request, NONE
	// writes nothing, as the Reviewer does for I8.
	comments []string
	// serverURL is the address of the fake GitHub; newScene sets it.
	serverURL string
}

// service returns a new Service on the scene, as after a restart of cumin.
func (sc *scene) service() *workflow.Service {
	logger := slog.New(slog.NewJSONHandler(sc.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	agents := &agent.Service{
		Roles: map[config.Role]config.RoleSettings{
			config.RolePlanner:     {TimeLimit: time.Minute, CLI: config.CLIClaudeCode, CLIPath: sc.cliPath},
			config.RoleImplementer: {TimeLimit: time.Minute, CLI: config.CLIClaudeCode, CLIPath: sc.cliPath},
			config.RoleReviewer:    {TimeLimit: time.Minute, CLI: config.CLIClaudeCode, CLIPath: sc.cliPath},
		},
		// The fake knows one App, so the Planner and the Reviewer run with
		// the same credentials as the Implementer. R1 does not read the
		// identity; the Reviewer is "<slug>[bot]" of that App.
		Apps: map[string]map[config.Role]github.AppCredentials{
			"example-org": {
				config.RolePlanner:     {ClientID: "Iv23liEXAMPLE", PrivateKey: testKey()},
				config.RoleImplementer: {ClientID: "Iv23liEXAMPLE", PrivateKey: testKey()},
				config.RoleReviewer:    {ClientID: "Iv23liEXAMPLE", PrivateKey: testKey()},
			},
		},
		GitHub: sc.client,
		Logger: logger,
		// The stop sends SIGTERM to the process group of the CLI and
		// SIGKILL after this grace. The tests must not wait ten seconds.
		Grace: 200 * time.Millisecond,
	}
	return &workflow.Service{
		GitHub:    sc.client,
		Agents:    agents,
		Notify:    sc.notifier,
		Workspace: agent.Workspace{Root: sc.workRoot, Logger: logger},
		Targets: []workflow.Target{{
			Repository: config.Repository{Owner: "example-org", Name: "example-repo"},
			RemoteURL:  sc.remote,
			Token:      func(context.Context) (string, error) { return githubtest.Token, nil },
			Login:      func(context.Context) (string, error) { return cuminLogin, nil },
		}},
		Settings:    sc.settings(),
		SettingsDir: sc.settingsDir,
		Logger:      logger,
		Now:         sc.clock.Now,
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
			config.RolePlanner:     roleSettings,
			config.RoleImplementer: roleSettings,
			config.RoleReviewer:    roleSettings,
		},
		Notify: config.NotifySettings{DiscordEnabled: sc.notifications},
		Quota:  sc.quota,
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
	// A run that sleeps ends with SIGKILL, so it prints its events first.
	sleep := ""
	if o.sleeps {
		trap := ""
		if o.ignoresTerm {
			trap = "trap '' TERM\n"
		}
		sleep = "if [ $n = agent ]; then\n" + trap + "sleep 600\nfi\n"
	}
	// The second agent run prints another fixture, so that a test can let
	// the retry end differently from the first run.
	second := ""
	if o.secondFixture != "" {
		second = "if [ $n = agent ] && [ \"$(grep -c '^agent$' " + filepath.Join(dir, "order") + ")\" -ge 2 ]; then f=" +
			fixturePath(t, o.secondFixture) + "; fi\n"
	}
	// The review goes to the fake GitHub, as the Reviewer submits it with
	// gh api. Its output goes to standard error.
	review := ""
	if len(o.reviews) > 0 {
		list := filepath.Join(dir, "reviews")
		if err := os.WriteFile(list, []byte(strings.Join(o.reviews, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		review = "if [ $n = agent ]; then\n" +
			"k=$(grep -c '^agent$' " + filepath.Join(dir, "order") + "); e=$(sed -n \"${k}p\" " + list + ")\n" +
			"if [ -n \"$e\" ] && [ \"$e\" != NONE ]; then\n" +
			"curl -s -X POST -H 'Authorization: Bearer " + githubtest.Token + "' " +
			"-d \"{\\\"commit_id\\\":\\\"$(git rev-parse HEAD)\\\",\\\"event\\\":\\\"$e\\\",\\\"body\\\":\\\"review\\\"}\" " +
			o.serverURL + "/repos/example-org/example-repo/pulls/21/reviews 1>&2\n" +
			"fi\nfi\n"
	}
	if o.movesHead {
		review += "if [ $n = agent ] && [ \"$(grep -c '^agent$' " + filepath.Join(dir, "order") + ")\" = 1 ]; then\n" +
			"echo moved > moved.txt; git add moved.txt 1>&2\n" +
			"git -c user.name=t -c user.email=t@example.com commit --quiet -m moved 1>&2\n" +
			"git push --quiet origin HEAD:refs/heads/moved 1>&2\n" +
			"curl -s -X POST -H 'Authorization: Bearer " + githubtest.Token + "' -d \"{\\\"sha\\\":\\\"$(git rev-parse HEAD)\\\"}\" " +
			o.serverURL + "/_fake/repos/example-org/example-repo/pulls/21/head 1>&2\n" +
			"fi\n"
	}
	if len(o.comments) > 0 {
		list := filepath.Join(dir, "comments")
		if err := os.WriteFile(list, []byte(strings.Join(o.comments, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		review += "if [ $n = agent ]; then\n" +
			"k=$(grep -c '^agent$' " + filepath.Join(dir, "order") + "); e=$(sed -n \"${k}p\" " + list + ")\n" +
			"if [ \"$e\" = DECISION ]; then\n" +
			"curl -s -X POST -H 'Authorization: Bearer " + githubtest.Token + "' " +
			"-d '{\"body\":\"## Decision needed: Which error does the handler return?\\n\\nType: Unresolved after 3 review rounds\"}' " +
			o.serverURL + "/repos/example-org/example-repo/issues/21/comments 1>&2\n" +
			"fi\nfi\n"
	}
	script := "#!/bin/sh\n" +
		"n=agent; f=" + agentFixture + "\n" +
		"for a in \"$@\"; do [ \"$a\" = --system-prompt ] && { n=quota; f=" + quota + "; }; done\n" +
		"[ $n = quota ] && [ -f " + filepath.Join(dir, "quota-override.jsonl") + " ] && f=" + filepath.Join(dir, "quota-override.jsonl") + "\n" +
		"echo $n >> " + filepath.Join(dir, "order") + "\n" +
		second +
		"for a in \"$@\"; do printf '%s\\0' \"$a\"; done > " + filepath.Join(dir, "$n.args") + "\n" +
		"env > " + filepath.Join(dir, "$n.env") + "\n" +
		"pwd > " + filepath.Join(dir, "$n.cwd") + "\n" +
		// The work directory as the agent saw it. The Planner's is removed
		// when its run ends, so a test reads these records instead.
		"git rev-parse HEAD > " + filepath.Join(dir, "$n.head") + " 2>/dev/null\n" +
		"git rev-parse --abbrev-ref HEAD > " + filepath.Join(dir, "$n.ref") + " 2>/dev/null\n" +
		"git branch --list 'cumin/*' > " + filepath.Join(dir, "$n.branches") + " 2>/dev/null\n" +
		commit +
		review +
		"cat $f\n" +
		sleep
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
	// The pull request that the Implementer opens, so that the run ends on
	// the success path of I2 and the issue is not stopped for the Owner.
	// GitHub made no closing link, so I2 adds it.
	sc.addUnlinkedPullRequest(21, sc.remoteHead)
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
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", "cumin/status/awaiting-checks"}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/awaiting-checks", got)
	}
	// Two label changes: the claim (I1) and the end of the run (I2).
	if n := sc.fake.CountRequests(http.MethodPut, putLabelsPath); n != 2 {
		t.Errorf("%d label changes, want 2", n)
	}
	// Three polls, one read again at the end of the run (I2), the closing
	// link, and one read after it.
	if n := sc.fake.CountRequests(http.MethodPost, "/graphql"); n != 6 {
		t.Errorf("%d GraphQL requests, want 6", n)
	}
	if n := len(sc.fake.Comments(sc.repo, 10)); n != 0 {
		t.Errorf("%d comments on #10, want none on the success path", n)
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
		Number: number, HeadCommit: head, Author: author, AuthorIsBot: isBot, Closes: []int{10}, HeadBranch: wantBranch,
	})
}

// addUnlinkedPullRequest registers one open pull request of the Implementer
// App on the branch of #10, with no closing link: GitHub did not make one
// from "Closes #10".
func (sc *scene) addUnlinkedPullRequest(number int, head string) {
	sc.fake.AddPullRequest(sc.repo, &githubtest.PullRequest{
		Number: number, HeadCommit: head, Author: implementerSlug, AuthorIsBot: true, HeadBranch: wantBranch,
	})
}

// target is one more target repository of the fake, for a test that polls
// more than one.
func target(sc *scene, name string) workflow.Target {
	return workflow.Target{
		Repository: config.Repository{Owner: "example-org", Name: name},
		RemoteURL:  sc.remote,
		Token:      func(context.Context) (string, error) { return githubtest.Token, nil },
	}
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
// number is checked (poll.md, the topic on the end of a run).
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

func TestI2_DoneWithoutAPullRequestStopsTheIssue(t *testing.T) {
	sc := newScene(t)
	service := sc.service()

	sc.pollAndWait(t, service)

	assertVerificationFailed(t, sc, "no open pull request is on the branch of the issue", workflow.FailureNoOpenPullRequest, 0)
}

func TestI2_DoneWithAPullRequestOfAnotherAuthorStopsTheIssue(t *testing.T) {
	sc := newScene(t)
	sc.addPullRequest(21, sc.remoteHead, "another-person", false)
	service := sc.service()

	sc.pollAndWait(t, service)

	assertVerificationFailed(t, sc, "the author of the pull request is not the Implementer App", workflow.FailureAuthorMismatch, 21)
	if !strings.Contains(sc.logs.String(), `"pull_request":21`) {
		t.Errorf("the log does not name the pull request that was checked:\n%s", sc.logs.String())
	}
}

// The agent commits in the worktree and does not push. The head of the pull
// request is then behind the head of the worktree.
func TestI2_DoneWithACommitThatIsNotPushedStopsTheIssue(t *testing.T) {
	sc := newScene(t, cliOptions{commit: true})
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	service := sc.service()

	sc.pollAndWait(t, service)

	assertVerificationFailed(t, sc, "the head commit of the worktree is not pushed", workflow.FailureHeadNotPushed, 21)
}

// I2 (issue-states.md): GitHub made no closing link from "Closes #10". The
// pull request is found on the branch that cumin chose, cumin-core adds
// exactly one link, reads it back, and the issue moves on.
func TestI2_APullRequestWithoutALinkGetsExactlyOneLink(t *testing.T) {
	sc := newScene(t)
	sc.addUnlinkedPullRequest(21, sc.remoteHead)
	service := sc.service()

	sc.pollAndWait(t, service)

	if n := closingLinkRequests(sc); n != 1 {
		t.Errorf("%d closing link requests, want 1", n)
	}
	if got := sc.fake.PullRequestCloses(sc.repo, 21); !slices.Equal(got, []int{10}) {
		t.Errorf("pull request #21 closes %v, want [10]", got)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", "cumin/status/awaiting-checks"}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/awaiting-checks", got)
	}
	logs := sc.logs.String()
	for _, want := range []string{`"msg":"I2: added the closing link"`, `"msg":"I2: verified the pull request"`, `"pull_request":21`} {
		if !strings.Contains(logs, want) {
			t.Errorf("the log has no %s:\n%s", want, logs)
		}
	}
	if n := len(sc.fake.Comments(sc.repo, 10)); n != 0 {
		t.Errorf("%d comments on #10, want none", n)
	}
}

// When GitHub already linked the pull request, cumin adds nothing.
func TestI2_ALinkedPullRequestGetsNoLink(t *testing.T) {
	sc := newScene(t)
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	service := sc.service()

	sc.pollAndWait(t, service)

	if n := closingLinkRequests(sc); n != 0 {
		t.Errorf("%d closing link requests, want none", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, "cumin/status/awaiting-checks") {
		t.Errorf("labels of #10 = %v, want cumin/status/awaiting-checks", got)
	}
}

// A pull request of the Implementer App on another branch is not the pull
// request of the issue, even when it is linked: the issue stops as today.
func TestI2_APullRequestOnAnotherBranchIsNotTaken(t *testing.T) {
	sc := newScene(t)
	sc.fake.AddPullRequest(sc.repo, &githubtest.PullRequest{
		Number: 21, HeadCommit: sc.remoteHead, Author: implementerSlug, AuthorIsBot: true, HeadBranch: "cumin/10-another-branch",
	})
	service := sc.service()

	sc.pollAndWait(t, service)

	assertVerificationFailed(t, sc, "no open pull request is on the branch of the issue", workflow.FailureNoOpenPullRequest, 0)
	if n := closingLinkRequests(sc); n != 0 {
		t.Errorf("%d closing link requests, want none", n)
	}
}

// A closing link that GitHub refuses stops the issue once, and the comment
// and the notification name the answer of GitHub.
func TestI2_AFailedLinkStopsTheIssueOnce(t *testing.T) {
	sc := newScene(t)
	sc.addUnlinkedPullRequest(21, sc.remoteHead)
	sc.fake.SetLinkErrors("Resource not accessible by integration")
	service := sc.service()

	sc.pollAndWait(t, service)

	if n := closingLinkRequests(sc); n != 1 {
		t.Errorf("%d closing link requests, want 1", n)
	}
	assertStoppedAtI2(t, sc, workflow.LinkFailedReason(21, "Resource not accessible by integration"), 21)
	if !strings.Contains(sc.fake.Comments(sc.repo, 10)[0].Body, "GitHub answered: Resource not accessible by integration.") {
		t.Errorf("the comment does not name the answer of GitHub:\n%s", sc.fake.Comments(sc.repo, 10)[0].Body)
	}
}

// A link that GitHub accepted and the issue does not show stops the issue
// once.
func TestI2_ALinkThatIsMissingAfterwardsStopsTheIssueOnce(t *testing.T) {
	sc := newScene(t)
	sc.addUnlinkedPullRequest(21, sc.remoteHead)
	sc.fake.IgnoreLinks()
	service := sc.service()

	sc.pollAndWait(t, service)

	assertStoppedAtI2(t, sc, workflow.LinkMissingReason(21), 21)
}

// An issue that GitHub already links to as many open pull requests as the
// poll reads gets no more links: one more would make every later poll of
// the repository fail. The issue stops once instead.
func TestI2_ALinkOverTheLimitOfThePollIsNotAdded(t *testing.T) {
	sc := newScene(t)
	// Two pull requests without an author already close #10; #19 is on the
	// branch of the issue, so the claim continues on that branch. The
	// Implementer App opened #21 on the same branch, and GitHub linked it
	// to nothing.
	sc.fake.AddPullRequest(sc.repo, &githubtest.PullRequest{Number: 18, HeadCommit: sc.remoteHead, HeadBranch: "cumin/10-old", Closes: []int{10}})
	sc.fake.AddPullRequest(sc.repo, &githubtest.PullRequest{Number: 19, HeadCommit: sc.remoteHead, HeadBranch: wantBranch, Closes: []int{10}})
	sc.addUnlinkedPullRequest(21, sc.remoteHead)
	service := sc.service()

	sc.pollAndWait(t, service)

	if n := closingLinkRequests(sc); n != 0 {
		t.Errorf("%d closing link requests, want none", n)
	}
	assertVerificationFailed(t, sc, "the issue has too many open closing pull requests for one more link", workflow.FailureTooManyLinks, 21)
}

// closingLinkRequests counts the requests of the closing link
// (addCloseIssueReferences).
func closingLinkRequests(sc *scene) int {
	n := 0
	for _, r := range sc.fake.Requests() {
		if r.Path == "/graphql" && strings.Contains(string(r.Body), "addCloseIssueReferences") {
			n++
		}
	}
	return n
}

// A blocked result is logged with the question of blocked_reason. The label
// stays; #81 posts the comment and asks the Owner.
// I2 with a blocked result (issue-states.md): cumin posts the
// blocked_reason on the issue, replaces the label with
// cumin/status/awaiting-owner-decision, and notifies the Owner once. It
// does not retry.
func TestI2_BlockedStopsTheIssueForTheOwner(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "blocked.jsonl"})
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	service := sc.service()

	sc.pollAndWait(t, service)

	const question = "## Decision needed: which sign-in method does the login screen use?"
	comments := sc.fake.Comments(sc.repo, 10)
	if len(comments) != 1 || comments[0].Body != question {
		t.Fatalf("the comments of #10 = %+v, want one with the blocked reason", comments)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", "cumin/status/awaiting-owner-decision"}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/awaiting-owner-decision", got)
	}

	messages := sc.webhook.messagesSent()
	if len(messages) != 1 {
		t.Fatalf("%d notifications, want 1: %v", len(messages), messages)
	}
	for _, want := range []string{"I2", question, "example-org/example-repo", "issue #10", "issuecomment-"} {
		if !strings.Contains(messages[0], want) {
			t.Errorf("the notification has no %q:\n%s", want, messages[0])
		}
	}

	// A blocked result is never retried.
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1", n)
	}
	logs := sc.logs.String()
	for _, want := range []string{`"msg":"I2: the agent returned blocked"`, `"msg":"I2: wrote the reason on the issue"`,
		`"msg":"I2: the issue waits for the Owner"`, `"msg":"the Owner was notified"`, `"row":"I2"`} {
		if !strings.Contains(logs, want) {
			t.Errorf("the log has no %s:\n%s", want, logs)
		}
	}
}

// A webhook that fails changes nothing on GitHub: the comment and the
// label stay, and the failure is logged at error level.
func TestI2_BlockedWithAFailedWebhookKeepsTheCommentAndTheLabel(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "blocked.jsonl"})
	sc.webhook.fails(http.StatusInternalServerError)
	service := sc.service()

	sc.pollAndWait(t, service)

	if n := len(sc.fake.Comments(sc.repo, 10)); n != 1 {
		t.Errorf("%d comments on #10, want 1", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, "cumin/status/awaiting-owner-decision") {
		t.Errorf("labels of #10 = %v, want cumin/status/awaiting-owner-decision", got)
	}
	logs := sc.logs.String()
	if !strings.Contains(logs, `"level":"ERROR","msg":"the Owner was not notified"`) {
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
func TestI2_BlockedWithNotificationsOffWritesOnlyOnGitHub(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "blocked.jsonl"})
	sc.notifications = false
	service := sc.service()

	sc.pollAndWait(t, service)

	if n := len(sc.fake.Comments(sc.repo, 10)); n != 1 {
		t.Errorf("%d comments on #10, want 1", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, "cumin/status/awaiting-owner-decision") {
		t.Errorf("labels of #10 = %v, want cumin/status/awaiting-owner-decision", got)
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
func TestI2_BlockedWithoutAChannelIsLoggedAtErrorLevel(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "blocked.jsonl"})
	sc.notifier = notify.New(nil)
	service := sc.service()

	sc.pollAndWait(t, service)

	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, "cumin/status/awaiting-owner-decision") {
		t.Errorf("labels of #10 = %v, want cumin/status/awaiting-owner-decision", got)
	}
	if !strings.Contains(sc.logs.String(), `"level":"ERROR","msg":"the Owner was not notified"`) {
		t.Errorf("the log does not report the missing channel at error level:\n%s", sc.logs.String())
	}
}

// assertVerificationFailed checks that the label of #10 stayed at
// cumin/status/implementing and that the log names the failure.

// Core-5 (cumin-core.md): a result that does not match the schema gives
// exactly one retry. After the second abnormal end the issue goes to the
// Owner, with one comment, the label, and exactly one notification.
func TestCore05_AnInvalidResultIsRetriedOnceAndThenGoesToTheOwner(t *testing.T) {
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
	for _, want := range []string{"## Stopped for the Owner", "Row: I2", "invalid result", "Retried: once", "Pull request: None"} {
		if !strings.Contains(body, want) {
			t.Errorf("the comment has no %q:\n%s", want, body)
		}
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", "cumin/status/awaiting-owner-decision"}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/awaiting-owner-decision", got)
	}
	messages := sc.webhook.messagesSent()
	if len(messages) != 1 {
		t.Fatalf("%d notifications, want 1: %v", len(messages), messages)
	}
	for _, want := range []string{"I2", "invalid result", "example-org/example-repo", "issue #10"} {
		if !strings.Contains(messages[0], want) {
			t.Errorf("the notification has no %q:\n%s", want, messages[0])
		}
	}
	logs := sc.logs.String()
	if !strings.Contains(logs, `"msg":"I2: the same request runs again in the same work directory"`) {
		t.Errorf("the log does not say that the request ran again:\n%s", logs)
	}
}

// The retry is the same request in the same work directory, and it starts
// a new session: no session of the first run is resumed.
func TestI2_TheRetryIsTheSameRequestInANewSession(t *testing.T) {
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
	// prepared (which the Workspace gives without resolving symlinks).
	if got, want := promptOf(t, args), workflow.ImplementRequestText("example-org/example-repo", 10, wantBranch, wantDir); got != want {
		t.Errorf("the request text of the retry = %q, want %q", got, want)
	}
}

// A retry that ends normally goes on as usual: the verification runs, the
// label moves to cumin/status/awaiting-checks, and nothing is said to the
// Owner.
func TestI2_ARetryThatEndsWellIsVerified(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "is-error.jsonl", secondFixture: "done.jsonl"})
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	service := sc.service()

	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 2 {
		t.Fatalf("%d agent runs, want 2", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", "cumin/status/awaiting-checks"}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/awaiting-checks", got)
	}
	if n := len(sc.fake.Comments(sc.repo, 10)); n != 0 {
		t.Errorf("%d comments on #10, want none", n)
	}
	if messages := sc.webhook.messagesSent(); len(messages) != 0 {
		t.Errorf("%d notifications, want none: %v", len(messages), messages)
	}
}

// The two runs can end in different ways. The comment names both kinds,
// so that the Owner knows where to look.
func TestI2_TheStopNoteNamesTheKindOfEachAbnormalEnd(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "no-result.jsonl", secondFixture: "invalid-result.jsonl"})
	service := sc.service()

	sc.pollAndWait(t, service)

	comments := sc.fake.Comments(sc.repo, 10)
	if len(comments) != 1 {
		t.Fatalf("%d comments on #10, want 1", len(comments))
	}
	for _, want := range []string{"no result", "invalid result", "Retried: once"} {
		if !strings.Contains(comments[0].Body, want) {
			t.Errorf("the comment has no %q:\n%s", want, comments[0].Body)
		}
	}
}

// The stop note names the pull request that the agent left behind, so that
// the Owner knows whether the work reached GitHub.
func TestI2_TheStopNoteOfAnAbnormalEndNamesThePullRequest(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "no-init.jsonl"})
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	service := sc.service()

	sc.pollAndWait(t, service)

	comments := sc.fake.Comments(sc.repo, 10)
	if len(comments) != 1 {
		t.Fatalf("%d comments on #10, want 1", len(comments))
	}
	for _, want := range []string{"Pull request: #21", "user-level context", "Retried: once"} {
		if !strings.Contains(comments[0].Body, want) {
			t.Errorf("the comment has no %q:\n%s", want, comments[0].Body)
		}
	}
}

// assertVerificationFailed checks the whole failed path of I2: the log
// names the check that failed, the issue holds one comment in the form of
// templates/stop-note.md with the sentence of that check, the label is
// cumin/status/awaiting-owner-decision, and exactly one notification went
// out with the same sentence. pullRequest is the number that the comment
// must name, or 0 for "None".
func assertVerificationFailed(t *testing.T, sc *scene, failure string, kind workflow.VerificationFailure, pullRequest int) {
	t.Helper()
	logs := sc.logs.String()
	if !strings.Contains(logs, `"msg":"I2: the verification failed"`) {
		t.Errorf("the log does not say that the verification failed:\n%s", logs)
	}
	if !strings.Contains(logs, `"failure":"`+failure+`"`) {
		t.Errorf("the log does not name the failure %q:\n%s", failure, logs)
	}

	assertStoppedAtI2(t, sc, workflow.VerificationReason(kind), pullRequest)
}

// assertStoppedAtI2 checks the stop step of I2 for #10: one comment in the
// form of templates/stop-note.md with the reason, the label
// cumin/status/awaiting-owner-decision, and exactly one notification with
// the same reason.
func assertStoppedAtI2(t *testing.T, sc *scene, reason string, pullRequest int) {
	t.Helper()
	comments := sc.fake.Comments(sc.repo, 10)
	if len(comments) != 1 {
		t.Fatalf("%d comments on #10, want 1: %+v", len(comments), comments)
	}
	body := comments[0].Body
	want := []string{"## Stopped for the Owner", "Row: I2", "Reason: " + reason, "Retried: no", "cumin/status/ready"}
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

	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", "cumin/status/awaiting-owner-decision"}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/awaiting-owner-decision", got)
	}
	if n := sc.fake.CountRequests(http.MethodPut, putLabelsPath); n != 2 {
		t.Errorf("%d label changes, want 2 (the claim and the stop)", n)
	}

	messages := sc.webhook.messagesSent()
	if len(messages) != 1 {
		t.Fatalf("%d notifications, want 1: %v", len(messages), messages)
	}
	for _, line := range []string{"I2", reason, "example-org/example-repo", "issue #10"} {
		if !strings.Contains(messages[0], line) {
			t.Errorf("the notification has no %q:\n%s", line, messages[0])
		}
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
	if n := sc.fake.CountRequests(http.MethodPost, "/repos/example-org/example-repo/labels"); n != 12 {
		t.Errorf("%d labels created, want 12", n)
	}
	if got := sc.fake.LabelNames(sc.repo); len(got) != 12 || !slices.Contains(got, "cumin/status/ready") {
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
	if n := sc.fake.CountRequests(http.MethodPost, "/repos/example-org/example-repo/labels"); n != 12 {
		t.Errorf("%d labels created after the second start, want 12 still", n)
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
	return argumentOf(t, args, "-p")
}

// systemPromptOf returns the instruction of the role from the recorded
// arguments: the value of --append-system-prompt.
func systemPromptOf(t *testing.T, args string) string {
	t.Helper()
	return argumentOf(t, args, "--append-system-prompt")
}

// argumentOf returns the value that follows flag in the recorded
// arguments, which are separated by NUL.
func argumentOf(t *testing.T, args, flag string) string {
	t.Helper()
	list := strings.Split(strings.TrimSuffix(args, "\x00"), "\x00")
	for i, arg := range list {
		if arg == flag && i+1 < len(list) {
			return list[i+1]
		}
	}
	t.Fatalf("the arguments have no %s: %q", flag, list)
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

// waitForAgentRun waits until the fake CLI of the agent has started.
func waitForAgentRun(t *testing.T, sc *scene) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if sc.agentRuns(t) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the agent run did not start:\n%s", sc.logs.String())
}

// The stop of cumin: SIGINT or SIGTERM ends the context, the request that
// is going on is cancelled, Run waits only for the grace, logs the issues
// that were in progress, and returns nil so that the command exits with 0.
// The labels stay as they are; the Owner restarts an issue with
// cumin/status/ready.
func TestCore_StopCancelsTheRunningRequestAndLogsTheIssue(t *testing.T) {
	// A CLI that ignores SIGTERM: the adapter sends SIGKILL after its
	// grace, which is 200 ms in the scene.
	sc := newScene(t, cliOptions{sleeps: true, ignoresTerm: true})
	service := sc.service()
	service.PollInterval = 10 * time.Millisecond
	service.StopGrace = 3 * time.Second

	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan error, 1)
	go func() { returned <- service.Run(ctx) }()
	waitForAgentRun(t, sc)

	start := time.Now()
	cancel()
	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("Run returned %v, want nil so that the command exits with 0", err)
		}
		if elapsed := time.Since(start); elapsed > service.StopGrace {
			t.Errorf("Run returned after %s, want less than the grace (%s)", elapsed, service.StopGrace)
		}
	case <-time.After(10 * time.Second):
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
	service.StopGrace = 30 * time.Second

	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan error, 1)
	go func() { returned <- service.Run(ctx) }()
	waitForAgentRun(t, sc)

	start := time.Now()
	cancel()
	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if elapsed := time.Since(start); elapsed > 10*time.Second {
			t.Errorf("Run returned after %s, want as soon as the request ended", elapsed)
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("Run did not return after the stop:\n%s", sc.logs.String())
	}
	logs := sc.logs.String()
	if !strings.Contains(logs, `"ended_within_grace":true`) {
		t.Errorf("the stop log does not say that the request ended:\n%s", logs)
	}
	// The issue was in progress when the signal came, so the line names it
	// even though its run ended at once. The issue keeps
	// cumin/status/implementing, and the Owner has to restart it.
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

// The risk criteria of the target repository reaches the agent as the last
// part of its instruction. cumin resolves the three levels for the
// repository and passes the text with the start request; internal/agent
// joins it to the role file, the discipline file, and the writing rules
// (docs/ja/requirements/agents/common.md, the section on the composition
// of the instruction).
func TestI1_TheInstructionEndsWithTheRiskCriteriaOfTheRepository(t *testing.T) {
	const criteria = "# Risk criteria of this repository\n\nEvery change is risk/high.\n"
	sc := newScene(t)
	sc.fake.SetFile(sc.repo, ".cumin/risk-criteria.md", githubtest.File{Content: criteria})
	service := sc.service()

	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	service.Wait()

	instruction := systemPromptOf(t, sc.record(t, "agent.args"))
	if !strings.HasSuffix(instruction, criteria) {
		t.Errorf("the instruction does not end with the risk criteria of the repository:\n%s", instruction)
	}
	for _, want := range []string{"# Implementer", "# Software engineering for the Implementer"} {
		if !strings.Contains(instruction, want) {
			t.Errorf("the instruction does not hold %q", want)
		}
	}
}

// TestI1_TheSessionOfARunIsKeptAndAClaimForgetsTheOldOne covers what the
// state file of the Host is for: a request in the same session (I4, a check
// failed) needs the session of the last run, and a claim (I1) after the
// Owner added cumin/status/ready must forget the old session and the count
// of check fixes.
func TestI1_TheSessionOfARunIsKeptAndAClaimForgetsTheOldOne(t *testing.T) {
	sc := newScene(t)
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	path := filepath.Join(t.TempDir(), "state.json")
	store := state.Open(path, nil)
	// What an earlier round left behind for this issue.
	if err := store.Set(sc.repo.Owner+"/"+sc.repo.Name, 10, state.Issue{SessionID: "old-session", CheckFixRequests: 2}); err != nil {
		t.Fatal(err)
	}
	service := sc.service()
	service.State = store

	sc.pollAndWait(t, service)

	// The session of the run that just ended, and the count back at zero.
	const repository = "example-org/example-repo"
	got := store.Issue(repository, 10)
	if got.SessionID != "11111111-2222-4333-8444-555555555555" {
		t.Errorf("session = %q, want the session of the run", got.SessionID)
	}
	if got.CheckFixRequests != 0 {
		t.Errorf("check fix requests = %d, want 0 after a claim", got.CheckFixRequests)
	}
	// A restart of cumin reads the same session back.
	if again := state.Open(path, nil).Issue(repository, 10); again != got {
		t.Errorf("after a restart: %+v, want %+v", again, got)
	}
}

// TestI1_AStateThatCannotBeClearedStopsTheClaim: the state of an issue must
// be cleared before the label changes. A stale entry would make a request in
// the same session (I4) resume the session from before the Owner added
// cumin/status/ready. The issue keeps its label, so the next poll tries
// again.
func TestI1_AStateThatCannotBeClearedStopsTheClaim(t *testing.T) {
	sc := newScene(t)
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	// A directory that cumin cannot write in.
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	store := state.Open(filepath.Join(dir, "state.json"), nil)
	if err := store.Set("example-org/example-repo", 10, state.Issue{SessionID: "old-session"}); err == nil {
		t.Skip("the test user can write in a directory with mode 500")
	}
	service := sc.service()
	service.State = store

	err := service.Poll(context.Background())
	service.Wait()

	if err == nil || !strings.Contains(err.Error(), "clear the state") {
		t.Fatalf("err = %v, want the failed state write", err)
	}
	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelReady) {
		t.Errorf("labels of #10 = %v, want cumin/status/ready to stay", got)
	}
}

// TestI1_AClaimWithoutAStateFileWorks: a Host without the file keeps
// nothing, as a Host that just lost it does.
func TestI1_AClaimWithoutAStateFileWorks(t *testing.T) {
	sc := newScene(t)
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	service := sc.service()
	service.State = nil

	sc.pollAndWait(t, service)

	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelAwaitingChecks) {
		t.Errorf("labels of #10 = %v, want cumin/status/awaiting-checks", got)
	}
}

// awaitingChecks puts issue #10 in cumin/status/awaiting-checks with one
// open pull request of the Implementer App, and gives the repository the
// required checks. checks are the results on the head commit.
func (sc *scene) awaitingChecks(t *testing.T, required []string, checks []githubtest.Check) {
	t.Helper()
	// Issue returns a copy, so the label is set by replacing the issue.
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{
		Number: 10, Parent: 6, Title: subIssueTitle,
		Labels: []string{"cumin/status/awaiting-checks", "risk/low"},
	})
	sc.repo.DefaultBranch = "main"
	for _, name := range required {
		sc.repo.RequiredChecks = append(sc.repo.RequiredChecks, githubtest.RequiredCheck{Name: name})
	}
	sc.fake.AddPullRequest(sc.repo, &githubtest.PullRequest{
		Number: 21, HeadCommit: sc.remoteHead, HeadBranch: "cumin/10-add-the-login-screen",
		Author: implementerSlug, AuthorIsBot: true, Closes: []int{10}, Checks: checks,
	})
}

const branchRulesPath = "/repos/example-org/example-repo/rules/branches/main"

// TestI3_EveryRequiredCheckPassedMovesTheIssueToTheReview is the success
// path of I3: the poll reads the required checks, they all passed on the
// head commit, and the issue waits for the Reviewer.
func TestI3_EveryRequiredCheckPassedMovesTheIssueToTheReview(t *testing.T) {
	sc := newScene(t, cliOptions{reviews: []string{"APPROVE"}})
	sc.awaitingChecks(t, []string{"ci", "cumin-protected-paths"}, []githubtest.Check{
		{Name: "ci", Conclusion: "SUCCESS"},
		{Name: "cumin-protected-paths", Conclusion: "SKIPPED"},
		{Name: "optional", Conclusion: "FAILURE"},
	})
	service := sc.service()

	sc.pollAndWait(t, service)

	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", workflow.LabelReviewing}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/reviewing", got)
	}
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want one Reviewer run", n)
	}
	for _, want := range []string{"I3: the pull request is ready for review", "I3: the Reviewer approved the head commit"} {
		if !strings.Contains(sc.logs.String(), want) {
			t.Errorf("the log does not say %q: %s", want, sc.logs)
		}
	}
	// The issue leaves awaiting-checks, so the next poll asks for nothing.
	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	if n := sc.fake.CountRequests(http.MethodGet, branchRulesPath); n != 1 {
		t.Errorf("%d reads of the required checks, want 1", n)
	}
	if n := sc.fake.CountRequests(http.MethodPut, putLabelsPath); n != 1 {
		t.Errorf("%d label changes, want 1", n)
	}
}

// TestI3_AnEmptyListOfRequiredChecksPassesAtOnce: a repository without a
// ruleset moves to the review in the next poll (issue-states.md).
func TestI3_AnEmptyListOfRequiredChecksPassesAtOnce(t *testing.T) {
	sc := newScene(t, cliOptions{reviews: []string{"APPROVE"}})
	sc.awaitingChecks(t, nil, nil)
	service := sc.service()

	sc.pollAndWait(t, service)

	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelReviewing) {
		t.Errorf("labels of #10 = %v, want cumin/status/reviewing", got)
	}
}

// TestI3_AFailedOrRunningCheckKeepsTheIssueWaiting: I4 answers a failure,
// and a check that has not finished is not an answer at all.
func TestI3_AFailedOrRunningCheckKeepsTheIssueWaiting(t *testing.T) {
	for _, tc := range []struct {
		name  string
		check githubtest.Check
	}{
		{name: "failed", check: githubtest.Check{Name: "ci", Conclusion: "FAILURE"}},
		{name: "running", check: githubtest.Check{Name: "ci", Status: "IN_PROGRESS"}},
		{name: "not reported", check: githubtest.Check{Name: "other", Conclusion: "SUCCESS"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc := newScene(t)
			sc.awaitingChecks(t, []string{"ci"}, []githubtest.Check{tc.check})
			service := sc.service()

			sc.pollAndWait(t, service)

			if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelAwaitingChecks) {
				t.Errorf("labels of #10 = %v, want cumin/status/awaiting-checks to stay", got)
			}
		})
	}
}

// TestI3_TheRequiredChecksAreReadOnlyWhenAnIssueWaits keeps the extra REST
// call out of a poll that has nothing to decide.
func TestI3_TheRequiredChecksAreReadOnlyWhenAnIssueWaits(t *testing.T) {
	sc := newScene(t)
	sc.repo.DefaultBranch = "main"
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	service := sc.service()

	sc.pollAndWait(t, service)

	if n := sc.fake.CountRequests(http.MethodGet, branchRulesPath); n != 0 {
		t.Errorf("%d reads of the required checks, want none while no issue waits", n)
	}
}

// Core-14 (cumin-core.md): cumin changes a label of the issue, the Owner
// changes the risk of the issue, and the Owner changes a label of the pull
// request. After each, the next poll makes the cumin/status/* and risk/*
// labels of the pull request equal to those of the issue (I11), and the
// labels of the pull request change no decision (principle 5).
func TestCore14_TheLabelsOfThePullRequestFollowTheIssue(t *testing.T) {
	sc := newScene(t)
	// A pull request of another author: the run ends, I2 stops the issue for
	// the Owner, and no later rule moves it again.
	sc.addPullRequest(21, sc.remoteHead, "someone", false)
	service := sc.service()
	prLabelsPath := "/repos/example-org/example-repo/issues/21/labels"
	assertEqual := func(step string) {
		t.Helper()
		issue := sc.fake.Issue(sc.repo, 10).Labels
		pr := sc.fake.PullRequestLabels(sc.repo, 21)
		if !sameLabelsAnyOrder(workflow.PullRequestLabels(issue, pr), pr) {
			t.Errorf("%s: labels of the pull request = %v, want the status and risk of the issue %v", step, pr, issue)
		}
	}

	// cumin changes the label of the issue: I1, then I2 stops it.
	sc.pollAndWait(t, service)
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelAwaitingOwnerDecision) {
		t.Fatalf("labels of #10 = %v, want cumin/status/awaiting-owner-decision", got)
	}
	sc.pollAndWait(t, service)
	assertEqual("after cumin changed the issue")

	// The Owner changes the risk of the issue.
	if err := sc.fake.SetLabels(sc.repo, 10, []string{"risk/high", workflow.LabelAwaitingOwnerDecision}); err != nil {
		t.Fatal(err)
	}
	sc.pollAndWait(t, service)
	assertEqual("after the Owner changed the risk of the issue")

	// The Owner changes the labels of the pull request, and adds
	// cumin/status/ready there. The issue is not claimed.
	if err := sc.fake.SetLabels(sc.repo, 21, []string{"docs", workflow.LabelReady, "risk/low"}); err != nil {
		t.Fatal(err)
	}
	sc.pollAndWait(t, service)
	assertEqual("after the Owner changed the pull request")
	if got := sc.fake.PullRequestLabels(sc.repo, 21); !slices.Contains(got, "docs") {
		t.Errorf("labels of the pull request = %v, want the label docs kept", got)
	}
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1: the labels of a pull request decide nothing", n)
	}

	// Equal labels cause no write.
	writes := sc.fake.CountRequests(http.MethodPut, prLabelsPath)
	sc.pollAndWait(t, service)
	if n := sc.fake.CountRequests(http.MethodPut, prLabelsPath); n != writes {
		t.Errorf("%d writes to the pull request after a poll with equal labels, want %d", n, writes)
	}
	if !strings.Contains(sc.logs.String(), `"msg":"I11: copied the labels of the issue to the pull request"`) {
		t.Error("the log has no line of I11")
	}
}

// sameLabelsAnyOrder reports whether a and b hold the same labels.
func sameLabelsAnyOrder(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// I1 (issue-states.md, implementer.md): the Owner added cumin/status/ready
// again to an issue whose pull request is open. The claim prepares the
// worktree on the branch of that pull request, not on the branch of the
// title, and asks to continue in the same pull request, in a new session.
// A worktree that an earlier round left on another branch is replaced.
func TestI1_AClaimWithAnOpenPullRequestContinuesOnItsBranch(t *testing.T) {
	sc := newScene(t)
	const branch = "cumin/10-an-older-title"
	head := sc.pushBranch(t, branch)
	sc.fake.AddPullRequest(sc.repo, &githubtest.PullRequest{
		Number: 21, HeadCommit: head, HeadBranch: branch, Author: implementerSlug, AuthorIsBot: true, Closes: []int{10},
	})
	service := sc.service()
	// An earlier round left a worktree on the branch of the title, at main.
	earlier, err := service.Workspace.Prepare(context.Background(), sc.remote, agent.Checkout{
		Owner: "example-org", Repo: "example-repo", Issue: 10, Role: config.RoleImplementer, Branch: wantBranch,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := branchOf(t, earlier); got != wantBranch {
		t.Fatalf("the earlier worktree is on %q, want %q", got, wantBranch)
	}

	sc.pollAndWait(t, service)

	dir := filepath.Join(sc.workRoot, "example-org", "example-repo", "10-implementer")
	if got := branchOf(t, dir); got != branch {
		t.Errorf("branch of the worktree = %q, want the branch of the pull request %q", got, branch)
	}
	if got := git(t, dir, "rev-parse", "HEAD"); got != head {
		t.Errorf("head of the worktree = %s, want the head of the pull request %s", got, head)
	}
	args := sc.record(t, "agent.args")
	text := promptOf(t, args)
	for _, want := range []string{"Request: continue", "Pull request: #21", "Branch: " + branch, "Do not open a new pull request"} {
		if !strings.Contains(text, want) {
			t.Errorf("the request text has no %q:\n%s", want, text)
		}
	}
	if strings.Contains(args, "--resume") {
		t.Error("a claim resumed a session; I1 always starts a new one")
	}
	// The agent made no commit, so the head of the worktree is the head of
	// the pull request, and I2 passes on it.
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", "cumin/status/awaiting-checks"}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/awaiting-checks", got)
	}
	logs := sc.logs.String()
	for _, want := range []string{`"kind":"continue"`, `"branch":"` + branch + `"`, `"pull_request":21`} {
		if !strings.Contains(logs, want) {
			t.Errorf("the log has no %s", want)
		}
	}
}

// pushBranch adds one commit on a new branch of the remote, as the
// Implementer did in an earlier round, and returns the commit.
func (sc *scene) pushBranch(t *testing.T, branch string) string {
	t.Helper()
	work := filepath.Join(t.TempDir(), "work")
	git(t, filepath.Dir(work), "clone", "--quiet", sc.remote, work)
	git(t, work, "switch", "--quiet", "-c", branch)
	if err := os.WriteFile(filepath.Join(work, "login.txt"), []byte("the earlier work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", "login.txt")
	git(t, work, "commit", "--quiet", "-m", "add the earlier work")
	git(t, work, "push", "--quiet", "origin", branch)
	return git(t, work, "rev-parse", "HEAD")
}

// I1: a run that cumin stopped leaves its work in the worktree, and the
// Owner restarts the issue with cumin/status/ready. The continuation keeps
// that worktree, because its work is not on GitHub.
func TestI1_AContinuationKeepsAWorktreeWithWorkThatIsNotPushed(t *testing.T) {
	sc := newScene(t)
	const branch = "cumin/10-an-older-title"
	head := sc.pushBranch(t, branch)
	sc.fake.AddPullRequest(sc.repo, &githubtest.PullRequest{
		Number: 21, HeadCommit: head, HeadBranch: branch, Author: implementerSlug, AuthorIsBot: true, Closes: []int{10},
	})
	service := sc.service()
	earlier, err := service.Workspace.Prepare(context.Background(), sc.remote, agent.Checkout{
		Owner: "example-org", Repo: "example-repo", Issue: 10, Role: config.RoleImplementer, Branch: branch,
	})
	if err != nil {
		t.Fatal(err)
	}
	unpushed := filepath.Join(earlier, "unpushed.txt")
	if err := os.WriteFile(unpushed, []byte("the work of a stopped run\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sc.pollAndWait(t, service)

	if _, err := os.Stat(unpushed); err != nil {
		t.Errorf("the work that is not pushed is gone: %v", err)
	}
	if !strings.Contains(sc.logs.String(), `"msg":"I1: the worktree of an earlier round holds work that is not on GitHub; it is used as it is"`) {
		t.Error("the log does not say that the worktree was kept")
	}
	if text := promptOf(t, sc.record(t, "agent.args")); !strings.Contains(text, "Request: continue") {
		t.Errorf("the request is not a continuation:\n%s", text)
	}
}

// failingCheck makes the required check "ci" of the pull request of #10
// fail, with an annotation and a job log that FailedCheckContent reads, and
// gives the scene a state file with the session of an earlier run and
// count check fix requests. It returns the path of the state file.
func (sc *scene) failingCheck(t *testing.T, service *workflow.Service, count int) string {
	t.Helper()
	sc.awaitingChecks(t, []string{"ci"}, []githubtest.Check{{Name: "ci", Conclusion: "FAILURE"}})
	sc.fake.AddCheckRun(sc.repo, sc.remoteHead, githubtest.CheckRun{
		ID: 7, Name: "ci", Conclusion: "failure", JobID: 42,
		Annotations: []githubtest.Annotation{{Path: "login.go", Level: "failure", Message: "login_test.go:12: want 2, got 1"}},
		JobLog:      "step 1\nFAIL\texample/login\n",
	})
	path := filepath.Join(t.TempDir(), "state.json")
	service.State = state.Open(path, nil)
	if err := service.State.Set("example-org/example-repo", 10, state.Issue{SessionID: "earlier-session", CheckFixRequests: count}); err != nil {
		t.Fatal(err)
	}
	return path
}

// I4 (issue-states.md): a failed required check moves the issue back to
// cumin/status/implementing and sends one request of the kind "check fix",
// in the session of the last run, with what the failed check says. The
// label changes first, so the polls that follow send nothing more. Here the
// fix is not pushed, so I2 stops the issue after the run.
func TestI4_AFailedCheckGivesOneFixRequestInTheSameSession(t *testing.T) {
	sc := newScene(t, cliOptions{commit: true})
	service := sc.service()
	path := sc.failingCheck(t, service, 0)
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
	args := sc.record(t, "agent.args")
	if got := argumentOf(t, args, "--resume"); got != "earlier-session" {
		t.Errorf("--resume = %q, want the session of the last run", got)
	}
	text := promptOf(t, args)
	for _, want := range []string{"Request: check fix", "Pull request: #21", "Branch: " + wantBranch,
		`Check "ci" failed.`, "login.go: login_test.go:12: want 2, got 1", "FAIL\texample/login", "Do not open a new pull request"} {
		if !strings.Contains(text, want) {
			t.Errorf("the request text has no %q:\n%s", want, text)
		}
	}
	// The worktree of the issue is on the branch of the pull request.
	dir := filepath.Join(sc.workRoot, "example-org", "example-repo", "10-implementer")
	if got := branchOf(t, dir); got != wantBranch {
		t.Errorf("branch of the worktree = %q, want %q", got, wantBranch)
	}
	if got := state.Open(path, nil).Issue("example-org/example-repo", 10).CheckFixRequests; got != 1 {
		t.Errorf("check fix requests after a restart = %d, want 1", got)
	}
	logs := sc.logs.String()
	for _, want := range []string{`"msg":"I4: a required check failed; the issue goes back to the Implementer"`,
		`"msg":"I4: requested the work"`, `"kind":"check fix"`, `"resumed":true`} {
		if !strings.Contains(logs, want) {
			t.Errorf("the log has no %s", want)
		}
	}
}

// I4, then I2 (issue-states.md): a fix that ends with done is verified
// again, and a verified pull request returns to cumin/status/awaiting-checks.
func TestI4_ADoneFixIsVerifiedAgain(t *testing.T) {
	sc := newScene(t)
	service := sc.service()
	sc.failingCheck(t, service, 1)

	sc.pollAndWait(t, service)

	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", workflow.LabelAwaitingChecks}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/awaiting-checks", got)
	}
	// The claim and I2: two label changes; the stop was not used.
	if n := sc.fake.CountRequests(http.MethodPut, putLabelsPath); n != 2 {
		t.Errorf("%d label changes, want 2 (I4 and I2)", n)
	}
	got := service.State.Issue("example-org/example-repo", 10)
	if got.CheckFixRequests != 2 {
		t.Errorf("check fix requests = %d, want 2", got.CheckFixRequests)
	}
	if got.SessionID != "11111111-2222-4333-8444-555555555555" {
		t.Errorf("session = %q, want the session of the fix run", got.SessionID)
	}
}

// I4 (issue-states.md): at max_check_fix_requests, cumin sends no request.
// The issue gets one comment, cumin/status/awaiting-owner-decision, and one
// notification, with the row I4. After the Owner adds cumin/status/ready,
// the next request starts a new session and the count starts at zero.
func TestI4_TheLimitStopsTheIssueForTheOwner(t *testing.T) {
	sc := newScene(t)
	service := sc.service()
	sc.failingCheck(t, service, 3)

	sc.pollAndWait(t, service)
	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none at the limit", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", workflow.LabelAwaitingOwnerDecision}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/awaiting-owner-decision", got)
	}
	comments := sc.fake.Comments(sc.repo, 10)
	if len(comments) != 1 {
		t.Fatalf("%d comments, want 1", len(comments))
	}
	for _, want := range []string{"Row: I4", "(ci)", "after 3 check fix requests", "Pull request: #21"} {
		if !strings.Contains(comments[0].Body, want) {
			t.Errorf("the comment has no %q:\n%s", want, comments[0].Body)
		}
	}
	if n := len(sc.webhook.messagesSent()); n != 1 {
		t.Errorf("%d notifications, want 1", n)
	}

	// The Owner answers and adds cumin/status/ready.
	if err := sc.fake.SetLabels(sc.repo, 10, []string{workflow.LabelReady, "risk/low"}); err != nil {
		t.Fatal(err)
	}
	sc.pollAndWait(t, service)
	if n := sc.agentRuns(t); n != 1 {
		t.Fatalf("%d agent runs after cumin/status/ready, want 1", n)
	}
	if args := sc.record(t, "agent.args"); strings.Contains(args, "--resume") {
		t.Error("the request after cumin/status/ready resumed a session; it must start a new one")
	}
	if got := service.State.Issue("example-org/example-repo", 10).CheckFixRequests; got != 0 {
		t.Errorf("check fix requests after cumin/status/ready = %d, want 0", got)
	}
}

// I4: a label that cannot change starts no request, and the count goes
// back, so that failed writes never use up the limit.
func TestI4_AFailedLabelChangeKeepsTheCount(t *testing.T) {
	sc := newScene(t)
	service := sc.service()
	sc.failingCheck(t, service, 1)
	sc.fake.FailNext(http.MethodPut, putLabelsPath, http.StatusInternalServerError)

	if err := service.Poll(context.Background()); err == nil {
		t.Error("the poll hid the failed label change")
	}
	service.Wait()

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none", n)
	}
	if got := service.State.Issue("example-org/example-repo", 10).CheckFixRequests; got != 1 {
		t.Errorf("check fix requests = %d, want 1 (set back)", got)
	}
}

// I4: at the limit, the label changes first. When it cannot change, cumin
// posts no comment and sends no notification, so that the polls that
// follow do not repeat them.
func TestI4_AStopWhoseLabelFailsWritesNothingElse(t *testing.T) {
	sc := newScene(t)
	service := sc.service()
	sc.failingCheck(t, service, 3)
	sc.fake.FailNext(http.MethodPut, putLabelsPath, http.StatusInternalServerError)

	if err := service.Poll(context.Background()); err == nil {
		t.Error("the poll hid the failed label change")
	}
	if n := len(sc.fake.Comments(sc.repo, 10)); n != 0 {
		t.Errorf("%d comments, want none", n)
	}
	if n := len(sc.webhook.messagesSent()); n != 0 {
		t.Errorf("%d notifications, want none", n)
	}

	// The next poll stops the issue once.
	sc.pollAndWait(t, service)
	sc.pollAndWait(t, service)
	if n := len(sc.fake.Comments(sc.repo, 10)); n != 1 {
		t.Errorf("%d comments after the label changed, want 1", n)
	}
	if n := len(sc.webhook.messagesSent()); n != 1 {
		t.Errorf("%d notifications after the label changed, want 1", n)
	}
}

// I4: a worktree of the issue on another branch than the pull request (a
// renamed branch, a newer pull request) is made again on the branch of the
// pull request, because it holds nothing that GitHub lacks.
func TestI4_AWorktreeOnAnotherBranchIsMadeAgainOnThePullRequest(t *testing.T) {
	sc := newScene(t)
	service := sc.service()
	sc.failingCheck(t, service, 0)
	earlier, err := service.Workspace.Prepare(context.Background(), sc.remote, agent.Checkout{
		Owner: "example-org", Repo: "example-repo", Issue: 10, Role: config.RoleImplementer, Branch: "cumin/10-an-older-title",
	})
	if err != nil {
		t.Fatal(err)
	}

	sc.pollAndWait(t, service)

	if got := branchOf(t, earlier); got != wantBranch {
		t.Errorf("branch of the worktree = %q, want the branch of the pull request %q", got, wantBranch)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", workflow.LabelAwaitingChecks}) {
		t.Errorf("labels of #10 = %v, want I2 to pass on the pull request", got)
	}
}

// I1 (issue-states.md): a ready sub-issue with cumin/type/owner-task is never
// claimed, across two polls, while a ready sub-issue without it is.
func TestI1_AnOwnerTaskIsNeverClaimed(t *testing.T) {
	sc := newScene(t)
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 9, Parent: 6, Title: "Change a workflow", Labels: []string{"cumin/type/owner-task", "cumin/status/ready", "risk/high"}})
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	service := sc.service()

	sc.pollAndWait(t, service)
	sc.pollAndWait(t, service)

	if got := sc.fake.Issue(sc.repo, 9).Labels; !slices.Equal(got, []string{"cumin/type/owner-task", "cumin/status/ready", "risk/high"}) {
		t.Errorf("labels of #9 = %v, want them unchanged", got)
	}
	if n := sc.fake.CountRequests(http.MethodPut, "/repos/example-org/example-repo/issues/9/labels"); n != 0 {
		t.Errorf("%d label changes of #9, want none", n)
	}
	// #10 was claimed and ran; #9 started nothing. The second poll also
	// starts the Reviewer of #10 (I3), so the runs are counted by work
	// directory: #9 has none.
	for _, role := range []string{"implementer", "reviewer", "planner"} {
		if _, err := os.Stat(filepath.Join(sc.workRoot, "example-org", "example-repo", "9-"+role)); err == nil {
			t.Errorf("#9 has a %s worktree, want no run for it", role)
		}
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; slices.Contains(got, "cumin/status/ready") {
		t.Errorf("labels of #10 = %v, want it claimed", got)
	}
}
