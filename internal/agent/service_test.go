package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
)

// The tests cover the start of one request (#69) with a fake GitHub and
// a fake CLI that serves both the quota run and the agent run.

const (
	serviceSlug  = "example-implementer"
	serviceBotID = 424242
	// serviceInstallation is the installation of the App on the first
	// repository of the fake, which numbers them from 1.
	serviceTokenPath = "/app/installations/1/access_tokens"
)

// serviceToken is the installation token that the fake GitHub creates.
const serviceToken = githubtest.Token

var serviceKey = sync.OnceValue(func() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return key
})

// newFakeGitHub starts the fake GitHub of the acceptance tests with the
// App of the Implementer and one repository, and returns the real client
// pointed at it.
func newFakeGitHub(t *testing.T) (*githubtest.Fake, *github.AppClient) {
	t.Helper()
	fake, server := githubtest.New(t)
	fake.AddRepository("example-org", "example-repo")
	fake.AddApp(githubtest.App{Slug: serviceSlug, Owner: "example-org", BotID: serviceBotID})
	return fake, github.NewAppClient(server.URL, server.Client())
}

// serviceCLI is a fake CLI for both runs of a start. The quota run is
// told by --system-prompt. Each run records its arguments and its
// environment under dir, named "quota" or "agent", and appends its name
// to the file "order".
func serviceCLI(t *testing.T, quotaFixture, agentFixture string) (path, dir string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "fake-claude")
	quota, err := filepath.Abs(filepath.Join("testdata", quotaFixture))
	if err != nil {
		t.Fatal(err)
	}
	agent, err := filepath.Abs(filepath.Join("testdata", agentFixture))
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"n=agent; f=" + agent + "\n" +
		"for a in \"$@\"; do [ \"$a\" = --system-prompt ] && { n=quota; f=" + quota + "; }; done\n" +
		"echo $n >> " + filepath.Join(dir, "order") + "\n" +
		"for a in \"$@\"; do printf '%s\\0' \"$a\"; done > " + filepath.Join(dir, "$n.args") + "\n" +
		"env > " + filepath.Join(dir, "$n.env") + "\n" +
		"cat $f\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, dir
}

func newService(t *testing.T, cliPath string, client *github.AppClient, logs *bytes.Buffer) *Service {
	t.Helper()
	level := slog.LevelInfo
	if logs == nil {
		logs = &bytes.Buffer{}
	}
	return &Service{
		Roles: map[config.Role]config.RoleSettings{
			config.RoleImplementer: {TimeLimit: time.Minute, CLI: config.CLIClaudeCode, CLIPath: cliPath, Model: "example-model"},
		},
		Apps: map[string]map[config.Role]github.AppCredentials{
			"example-org": {config.RoleImplementer: {ClientID: "Iv23liEXAMPLE", PrivateKey: serviceKey()}},
		},
		GitHub: client,
		Logger: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: level})),
	}
}

func startRequest(t *testing.T) StartRequest {
	t.Helper()
	return StartRequest{
		Owner: "example-org", Repo: "example-repo", Role: config.RoleImplementer,
		Text: "Implement issue 12.", WorkDir: t.TempDir(),
	}
}

