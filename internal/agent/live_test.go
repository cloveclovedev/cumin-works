package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/keychain"
	"github.com/cloveclovedev/cumin-works/roles"
)

// TestLive_AgentRun is the live check of the last requirement of #7. It
// runs real Claude Code three times, without GitHub, in a throwaway
// repository. It runs only with CUMIN_LIVE=1, because it uses quota.
// docs/ja/development/agent-live-check.md says how to run it.
//
// The log of this test holds no quota number and no session ID.
func TestLive_AgentRun(t *testing.T) {
	if os.Getenv("CUMIN_LIVE") != "1" {
		t.Skip("set CUMIN_LIVE=1 to run the live check; it uses quota")
	}
	path := os.Getenv("CUMIN_CLAUDE_PATH")
	if path == "" {
		var err error
		if path, err = exec.LookPath("claude"); err != nil {
			t.Fatalf("claude is not on PATH: %v (set CUMIN_CLAUDE_PATH)", err)
		}
	}
	r := newRemote(t)
	w := Workspace{Root: filepath.Join(t.TempDir(), "work"), Logger: testLogger(t)}
	c := Checkout{Owner: "example-org", Repo: "example-repo", Issue: 1, Role: config.RoleImplementer, Branch: "cumin/1-live-check"}
	dir, err := w.Prepare(context.Background(), r.path, c)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	instruction, err := roles.Instruction(config.RoleImplementer)
	if err != nil {
		t.Fatal(err)
	}
	cli := ClaudeCode{Path: path, Logger: testLogger(t)}
	// The agent does not reach GitHub in this check, so the credentials
	// are placeholders.
	base := Request{Role: config.RoleImplementer, RoleInstruction: instruction, WorkDir: dir, TimeLimit: 5 * time.Minute, Credentials: testCredentials}

	// Run 1: a new session that reads a file and returns done.
	req := base
	req.Text = "Read README.md in the current directory. Do not change any file. " +
		"Then finish with the result done, and a summary of one sentence that names the file that you read."
	run1, err := cli.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run 1: %v", err)
	}
	if run1.Result.Result != ResultDone || run1.Result.Summary == "" {
		t.Errorf("run 1: result = %+v, want done with a summary", run1.Result)
	}
	if run1.SessionID == "" {
		t.Error("run 1: no session ID")
	}
	if !run1.QuotaRead {
		t.Error("run 1: no quota usage was read")
	} else if now := time.Now(); !run1.Quota.FiveHour.ResetsAt.After(now) || !run1.Quota.Weekly.ResetsAt.After(now) {
		t.Error("run 1: a reset time is not in the future")
	}
	t.Logf("run 1: result %s, session ID read: %v, quota read: %v, reset times in the future: %v",
		run1.Result.Result, run1.SessionID != "", run1.QuotaRead,
		run1.QuotaRead && run1.Quota.FiveHour.ResetsAt.After(time.Now()) && run1.Quota.Weekly.ResetsAt.After(time.Now()))

	// Run 2: the same session. The agent must remember run 1.
	req = base
	req.SessionID = run1.SessionID
	req.Text = "Which file did you read in your earlier turn? " +
		"Finish with the result done, and a summary of one sentence that names that file."
	run2, err := cli.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if run2.SessionID != run1.SessionID {
		t.Error("run 2: the session ID differs from run 1")
	}
	if run2.Result.Result != ResultDone || !strings.Contains(run2.Result.Summary, "README") {
		t.Errorf("run 2: result = %+v, want done with a summary that names README.md", run2.Result)
	}
	t.Logf("run 2: result %s, same session as run 1: %v, summary names README.md: %v",
		run2.Result.Result, run2.SessionID == run1.SessionID, strings.Contains(run2.Result.Summary, "README"))

	// Run 3: a run over the time limit. No process of its group may stay.
	// The command creates a marker file first, so that the test knows
	// that the child ran before the stop.
	req = base
	req.Text = "Run exactly this shell command with the Bash tool, and wait for it to finish: " +
		"`touch started && sleep 600`. Then finish with the result done."
	req.TimeLimit = 20 * time.Second
	start := time.Now()
	_, err = cli.Run(context.Background(), req)
	elapsed := time.Since(start)
	var end *AbnormalEnd
	if !errors.As(err, &end) || end.Kind != EndTimeLimit {
		t.Fatalf("run 3: err = %v, want an abnormal end of kind %s", err, EndTimeLimit)
	}
	if end.PID == 0 {
		t.Fatal("run 3: no process ID")
	}
	if _, err := os.Stat(filepath.Join(dir, "started")); err != nil {
		t.Errorf("run 3: the agent did not run the command before the stop: %v", err)
	}
	if _, err := syscall.Getpgid(end.PID); err == nil {
		t.Errorf("run 3: the process group leader is still alive")
	}
	// pgrep exits with 1 when no process matches. Any other failure
	// means that the check did not run.
	out, pgrepErr := exec.Command("pgrep", "-g", strconv.Itoa(end.PID)).Output()
	var pgrepExit *exec.ExitError
	switch {
	case pgrepErr == nil:
		t.Errorf("run 3: processes are still in the group of the CLI:\n%s", out)
	case errors.As(pgrepErr, &pgrepExit) && pgrepExit.ExitCode() == 1:
		// No process in the group.
	default:
		t.Fatalf("run 3: pgrep did not run: %v", pgrepErr)
	}
	t.Logf("run 3: kind %s after %s, command started: %v, group empty: %v",
		end.Kind, elapsed.Round(time.Second), fileExists(filepath.Join(dir, "started")), pgrepErr != nil)
}

