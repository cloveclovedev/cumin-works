package workflow_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
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
	"syscall"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/agent"
	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/notify"
	"github.com/cloveclovedev/cumin-works/internal/platform/discord"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
	"github.com/cloveclovedev/cumin-works/internal/workflow"
)

const (
	putLabelsPath = "/repos/example-org/example-repo/issues/10/labels"
	// subIssueTitle gives the branch cumin/10-add-the-login-screen
	// (the slug rule of BranchName).
	subIssueTitle   = "Add the login screen"
	wantBranch      = "cumin/10-add-the-login-screen"
	implementerSlug = "example-implementer"
	// hangGuard is how long a test waits on a signal of a fake. It is only a
	// guard against a hang: no test passes or fails by how long a step took.
	hangGuard = time.Minute
	// everyTry is how many times the client sends a read that fails with a
	// temporary failure: the first try and 3 retries.
	everyTry = 4
)

// noWait is the wait between two tries of the client, without a sleep.
func noWait(context.Context, time.Duration) error { return nil }

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
	// serverURL is the address of the fake GitHub, for a test that builds
	// its own client.
	serverURL string
	repo      *githubtest.Repository
	logs      *logBuffer
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
	// clock is the time of the quota decisions ("stop agent starts") and of the fake GitHub.
	clock *testClock
	// quota are the Host settings of the quota limits: the defaults of the
	// settings table.
	quota config.QuotaSettings
}

// sceneZone is the time zone of the time bands in these tests. Its offset
// is not a full hour.
var sceneZone = time.FixedZone("UTC+05:30", 5*3600+30*60)

// sceneNow is the default time of the scene, for the quota decisions and for
// the fake GitHub: one hour before the weekly reset of the fixtures of
// internal/agent, so that the pace limit is the target and their usage stops
// nothing. Their 5h window reset earlier, so it stops nothing either. Every
// fixture time of a test derives from it.
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

// failQuotaOnce makes the next minimal run print no rate_limit_event. The
// minimal runs after it read the usage again.
func (sc *scene) failQuotaOnce(t *testing.T) {
	t.Helper()
	data, err := os.ReadFile(fixturePath(t, "no-quota.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sc.cliDir, "quota-once.jsonl"), data, 0o644); err != nil {
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
	// A notification names no person: it goes to a channel, not to one
	// reader. Every notification of every test is checked here.
	t.Cleanup(func() {
		for _, message := range f.messagesSent() {
			for _, person := range []string{"Owner", "Maintainer", "Operator"} {
				if strings.Contains(message, person) {
					t.Errorf("the notification names a person (%s):\n%s", person, message)
				}
			}
		}
	})
	return f
}

// messagesSent returns the messages that the webhook received.
func (f *fakeWebhook) messagesSent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.messages)
}