func TestStart_TokenThenIdentityThenRunWithoutAQuotaRun(t *testing.T) {
	fake, client := newFakeGitHub(t)
	path, dir := serviceCLI(t, "quota-run.jsonl", "done.jsonl")
	var logs bytes.Buffer
	s := newService(t, path, client, &logs)

	run, err := s.Start(context.Background(), startRequest(t))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if run.Result.Result != ResultDone || run.SessionID != fixtureSessionID {
		t.Errorf("run = %+v", run)
	}
	// The login of the bot goes with the result, for the check of "wait for the checks".
	if run.BotLogin != serviceSlug+"[bot]" {
		t.Errorf("run.BotLogin = %q, want %s[bot]", run.BotLogin, serviceSlug)
	}

	order, err := os.ReadFile(filepath.Join(dir, "order"))
	if err != nil {
		t.Fatal(err)
	}
	// Start reads no usage; the caller reads it in its one check before
	// every start of an agent (designs/quota.md).
	if got := strings.TrimSpace(string(order)); got != "agent" {
		t.Errorf("order of the runs = %q, want only the agent run", got)
	}
	env := recordedEnv(t, filepath.Join(dir, "agent"))
	if env["GH_TOKEN"] != serviceToken {
		t.Errorf("GH_TOKEN = %q, want the token of the fake GitHub", env["GH_TOKEN"])
	}
	basic := strings.TrimPrefix(env["GIT_CONFIG_VALUE_0"], "Authorization: Basic ")
	if decoded, err := base64.StdEncoding.DecodeString(basic); err != nil || string(decoded) != "x-access-token:"+serviceToken {
		t.Errorf("GIT_CONFIG_VALUE_0 = %q, want the token", env["GIT_CONFIG_VALUE_0"])
	}
	wantEmail := fmt.Sprintf("%d+%s[bot]@users.noreply.github.com", serviceBotID, serviceSlug)
	if env["GIT_AUTHOR_NAME"] != serviceSlug+"[bot]" || env["GIT_AUTHOR_EMAIL"] != wantEmail || env["GIT_COMMITTER_EMAIL"] != wantEmail {
		t.Errorf("identity = %q <%q> / <%q>, want %s[bot] <%s>", env["GIT_AUTHOR_NAME"], env["GIT_AUTHOR_EMAIL"], env["GIT_COMMITTER_EMAIL"], serviceSlug, wantEmail)
	}
	args := recordedArgs(t, filepath.Join(dir, "agent.args"))
	if i := indexOf(args, "--model"); i < 0 || args[i+1] != "example-model" {
		t.Errorf("the agent run has no --model example-model: %q", args)
	}

	// The token is limited to the repository and to the permissions of
	// the App of the role.
	requests := fake.TokenRequests()
	if len(requests) != 1 {
		t.Fatalf("%d token requests, want 1", len(requests))
	}
	if repos := requests[0].Repositories; len(repos) != 1 || repos[0] != "example-repo" {
		t.Errorf("token repositories = %v", repos)
	}
	if perms := requests[0].Permissions; perms["contents"] != "write" || perms["issues"] != "read" {
		t.Errorf("token permissions = %v, want the Implementer table", perms)
	}
	if strings.Contains(logs.String(), serviceToken) {
		t.Errorf("the info logs hold the token:\n%s", logs.String())
	}
	for _, want := range []string{"agent token created", "agent identity read", "agent end"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("info logs lack %q:\n%s", want, logs.String())
		}
	}
}

func indexOf(list []string, s string) int {
	for i, item := range list {
		if item == s {
			return i
		}
	}
	return -1
}

// Every line of a start names the role, and none names it twice.
func TestStart_EveryLogLineNamesTheRoleOnce(t *testing.T) {
	_, client := newFakeGitHub(t)
	path, _ := serviceCLI(t, "quota-run.jsonl", "done.jsonl")
	var logs bytes.Buffer
	s := newService(t, path, client, &logs)

	if _, err := s.Start(context.Background(), startRequest(t)); err != nil {
		t.Fatalf("Start: %v", err)
	}

	want := "role=" + string(config.RoleImplementer)
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if line == "" {
			continue
		}
		if n := strings.Count(line, "role="); n != 1 {
			t.Errorf("the line has %d role fields, want 1:\n%s", n, line)
		}
		if !strings.Contains(line, want) {
			t.Errorf("the line does not name the role:\n%s", line)
		}
		for _, msg := range []string{"agent start", "agent end"} {
			if strings.Contains(line, `msg="`+msg+`"`) || strings.Contains(line, "msg="+msg) {
				seen[msg] = true
			}
		}
	}
	for _, msg := range []string{"agent start", "agent end"} {
		if !seen[msg] {
			t.Errorf("the logs have no line %q:\n%s", msg, logs.String())
		}
	}
}