// TestLive_ReadQuota is the live check of #67: one real minimal run reads
// the quota usage. It runs only with CUMIN_LIVE=1, because it uses a
// small amount of quota. The log holds the field names of the init event
// and of rate_limit_info, and the value of status, but no number.
func TestLive_ReadQuota(t *testing.T) {
	if os.Getenv("CUMIN_LIVE") != "1" {
		t.Skip("set CUMIN_LIVE=1 to run the live check; it uses quota")
	}
	path := os.Getenv("CUMIN_CLAUDE_PATH")
	if path == "" {
		var err error
		if path, err = exec.LookPath("claude"); err != nil {
			t.Fatalf("claude is not on PATH: %v (set CUMIN_CLAUDE_PATH)", err)
		}
	}
	cli := ClaudeCode{Path: path, Logger: recordLogger(t)}
	start := time.Now()
	usage, err := cli.ReadQuota(context.Background())
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ReadQuota: %v", err)
	}
	now := time.Now()
	if !usage.FiveHour.ResetsAt.After(now) || !usage.Weekly.ResetsAt.After(now) {
		t.Error("a reset time is not in the future")
	}
	if usage.FiveHour.Utilization < 0 || usage.FiveHour.Utilization > 1 || usage.Weekly.Utilization < 0 || usage.Weekly.Utilization > 1 {
		t.Error("a utilization is outside 0 to 1")
	}
	t.Logf("quota read: %v, reset times in the future: %v, utilizations in 0 to 1: %v, took %s",
		err == nil, usage.FiveHour.ResetsAt.After(now) && usage.Weekly.ResetsAt.After(now),
		usage.FiveHour.Utilization >= 0 && usage.FiveHour.Utilization <= 1 && usage.Weekly.Utilization >= 0 && usage.Weekly.Utilization <= 1,
		elapsed.Round(time.Second))
}

