package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
)

// The tests cover the start of one request (#69) with a fake GitHub and
// a fake CLI that serves both the quota run and the agent run.

const (
	serviceToken = "ghs_service_fake_token_0123456789"
	serviceSlug  = "example-implementer"
	serviceBotID = 424242
)

var serviceKey = sync.OnceValue(func() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return key
})

// fakeGitHub answers the four endpoints of a start and counts each.
type fakeGitHub struct {
	mu        sync.Mutex
	counts    map[string]int
	tokenBody map[string]any
	// tokenStatus is the status of the token endpoint. 0 means 201.
	tokenStatus int
}

func newFakeGitHub(t *testing.T) (*fakeGitHub, *github.AppClient) {
	t.Helper()
	f := &fakeGitHub{counts: map[string]int{}}
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(server.Close)
	return f, github.NewAppClient(server.URL, nil)
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := r.Method + " " + r.URL.Path
	f.counts[key]++
	write := func(status int, body any) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
	switch key {
	case "GET /repos/example-org/example-repo/installation":
		write(http.StatusOK, map[string]any{"id": 7})
	case "POST /app/installations/7/access_tokens":
		_ = json.NewDecoder(r.Body).Decode(&f.tokenBody)
		if f.tokenStatus != 0 {
			write(f.tokenStatus, map[string]any{"message": "Validation Failed"})
			return
		}
		write(http.StatusCreated, map[string]any{"token": serviceToken, "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
	case "GET /app":
		write(http.StatusOK, map[string]any{"slug": serviceSlug, "html_url": "https://github.com/apps/" + serviceSlug, "owner": map[string]any{"login": "example-org"}, "permissions": map[string]any{"contents": "write"}})
	case "GET /users/" + serviceSlug + "[bot]", "GET /users/" + serviceSlug + "%5Bbot%5D":
		write(http.StatusOK, map[string]any{"id": serviceBotID, "login": serviceSlug + "[bot]", "type": "Bot"})
	default:
		write(http.StatusNotFound, map[string]any{"message": "Not Found: " + key})
	}
}

func (f *fakeGitHub) count(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.counts[key]
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
		RoleInstruction: "# Implementer", Text: "Implement issue 12.", WorkDir: t.TempDir(),
	}
}

func TestStart_QuotaThenTokenThenIdentityThenRun(t *testing.T) {
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
	// The login of the bot goes with the result, for the check of I2.
	if run.BotLogin != serviceSlug+"[bot]" {
		t.Errorf("run.BotLogin = %q, want %s[bot]", run.BotLogin, serviceSlug)
	}

	order, err := os.ReadFile(filepath.Join(dir, "order"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(order)); got != "quota\nagent" {
		t.Errorf("order of the runs = %q, want the quota run first", got)
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
	quotaEnv := recordedEnv(t, filepath.Join(dir, "quota"))
	if _, ok := quotaEnv["GH_TOKEN"]; ok {
		t.Error("the quota run received the token")
	}
	args := recordedArgs(t, filepath.Join(dir, "agent.args"))
	if i := indexOf(args, "--model"); i < 0 || args[i+1] != "example-model" {
		t.Errorf("the agent run has no --model example-model: %q", args)
	}

	// The token is limited to the repository and to the permissions of
	// the App of the role.
	if repos, _ := fake.tokenBody["repositories"].([]any); len(repos) != 1 || repos[0] != "example-repo" {
		t.Errorf("token repositories = %v", fake.tokenBody["repositories"])
	}
	if perms, _ := fake.tokenBody["permissions"].(map[string]any); perms["contents"] != "write" || perms["issues"] != "read" {
		t.Errorf("token permissions = %v, want the Implementer table", fake.tokenBody["permissions"])
	}
	if strings.Contains(logs.String(), serviceToken) {
		t.Errorf("the info logs hold the token:\n%s", logs.String())
	}
	for _, want := range []string{"quota usage read", "agent token created", "agent identity read", "agent end"} {
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

// Every line of a start names the role, and none names it twice. The
// quota run logs through the same logger, so it is named too.
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
		for _, msg := range []string{"quota usage read", "agent start", "agent end"} {
			if strings.Contains(line, `msg="`+msg+`"`) || strings.Contains(line, "msg="+msg) {
				seen[msg] = true
			}
		}
	}
	for _, msg := range []string{"quota usage read", "agent start", "agent end"} {
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
	if n := fake.count("GET /app"); n != 1 {
		t.Errorf("GET /app = %d, want 1", n)
	}
	if n := fake.count("GET /users/"+serviceSlug+"[bot]") + fake.count("GET /users/"+serviceSlug+"%5Bbot%5D"); n != 1 {
		t.Errorf("GET /users = %d, want 1", n)
	}
	if n := fake.count("POST /app/installations/7/access_tokens"); n != 2 {
		t.Errorf("token requests = %d, want 2", n)
	}
}

func TestStart_UnreadableQuotaStopsBeforeTheToken(t *testing.T) {
	fake, client := newFakeGitHub(t)
	path, dir := serviceCLI(t, "no-quota.jsonl", "done.jsonl")
	s := newService(t, path, client, nil)

	_, err := s.Start(context.Background(), startRequest(t))
	var q *QuotaNotRead
	if !errors.As(err, &q) {
		t.Fatalf("err = %v, want *QuotaNotRead", err)
	}
	if n := fake.count("POST /app/installations/7/access_tokens"); n != 0 {
		t.Errorf("token requests = %d, want 0", n)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "agent.args")); statErr == nil {
		t.Error("the agent run started")
	}
}

func TestStart_TokenErrorStopsBeforeTheRun(t *testing.T) {
	fake, client := newFakeGitHub(t)
	fake.tokenStatus = http.StatusUnprocessableEntity
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
func TestStart_AppsAreKeyedByOwner(t *testing.T) {
	fake, client := newFakeGitHub(t)
	path, _ := serviceCLI(t, "quota-run.jsonl", "done.jsonl")
	s := newService(t, path, client, nil)
	req := startRequest(t)
	req.Owner = "other-org"
	_, err := s.Start(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "github_apps.other-org.implementer") {
		t.Errorf("err = %v, want the missing setting named", err)
	}
	if n := fake.count("GET /repos/other-org/example-repo/installation"); n != 0 {
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
func TestStart_OwnerMatchesTheSettingsWithoutRegardToCase(t *testing.T) {
	fake, client := newFakeGitHub(t)
	path, _ := serviceCLI(t, "quota-run.jsonl", "done.jsonl")
	s := newService(t, path, client, nil)
	s.Apps = map[string]map[config.Role]github.AppCredentials{
		"Example-Org": s.Apps["example-org"],
	}
	if _, err := s.Start(context.Background(), startRequest(t)); err != nil {
		t.Fatalf("Start with the owner in another case: %v", err)
	}
	if n := fake.count("POST /app/installations/7/access_tokens"); n != 1 {
		t.Errorf("token requests = %d, want 1", n)
	}
	s.Apps["example-org"] = s.Apps["Example-Org"]
	if _, err := s.Start(context.Background(), startRequest(t)); err == nil || !strings.Contains(err.Error(), "different cases") {
		t.Errorf("err = %v, want the two keys refused", err)
	}
}

func TestHostWarnings_EmptyForClaudeCode(t *testing.T) {
	s := &Service{Roles: map[config.Role]config.RoleSettings{
		config.RoleChiefEngineer: {CLI: config.CLIClaudeCode},
		config.RoleImplementer:   {CLI: config.CLIClaudeCode},
		config.RoleReviewer:      {CLI: config.CLIClaudeCode},
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