func TestStart_IdentityIsReadOnceAndTokenEveryTime(t *testing.T) {
	fake, client := newFakeGitHub(t)
	path, _ := serviceCLI(t, "quota-run.jsonl", "done.jsonl")
	s := newService(t, path, client, nil)
	for i := 0; i < 2; i++ {
		if _, err := s.Start(context.Background(), startRequest(t)); err != nil {
			t.Fatalf("Start %d: %v", i+1, err)
		}
	}
	if n := fake.CountRequests(http.MethodGet, "/app"); n != 1 {
		t.Errorf("GET /app = %d, want 1", n)
	}
	if n := fake.CountRequests(http.MethodGet, "/users/"+serviceSlug+"[bot]"); n != 1 {
		t.Errorf("GET /users = %d, want 1", n)
	}
	if n := fake.CountRequests(http.MethodPost, serviceTokenPath); n != 2 {
		t.Errorf("token requests = %d, want 2", n)
	}
}

// ReadQuota runs the minimal run with the CLI of the role, without a token,
// and names the role in its log lines.
func TestReadQuota_RunsTheMinimalRunWithoutAToken(t *testing.T) {
	fake, client := newFakeGitHub(t)
	path, dir := serviceCLI(t, "quota-run.jsonl", "done.jsonl")
	var logs bytes.Buffer
	s := newService(t, path, client, &logs)

	usage, err := s.ReadQuota(context.Background(), config.RoleImplementer)
	if err != nil {
		t.Fatalf("ReadQuota: %v", err)
	}
	if usage.FiveHour.ResetsAt.IsZero() || usage.Weekly.ResetsAt.IsZero() {
		t.Errorf("usage = %+v, want both windows", usage)
	}
	order, err := os.ReadFile(filepath.Join(dir, "order"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(order)); got != "quota" {
		t.Errorf("order of the runs = %q, want only the quota run", got)
	}
	if _, ok := recordedEnv(t, filepath.Join(dir, "quota"))["GH_TOKEN"]; ok {
		t.Error("the quota run received a token")
	}
	if n := fake.CountRequests(http.MethodPost, serviceTokenPath); n != 0 {
		t.Errorf("token requests = %d, want 0", n)
	}
	if !strings.Contains(logs.String(), "quota usage read") || !strings.Contains(logs.String(), "role="+string(config.RoleImplementer)) {
		t.Errorf("the logs lack the read or the role:\n%s", logs.String())
	}
}

func TestReadQuota_UnreadableUsageIsQuotaNotRead(t *testing.T) {
	_, client := newFakeGitHub(t)
	path, _ := serviceCLI(t, "no-quota.jsonl", "done.jsonl")
	s := newService(t, path, client, nil)

	_, err := s.ReadQuota(context.Background(), config.RoleImplementer)
	var q *QuotaNotRead
	if !errors.As(err, &q) {
		t.Fatalf("err = %v, want *QuotaNotRead", err)
	}
}

func TestReadQuota_UnknownRoleIsQuotaNotRead(t *testing.T) {
	_, client := newFakeGitHub(t)
	path, _ := serviceCLI(t, "quota-run.jsonl", "done.jsonl")
	s := newService(t, path, client, nil)

	_, err := s.ReadQuota(context.Background(), config.Role("unknown"))
	var q *QuotaNotRead
	if !errors.As(err, &q) {
		t.Fatalf("err = %v, want *QuotaNotRead", err)
	}
}

func TestStart_TokenErrorStopsBeforeTheRun(t *testing.T) {
	fake, client := newFakeGitHub(t)
	fake.FailNext(http.MethodPost, serviceTokenPath, http.StatusUnprocessableEntity)
	path, dir := serviceCLI(t, "quota-run.jsonl", "done.jsonl")
	s := newService(t, path, client, nil)

	_, err := s.Start(context.Background(), startRequest(t))
	if err == nil || !strings.Contains(err.Error(), "422") {
		t.Fatalf("err = %v, want the 422 of the token request", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "agent.args")); statErr == nil {
		t.Error("the agent run started")
	}
}

func TestStart_UnknownRoleIsRefused(t *testing.T) {
	_, client := newFakeGitHub(t)
	path, _ := serviceCLI(t, "quota-run.jsonl", "done.jsonl")
	s := newService(t, path, client, nil)
	req := startRequest(t)
	req.Role = config.RoleReviewer
	if _, err := s.Start(context.Background(), req); err == nil || !strings.Contains(err.Error(), "no settings for the role") {
		t.Errorf("err = %v", err)
	}
}

// The App of a role belongs to one owner. A repository of another owner
// needs its own App, and its identity is cached apart.
func TestStart_AppsAreKeyedByMaintainer(t *testing.T) {
	fake, client := newFakeGitHub(t)
	path, _ := serviceCLI(t, "quota-run.jsonl", "done.jsonl")
	s := newService(t, path, client, nil)
	req := startRequest(t)
	req.Owner = "other-org"
	_, err := s.Start(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "github_apps.other-org.implementer") {
		t.Errorf("err = %v, want the missing setting named", err)
	}
	if n := fake.CountRequests(http.MethodGet, "/repos/other-org/example-repo/installation"); n != 0 {
		t.Errorf("GitHub was called for the other owner %d times", n)
	}
	if _, err := s.Start(context.Background(), startRequest(t)); err != nil {
		t.Fatalf("Start for the configured owner: %v", err)
	}
	if _, ok := s.identities[appKey{"example-org", config.RoleImplementer}]; !ok {
		t.Errorf("identities = %v, want the key of the owner and the role", s.identities)
	}
}

// GitHub account names are case-insensitive: the settings key matches the
// owner of the request without regard to case, and two keys that differ
// only by case are refused.
func TestStart_MaintainerMatchesTheSettingsWithoutRegardToCase(t *testing.T) {
	fake, client := newFakeGitHub(t)
	path, _ := serviceCLI(t, "quota-run.jsonl", "done.jsonl")
	s := newService(t, path, client, nil)
	s.Apps = map[string]map[config.Role]github.AppCredentials{
		"Example-Org": s.Apps["example-org"],
	}
	if _, err := s.Start(context.Background(), startRequest(t)); err != nil {
		t.Fatalf("Start with the owner in another case: %v", err)
	}
	if n := fake.CountRequests(http.MethodPost, serviceTokenPath); n != 1 {
		t.Errorf("token requests = %d, want 1", n)
	}
	s.Apps["example-org"] = s.Apps["Example-Org"]
	if _, err := s.Start(context.Background(), startRequest(t)); err == nil || !strings.Contains(err.Error(), "different cases") {
		t.Errorf("err = %v, want the two keys refused", err)
	}
}

func TestHostWarnings_EmptyForClaudeCode(t *testing.T) {
	s := &Service{Roles: map[config.Role]config.RoleSettings{
		config.RolePlanner:     {CLI: config.CLIClaudeCode},
		config.RoleImplementer: {CLI: config.CLIClaudeCode},
		config.RoleReviewer:    {CLI: config.CLIClaudeCode},
	}}
	if warnings := s.HostWarnings(); len(warnings) != 0 {
		t.Errorf("HostWarnings = %q, want none", warnings)
	}
}

// The settings of a request replace the settings of the role. The poll
// passes the settings of the target repository, whose .cumin/config.toml
// may set the CLI and the model of a role.
func TestStart_TheSettingsOfTheRequestReplaceTheOnesOfTheRole(t *testing.T) {
	_, client := newFakeGitHub(t)
	path, dir := serviceCLI(t, "quota-run.jsonl", "done.jsonl")
	s := newService(t, path, client, nil)

	settings := s.Roles[config.RoleImplementer]
	settings.Model = "model-of-the-repository"
	request := startRequest(t)
	request.Settings = &settings

	if _, err := s.Start(context.Background(), request); err != nil {
		t.Fatalf("Start: %v", err)
	}
	args := recordedArgs(t, filepath.Join(dir, "agent.args"))
	if i := indexOf(args, "--model"); i < 0 || args[i+1] != "model-of-the-repository" {
		t.Errorf("the agent run does not use the model of the request: %q", args)
	}
	// The settings of the Service are not changed by a request.
	if s.Roles[config.RoleImplementer].Model != "example-model" {
		t.Errorf("the settings of the role changed: %+v", s.Roles[config.RoleImplementer])
	}
}

// A start passes the skills directory of its own role, so that a role is
// offered only the skills of its own work.
func TestStart_PassesTheSkillsDirectoryOfTheRole(t *testing.T) {
	_, client := newFakeGitHub(t)
	path, dir := serviceCLI(t, "quota-run.jsonl", "done.jsonl")
	s := newService(t, path, client, nil)
	root := t.TempDir()
	if err := WriteSkills(root); err != nil {
		t.Fatal(err)
	}
	s.SkillsDir = root

	if _, err := s.Start(context.Background(), startRequest(t)); err != nil {
		t.Fatalf("Start: %v", err)
	}

	args := recordedArgs(t, filepath.Join(dir, "agent.args"))
	i := slices.Index(args, "--add-dir")
	want := SkillDir(root, config.RoleImplementer)
	if i < 0 || i+1 >= len(args) || args[i+1] != want {
		t.Errorf("args have no --add-dir %s: %q", want, args)
	}
	// The directory of another role is not the one that was passed.
	if other := SkillDir(root, config.RolePlanner); slices.Contains(args, other) {
		t.Errorf("the arguments name the directory of another role: %q", args)
	}
}

// promptOfRun returns the request text that the fake CLI received: the
// value of -p.
func promptOfRun(t *testing.T, dir string) string {
	t.Helper()
	args := recordedArgs(t, filepath.Join(dir, "agent.args"))
	i := indexOf(args, "-p")
	if i < 0 || i+1 >= len(args) {
		t.Fatalf("the agent run has no -p: %q", args)
	}
	return args[i+1]
}

// fixedClock returns a clock that always gives the same time.
func fixedClock(now time.Time) func() time.Time {
	return func() time.Time { return now }
}

// The start request of every role names the time limit of that role and
// the end time of the run, before the request text
// (docs/ja/requirements/agents/common.md, the test of the time limit).
func TestStart_ThePromptOfEveryRoleNamesTheTimeLimitAndTheEndTime(t *testing.T) {
	tests := []struct {
		role    config.Role
		fixture string
		limit   time.Duration
		want    string
	}{
		{config.RolePlanner, "planner-done.jsonl", 20 * time.Minute,
			"- Time limit of the run: 20m\n- End time of the run: 2026-10-03T01:20:00Z\n" +
				"- Time limit of the Implementer: 1m\n- Time limit of the Reviewer: 0s\n"},
		{config.RoleImplementer, "done.jsonl", 50 * time.Minute,
			"- Time limit of the run: 50m\n- End time of the run: 2026-10-03T01:50:00Z\n"},
		{config.RoleReviewer, "done.jsonl", 30 * time.Minute,
			"- Time limit of the run: 30m\n- End time of the run: 2026-10-03T01:30:00Z\n"},
	}
	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			_, client := newFakeGitHub(t)
			path, dir := serviceCLI(t, "quota-run.jsonl", tt.fixture)
			s := newService(t, path, client, nil)
			// The fake knows one App, so every role runs with its
			// credentials.
			s.Roles[tt.role] = config.RoleSettings{TimeLimit: tt.limit, CLI: config.CLIClaudeCode, CLIPath: path}
			s.Apps["example-org"][tt.role] = s.Apps["example-org"][config.RoleImplementer]
			s.Now = fixedClock(time.Date(2026, 10, 3, 1, 0, 0, 0, time.UTC))
			request := startRequest(t)
			request.Role = tt.role

			if _, err := s.Start(context.Background(), request); err != nil {
				t.Fatalf("Start: %v", err)
			}
			want := "Facts of this run (data from cumin):\n" + tt.want + "\n" + request.Text
			if got := promptOfRun(t, dir); got != want {
				t.Errorf("prompt = %q, want %q", got, want)
			}
		})
	}
}