// recordLogger logs at debug level into the test log, so that the field
// names of the events appear, but replaces every value that is a number
// or an output of the CLI, so that the log can be copied into a record.
func recordLogger(t *testing.T) *slog.Logger {
	omitted := map[string]bool{"five_hour": true, "weekly": true, "five_hour_resets_at": true, "weekly_resets_at": true, "text": true, "session_id": true, "work_dir": true}
	return slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if omitted[a.Key] {
				return slog.String(a.Key, "<omitted>")
			}
			return a
		},
	}))
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// testLogger logs at info level into the test log, without the debug
// lines that hold quota numbers. The session ID and the work directory
// are redacted, so that the log can be copied into a record.
func testLogger(t *testing.T) *slog.Logger {
	return slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{
		Level: slog.LevelInfo,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			// The login of the bot names the App, which stays out of records.
			if a.Key == "session_id" || a.Key == "work_dir" || a.Key == "login" {
				return slog.String(a.Key, "<redacted>")
			}
			return a
		},
	}))
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// sandbox holds what the live checks on the sandbox repository share.
type sandbox struct {
	owner, repo string
	role        config.Role
	service     *Service
}

// newSandbox skips the test without CUMIN_LIVE=1, reads the sandbox from
// CUMIN_LIVE_REPO, and builds a Service with the Implementer App: the
// client ID from the Host settings, the private key from the Keychain.
func newSandbox(t *testing.T, cliPath string) *sandbox {
	t.Helper()
	if os.Getenv("CUMIN_LIVE") != "1" {
		t.Skip("set CUMIN_LIVE=1 to run the live check")
	}
	owner, repo, ok := strings.Cut(os.Getenv("CUMIN_LIVE_REPO"), "/")
	if !ok || owner == "" || repo == "" {
		t.Fatal("set CUMIN_LIVE_REPO to <owner>/<repo> of the sandbox repository")
	}
	ctx := context.Background()
	role := config.RoleImplementer
	path := os.Getenv("CUMIN_CONFIG")
	if path == "" {
		var err error
		if path, err = config.DefaultPath(); err != nil {
			t.Fatal(err)
		}
	}
	apps, err := config.ReadGitHubApps(path)
	if err != nil {
		t.Fatal(err)
	}
	var clientID string
	for org, ids := range apps {
		if strings.EqualFold(org, owner) {
			clientID = ids[string(role)]
		}
	}
	if clientID == "" {
		t.Fatalf("the Host settings have no github_apps.<owner>.%s for the owner of the sandbox", role)
	}
	store, err := keychain.Default(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes, err := store.GetBase64(ctx, keychain.Service, keychain.PrivateKeyAccount(clientID))
	if err != nil {
		t.Fatal(err)
	}
	key, err := github.ParsePrivateKey(pemBytes)
	if err != nil {
		t.Fatal(err)
	}
	return &sandbox{owner: owner, repo: repo, role: role, service: &Service{
		Roles:  map[config.Role]config.RoleSettings{role: {CLI: config.CLIClaudeCode, CLIPath: cliPath, TimeLimit: 5 * time.Minute}},
		Apps:   map[string]map[config.Role]github.AppCredentials{owner: {role: {ClientID: clientID, PrivateKey: key}}},
		GitHub: github.NewAppClient(github.DefaultBaseURL, nil),
		Logger: testLogger(t),
	}}
}

// worktree prepares a worktree of the sandbox on branch, and removes it
// at the end of the test.
func (sb *sandbox) worktree(t *testing.T, branch string) string {
	t.Helper()
	w := Workspace{Root: filepath.Join(t.TempDir(), "work"), Logger: testLogger(t)}
	c := Checkout{Owner: sb.owner, Repo: sb.repo, Issue: 1, Role: sb.role, Branch: branch}
	dir, err := w.Prepare(context.Background(), "https://github.com/"+sb.owner+"/"+sb.repo+".git", c)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	t.Cleanup(func() { _ = w.Remove(context.Background(), c) })
	return dir
}

// TestLive_AgentEnvironment is the live check of #70 on the sandbox
// repository: a push and a pull request from the environment that cumin
// builds for an agent appear under the Implementer App, not under the
// Owner. It runs the real git and gh in that environment, without Claude
// Code and without quota. It runs only with CUMIN_LIVE=1 and
// CUMIN_LIVE_REPO=<owner>/<repo>. docs/ja/development/live-tests.md says
// how to run it.
//
// The log holds no token, no absolute path of the Host, and no App name.
func TestLive_AgentEnvironment(t *testing.T) {
	sb := newSandbox(t, "claude")
	owner, repo, role, s := sb.owner, sb.repo, sb.role, sb.service
	ctx := context.Background()
	runID := time.Now().UTC().Format("20060102-150405")

	// The worktree of the sandbox, as for an agent.
	branch := "cumin/live-" + runID
	dir := sb.worktree(t, branch)

	// The same steps as Service.Start, without the CLI: the token, the
	// identity, the environment.
	cred, err := s.app(owner, role)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.GitHub.CreateInstallationToken(ctx, cred, string(role), owner, repo)
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	id, err := s.identity(ctx, appKey{strings.ToLower(owner), role}, cred, token.Token)
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	ghConfigDir := t.TempDir()
	env := environment(Credentials{Token: token.Token, AuthorName: id.name, AuthorEmail: id.email}, ghConfigDir)
	api := liveAPI{t: t, base: github.DefaultBaseURL, token: token.Token, owner: owner, repo: repo}

	// git reads no file of the Host user.
	out, err := tool(t, dir, env, "git", "config", "--show-origin", "--list")
	if err != nil {
		t.Fatalf("git config: %v: %s", err, redactToken(out, token.Token))
	}
	workRoot := resolvePath(filepath.Dir(filepath.Dir(filepath.Dir(dir))))
	hostFileRead := false
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		origin, _, _ := strings.Cut(line, "\t")
		if file, ok := strings.CutPrefix(origin, "file:"); ok && !underDir(file, workRoot) {
			hostFileRead = true
			t.Errorf("git read a file outside the work directory: %s", strings.Replace(origin, os.Getenv("HOME"), "<home>", 1))
		}
	}

	// A commit and a push with the token, and a pull request with gh.
	file := filepath.Join(dir, "live", runID+"-agent-env.md")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("A live check of the agent environment of cumin-works.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "live/" + runID + "-agent-env.md"},
		{"commit", "--quiet", "-m", "test: live agent environment " + runID},
		{"push", "--quiet", "-u", "origin", branch},
	} {
		if out, err := tool(t, dir, env, "git", args...); err != nil {
			t.Fatalf("git %s: %v: %s", args[0], err, redactToken(out, token.Token))
		}
	}
	t.Cleanup(func() { api.deleteBranch(branch) })
	sha, err := tool(t, dir, env, "git", "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	sha = strings.TrimSpace(sha)

	out, err = tool(t, dir, env, "gh", "pr", "create", "--base", defaultBranch(t, api), "--head", branch,
		"--title", "test: live agent environment "+runID, "--body", "A live check of cumin-works: a pull request from the environment of an agent. It is closed by the test.")
	if err != nil {
		t.Fatalf("gh pr create: %v: %s", err, redactToken(out, token.Token))
	}
	number := pullNumber(t, out)
	t.Cleanup(func() { api.closePull(number) })

	// The facts on GitHub: the pull request and the commit are the bot.
	var pull struct {
		User struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"user"`
	}
	api.get(fmt.Sprintf("/pulls/%d", number), &pull)
	var commit struct {
		Author    *struct{ Login string } `json:"author"`
		Committer *struct{ Login string } `json:"committer"`
	}
	api.get("/commits/"+sha, &commit)
	pullIsBot := pull.User.Login == id.name && pull.User.Type == "Bot"
	commitIsBot := commit.Author != nil && commit.Committer != nil && commit.Author.Login == id.name && commit.Committer.Login == id.name
	if !pullIsBot {
		t.Errorf("the pull request author is not the bot of the Implementer App (type %s)", pull.User.Type)
	}
	if !commitIsBot {
		t.Error("the commit author or committer is not the bot of the Implementer App")
	}
	t.Logf("git read no Host file: %v; push with the token: true; pull request author is the Implementer bot: %v; commit author and committer are the bot: %v",
		!hostFileRead, pullIsBot, commitIsBot)
}

// liveAPI calls the REST API of the sandbox with the token of the test.
// The token never appears in an error or in the log.
type liveAPI struct {
	t           *testing.T
	base, token string
	owner, repo string
}

func (a liveAPI) do(method, path string, body any) (int, []byte) {
	a.t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			a.t.Fatal(err)
		}
		reader = strings.NewReader(string(encoded))
	}
	req, err := http.NewRequest(method, a.base+"/repos/"+url.PathEscape(a.owner)+"/"+url.PathEscape(a.repo)+path, reader)
	if err != nil {
		a.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatalf("%s %s: request failed", method, path)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, data
}

func (a liveAPI) get(path string, out any) {
	a.t.Helper()
	status, data := a.do(http.MethodGet, path, nil)
	if status != http.StatusOK {
		a.t.Fatalf("GET %s: status %d", path, status)
	}
	if err := json.Unmarshal(data, out); err != nil {
		a.t.Fatalf("GET %s: %v", path, err)
	}
}

// closePull closes a pull request of the test. A failure fails the test:
// the sandbox must hold nothing of the test after the run.
func (a liveAPI) closePull(number int) {
	if status, _ := a.do(http.MethodPatch, fmt.Sprintf("/pulls/%d", number), map[string]any{"state": "closed"}); status != http.StatusOK {
		a.t.Errorf("cleanup: close pull request %d: status %d; close it by hand", number, status)
	}
}

// deleteBranch deletes a branch of the test. A branch that was never
// pushed answers 422 (the reference does not exist), which leaves
// nothing behind. Any other failure fails the test.
func (a liveAPI) deleteBranch(branch string) {
	status, _ := a.do(http.MethodDelete, "/git/refs/heads/"+branch, nil)
	if status != http.StatusNoContent && status != http.StatusUnprocessableEntity {
		a.t.Errorf("cleanup: delete branch %s: status %d; delete it by hand", branch, status)
	}
}

func defaultBranch(t *testing.T, api liveAPI) string {
	t.Helper()
	var repository struct {
		DefaultBranch string `json:"default_branch"`
	}
	api.get("", &repository)
	if repository.DefaultBranch == "" {
		t.Fatal("the sandbox has no default branch")
	}
	return repository.DefaultBranch
}

// pullNumber reads the number from the address that gh pr create prints.
func pullNumber(t *testing.T, out string) int {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if i := strings.LastIndex(line, "/pull/"); i >= 0 {
			if n, err := strconv.Atoi(strings.TrimSpace(line[i+len("/pull/"):])); err == nil {
				return n
			}
		}
	}
	t.Fatalf("gh pr create printed no pull request address: %s", out)
	return 0
}

func redactToken(s, token string) string {
	return strings.ReplaceAll(s, token, "[redacted]")
}

// TestLive_AgentRunOnSandbox is the optional second form of the live
// check of #70: a real Claude Code run through Service.Start commits one
// file, pushes, and opens a pull request on the sandbox, and both appear
// under the Implementer App. It uses quota (the minimal quota run and one
// agent run), so it runs only with CUMIN_LIVE=1 and after the Owner
// agrees. It also exercises the check of the init event with a real run.
func TestLive_AgentRunOnSandbox(t *testing.T) {
	path := os.Getenv("CUMIN_CLAUDE_PATH")
	if path == "" && os.Getenv("CUMIN_LIVE") == "1" {
		var err error
		if path, err = exec.LookPath("claude"); err != nil {
			t.Fatalf("claude is not on PATH: %v (set CUMIN_CLAUDE_PATH)", err)
		}
	}
	sb := newSandbox(t, path)
	ctx := context.Background()
	runID := time.Now().UTC().Format("20060102-150405")
	branch := "cumin/live-" + runID + "-agent"
	dir := sb.worktree(t, branch)
	instruction, err := roles.Instruction(sb.role)
	if err != nil {
		t.Fatal(err)
	}

	// Cleanup by branch name, with a token of the test: the pull request
	// that the agent opens is found through the branch.
	cred, err := sb.service.app(sb.owner, sb.role)
	if err != nil {
		t.Fatal(err)
	}
	token, err := sb.service.GitHub.CreateInstallationToken(ctx, cred, string(sb.role), sb.owner, sb.repo)
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	api := liveAPI{t: t, base: github.DefaultBaseURL, token: token.Token, owner: sb.owner, repo: sb.repo}
	base := defaultBranch(t, api)
	t.Cleanup(func() {
		var pulls []struct {
			Number int `json:"number"`
		}
		api.get("/pulls?state=open&head="+url.QueryEscape(sb.owner+":"+branch), &pulls)
		for _, pull := range pulls {
			api.closePull(pull.Number)
		}
		api.deleteBranch(branch)
	})

	file := "live/" + runID + "-claude.md"
	text := "You are in a git worktree on the branch " + branch + " of the repository " + sb.owner + "/" + sb.repo + ". " +
		"Do exactly the following, with the Bash tool, and nothing else. " +
		"1. Create the file " + file + " with the one line: A live check of cumin-works: a commit by an agent. " +
		"2. Run: git add " + file + " && git commit -m 'test: live agent run " + runID + "'. " +
		"3. Run: git push -u origin " + branch + ". " +
		"4. Run: gh pr create --base " + base + " --head " + branch + " --title 'test: live agent run " + runID + "' --body 'A live check of cumin-works: a pull request by an agent. The test closes it.'. " +
		"Do not change any other file. Do not merge. Then finish with the result done and a summary of one sentence that holds the address of the pull request."
	start := time.Now()
	run, err := sb.service.Start(ctx, StartRequest{
		Owner: sb.owner, Repo: sb.repo, Role: sb.role,
		RoleInstruction: instruction, Text: text, WorkDir: dir,
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if run.Result.Result != ResultDone {
		t.Fatalf("result = %+v, want done", run.Result)
	}

	var pulls []struct {
		Number int `json:"number"`
		User   struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"user"`
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	api.get("/pulls?state=open&head="+url.QueryEscape(sb.owner+":"+branch), &pulls)
	if len(pulls) != 1 {
		t.Fatalf("pull requests from the branch of the agent: %d, want 1", len(pulls))
	}
	pull := pulls[0]
	var commit struct {
		Author    *struct{ Login string } `json:"author"`
		Committer *struct{ Login string } `json:"committer"`
	}
	api.get("/commits/"+pull.Head.SHA, &commit)
	id, err := sb.service.identity(ctx, appKey{strings.ToLower(sb.owner), sb.role}, cred, token.Token)
	if err != nil {
		t.Fatal(err)
	}
	pullIsBot := pull.User.Login == id.name && pull.User.Type == "Bot"
	commitIsBot := commit.Author != nil && commit.Committer != nil && commit.Author.Login == id.name && commit.Committer.Login == id.name
	if !pullIsBot {
		t.Errorf("the pull request author is not the bot of the Implementer App (type %s)", pull.User.Type)
	}
	if !commitIsBot {
		t.Error("the commit author or committer is not the bot of the Implementer App")
	}
	t.Logf("result %s after %s; quota read before the start: true; session ID read: %v; pull request by the agent found: true; author is the Implementer bot: %v; commit author and committer are the bot: %v",
		run.Result.Result, elapsed.Round(time.Second), run.SessionID != "", pullIsBot, commitIsBot)
}