// messagesExceptWaiting returns the messages other than "tell that cumin
// waits" (waiting). A test of
// another row that ends with nothing to do also gets one "tell that cumin waits" notification.
func (sc *scene) messagesExceptWaiting() []string {
	var messages []string
	for _, m := range sc.webhook.messagesSent() {
		if !strings.HasPrefix(m, "cumin: tell that cumin waits: ") {
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
	// cumin-core changes the labels, so its events carry its login.
	fake.SetLabelWriter(cuminSlug)
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
	// The fake GitHub stamps its comments, reviews, and label events from
	// the clock of the scene.
	clock := &testClock{now: sceneNow}
	fake.SetClock(clock.Now)
	client := github.NewAppClient(server.URL, server.Client())
	client.SetRetryWait(noWait)
	return &scene{
		fake: fake, client: client, serverURL: server.URL, repo: repo,
		logs: newLogBuffer(), remote: remote, remoteHead: head, cliDir: cliDir,
		workRoot: t.TempDir(), cliPath: cliPath, settingsDir: t.TempDir(),
		webhook: webhook, notifier: webhook.notifier(), notifications: true,
		clock: clock,
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
	// stop cumin while a request is going on. The run first writes one line
	// to the named pipe "started", and waits there until a test reads it
	// (waitForAgentRun) or a signal ends the run.
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
	// movesHeadOnRun makes that agent run (1 is the first) push a new
	// commit and move the head of the pull request #21 to it, as a push
	// during the review or a resolved conflict. 0 means no run.
	movesHeadOnRun int
	// comments are what each agent run writes on the pull request #21, one
	// entry for each run in order: DECISION writes a decision request, NONE
	// writes nothing, as the Reviewer does for "request the cause".
	comments []string
	// holds makes the agent run wait until the test releases it
	// (scene.release), and then end as the fixture says: a run that is
	// going on while the test does something else. The run first writes
	// one line to the named pipe "started", as a run that sleeps does, and
	// then reads the named pipe "release".
	holds bool
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
		// the same credentials as the Implementer. "request the split" does not read the
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
		// The read time of a usage comes from the clock of the scene, as
		// the workflow decides with that clock.
		Now: sc.clock.Now,
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
		Location:    sceneZone,
		// The merge step waits for GitHub to close the issue; the fake
		// answers at once.
		MergeWait: time.Millisecond,
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
		ChecksWaitTime:      time.Hour,
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

// release lets the agent run that holds (cliOptions.holds) end: the run
// reads the named pipe "release", and the write returns when it did.
func (sc *scene) release(t *testing.T) {
	t.Helper()
	released := make(chan error, 1)
	go func() { released <- os.WriteFile(filepath.Join(sc.cliDir, "release"), []byte("go\n"), 0o600) }()
	select {
	case err := <-released:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(hangGuard):
		t.Fatal("the agent run did not take the release")
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
		started := filepath.Join(dir, "started")
		if err := syscall.Mkfifo(started, 0o600); err != nil {
			t.Fatal(err)
		}
		sleep = "if [ $n = agent ]; then\n" + trap + "echo started > " + started + "\nsleep 600\nfi\n"
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
	if o.movesHeadOnRun > 0 {
		review += "if [ $n = agent ] && [ \"$(grep -c '^agent$' " + filepath.Join(dir, "order") + ")\" = " + fmt.Sprint(o.movesHeadOnRun) + " ]; then\n" +
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
	hold := ""
	if o.holds {
		started, release := filepath.Join(dir, "started"), filepath.Join(dir, "release")
		for _, pipe := range []string{started, release} {
			if err := syscall.Mkfifo(pipe, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		hold = "if [ $n = agent ]; then\necho started > " + started + "\ncat " + release + " > /dev/null\nfi\n"
	}
	script := "#!/bin/sh\n" +
		"n=agent; f=" + agentFixture + "\n" +
		"for a in \"$@\"; do [ \"$a\" = --system-prompt ] && { n=quota; f=" + quota + "; }; done\n" +
		"[ $n = quota ] && [ -f " + filepath.Join(dir, "quota-override.jsonl") + " ] && f=" + filepath.Join(dir, "quota-override.jsonl") + "\n" +
		// quota-once.jsonl is the answer of the next minimal run only.
		"[ $n = quota ] && [ -f " + filepath.Join(dir, "quota-once.jsonl") + " ] && { f=" + filepath.Join(dir, "quota-once-used.jsonl") + "; mv " + filepath.Join(dir, "quota-once.jsonl") + " $f; }\n" +
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
		hold +
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

// requireIssueOfTheRun fails the test unless the prompt starts with the
// facts of the run and names the issue with its kind
// (docs/ja/requirements/agents/common.md, the facts of the start request).
func requireIssueOfTheRun(t *testing.T, prompt string, number int, kind string) {
	t.Helper()
	want := fmt.Sprintf("Facts of this run (data from cumin):\n- Issue of the run: #%d (%s)\n", number, kind)
	if !strings.HasPrefix(prompt, want) {
		t.Errorf("the prompt does not start with %q:\n%s", want, prompt)
	}
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

// logBuffer holds the log of the scene. The service writes while the test
// reads, so a lock guards the text, and a channel tells each write.
type logBuffer struct {
	mu      sync.Mutex
	text    bytes.Buffer
	written chan struct{}
}

func newLogBuffer() *logBuffer { return &logBuffer{written: make(chan struct{})} }

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	close(b.written)
	b.written = make(chan struct{})
	return b.text.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.text.String()
}

// waitForLog waits until the log of the scene holds the text: an event of
// cumin that leaves no request on the fake GitHub. The guard is only there
// against a hang.
func waitForLog(t *testing.T, sc *scene, text string) {
	t.Helper()
	timeout := time.After(hangGuard)
	for {
		sc.logs.mu.Lock()
		written := sc.logs.written
		found := strings.Contains(sc.logs.text.String(), text)
		sc.logs.mu.Unlock()
		if found {
			return
		}
		select {
		case <-written:
		case <-timeout:
			t.Fatalf("the log has no %s:\n%s", text, sc.logs.String())
		}
	}
}

// waitForAgentRun waits until the fake CLI of the agent has started: the
// sleeping run writes to the named pipe "started" after it set its trap,
// and the read returns when it did.
func waitForAgentRun(t *testing.T, sc *scene) {
	t.Helper()
	started := make(chan error, 1)
	go func() {
		_, err := os.ReadFile(filepath.Join(sc.cliDir, "started"))
		started <- err
	}()
	select {
	case err := <-started:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(hangGuard):
		t.Fatalf("the agent run did not start:\n%s", sc.logs.String())
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