// plannerService returns a Service that holds the settings of the three
// roles, each with its own time limit. The fake knows one App, so the
// Planner runs with its credentials.
func plannerService(t *testing.T, path string, client *github.AppClient) *Service {
	t.Helper()
	s := newService(t, path, client, nil)
	s.Roles[config.RolePlanner] = config.RoleSettings{TimeLimit: 20 * time.Minute, CLI: config.CLIClaudeCode, CLIPath: path}
	s.Roles[config.RoleImplementer] = config.RoleSettings{TimeLimit: 50 * time.Minute, CLI: config.CLIClaudeCode, CLIPath: path}
	s.Roles[config.RoleReviewer] = config.RoleSettings{TimeLimit: 30 * time.Minute, CLI: config.CLIClaudeCode, CLIPath: path}
	s.Apps["example-org"][config.RolePlanner] = s.Apps["example-org"][config.RoleImplementer]
	s.Apps["example-org"][config.RoleReviewer] = s.Apps["example-org"][config.RoleImplementer]
	return s
}

// otherLimitLines are the lines of the time limits of the Implementer and
// the Reviewer, with the values of plannerService.
const otherLimitLines = "- Time limit of the Implementer: 50m\n- Time limit of the Reviewer: 30m\n"

// The start request of the Planner names the time limits of the
// Implementer and of the Reviewer, after the end time, so that the Planner
// sizes each implementation issue for one run of each. The start requests
// of the Implementer and of the Reviewer do not hold these lines
// (docs/ja/requirements/agents/common.md, the test of the time limits that
// the Planner receives).
func TestStart_ThePromptOfThePlannerAloneNamesTheTimeLimitsOfTheImplementerAndTheReviewer(t *testing.T) {
	tests := []struct {
		role    config.Role
		fixture string
		want    string
	}{
		{config.RolePlanner, "planner-done.jsonl",
			"- Time limit of the run: 20m\n- End time of the run: 2026-10-03T01:20:00Z\n" + otherLimitLines},
		{config.RoleImplementer, "done.jsonl",
			"- Time limit of the run: 50m\n- End time of the run: 2026-10-03T01:50:00Z\n"},
		{config.RoleReviewer, "done.jsonl",
			"- Time limit of the run: 30m\n- End time of the run: 2026-10-03T01:30:00Z\n"},
	}
	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			_, client := newFakeGitHub(t)
			path, dir := serviceCLI(t, "quota-run.jsonl", tt.fixture)
			s := plannerService(t, path, client)
			s.Now = fixedClock(time.Date(2026, 10, 3, 1, 0, 0, 0, time.UTC))
			request := startRequest(t)
			request.Role = tt.role

			if _, err := s.Start(context.Background(), request); err != nil {
				t.Fatalf("Start: %v", err)
			}
			want := "Facts of this run (data from cumin):\n" + tt.want + "\n" + request.Text
			if got := promptOfRun(t, dir); got != want {
				t.Errorf("prompt = %q, want %q", got, want)
			}
		})
	}
}

// A resumed session of the Planner receives the time limits of the
// Implementer and of the Reviewer again.
func TestStart_AResumedSessionOfThePlannerReceivesTheTimeLimitsOfTheOtherRolesAgain(t *testing.T) {
	_, client := newFakeGitHub(t)
	path, dir := serviceCLI(t, "quota-run.jsonl", "planner-done.jsonl")
	s := plannerService(t, path, client)

	s.Now = fixedClock(time.Date(2026, 10, 3, 1, 0, 0, 0, time.UTC))
	request := startRequest(t)
	request.Role = config.RolePlanner
	first, err := s.Start(context.Background(), request)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	s.Now = fixedClock(time.Date(2026, 10, 3, 2, 30, 0, 0, time.UTC))
	request.SessionID = first.SessionID
	if _, err := s.Start(context.Background(), request); err != nil {
		t.Fatalf("Start of the resumed session: %v", err)
	}
	args := recordedArgs(t, filepath.Join(dir, "agent.args"))
	if i := indexOf(args, "--resume"); i < 0 || args[i+1] != first.SessionID {
		t.Fatalf("the second run does not resume the session: %q", args)
	}
	want := "Facts of this run (data from cumin):\n" +
		"- Time limit of the run: 20m\n- End time of the run: 2026-10-03T02:50:00Z\n" + otherLimitLines + "\n" + request.Text
	if got := promptOfRun(t, dir); got != want {
		t.Errorf("resumed prompt = %q, want %q", got, want)
	}
}

// With settings in the request, the time limit and the end time are the
// ones of those settings: they are the settings that cut the run.
func TestStart_TheTimeLimitOfThePromptIsTheOneOfTheSettingsOfTheRequest(t *testing.T) {
	_, client := newFakeGitHub(t)
	path, dir := serviceCLI(t, "quota-run.jsonl", "done.jsonl")
	s := newService(t, path, client, nil)
	s.Now = fixedClock(time.Date(2026, 10, 3, 1, 0, 0, 0, time.UTC))

	settings := s.Roles[config.RoleImplementer]
	settings.TimeLimit = 45 * time.Minute
	request := startRequest(t)
	request.Settings = &settings

	if _, err := s.Start(context.Background(), request); err != nil {
		t.Fatalf("Start: %v", err)
	}
	want := "- Time limit of the run: 45m\n- End time of the run: 2026-10-03T01:45:00Z\n"
	if got := promptOfRun(t, dir); !strings.Contains(got, want) {
		t.Errorf("prompt = %q, want the lines %q", got, want)
	}
}

// A resumed session receives the block again, with the issue of the run,
// and with the end time of the new run and not the one of the run that it
// continues.
func TestStart_AResumedSessionReceivesANewEndTime(t *testing.T) {
	_, client := newFakeGitHub(t)
	path, dir := serviceCLI(t, "quota-run.jsonl", "done.jsonl")
	s := newService(t, path, client, nil)

	s.Now = fixedClock(time.Date(2026, 10, 3, 1, 0, 0, 0, time.UTC))
	first, err := s.Start(context.Background(), startRequest(t))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := promptOfRun(t, dir); !strings.Contains(got, "- End time of the run: 2026-10-03T01:01:00Z\n") {
		t.Errorf("first prompt = %q, want the end time 01:01:00Z", got)
	}

	s.Now = fixedClock(time.Date(2026, 10, 3, 2, 30, 0, 0, time.UTC))
	request := startRequest(t)
	request.Facts = Facts{IssueNumber: 12, IssueKind: IssueKindImplementation, IssueOwnerLogin: "example-owner"}
	request.SessionID = first.SessionID
	if _, err := s.Start(context.Background(), request); err != nil {
		t.Fatalf("Start of the resumed session: %v", err)
	}
	args := recordedArgs(t, filepath.Join(dir, "agent.args"))
	if i := indexOf(args, "--resume"); i < 0 || args[i+1] != first.SessionID {
		t.Fatalf("the second run does not resume the session: %q", args)
	}
	want := "Facts of this run (data from cumin):\n- Issue of the run: #12 (implementation issue)\n" +
		"- Issue Owner login: example-owner\n" +
		"- Time limit of the run: 1m\n- End time of the run: 2026-10-03T02:31:00Z\n\n" + request.Text
	if got := promptOfRun(t, dir); got != want {
		t.Errorf("resumed prompt = %q, want %q", got, want)
	}
}
