package github_test

// Live checks against a sandbox repository with the registered GitHub Apps.
// They run only with CUMIN_LIVE=1 and only after the Owner agrees.
// docs/ja/development/live-tests.md says how to run them.
//
// Every token stays inside this test process: it is never an argument of a
// process, never part of a remote address, never in a file, and never in the
// test output.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/keychain"
)

// live holds what the live checks share.
type live struct {
	owner, repo string
	runID       string // makes branch and issue names unique
	clientIDs   map[string]string

	mu     sync.Mutex
	tokens map[string]string // app -> installation token
	rows   []resultRow
}

type resultRow struct {
	number                 string
	what, expected, actual string
}

// newLive skips the test unless the Owner enabled the live checks.
func newLive(t *testing.T) *live {
	t.Helper()
	if os.Getenv("CUMIN_LIVE") != "1" {
		t.Skip("live checks run only with CUMIN_LIVE=1")
	}
	owner, repo, ok := strings.Cut(os.Getenv("CUMIN_LIVE_REPO"), "/")
	if !ok || owner == "" || repo == "" {
		t.Fatal("set CUMIN_LIVE_REPO to <owner>/<repo> of the sandbox repository")
	}
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
	var clientIDs map[string]string
	for org, ids := range apps {
		if strings.EqualFold(org, owner) {
			clientIDs = ids
		}
	}
	if len(clientIDs) == 0 {
		t.Fatalf("the Host settings have no github_apps table for %s", owner)
	}
	return &live{owner: owner, repo: repo, runID: time.Now().UTC().Format("20060102-150405"), clientIDs: clientIDs, tokens: map[string]string{}}
}

// credentials reads the client ID from the Host settings and the private key
// from the Keychain.
func (l *live) credentials(t *testing.T, app string) github.AppCredentials {
	t.Helper()
	ctx := context.Background()
	store, err := keychain.Default(ctx)
	if err != nil {
		t.Fatal(err)
	}
	clientID := l.clientIDs[app]
	pemBytes, err := store.GetBase64(ctx, keychain.Service, keychain.PrivateKeyAccount(clientID))
	if err != nil {
		t.Fatalf("app %s: %v", app, err)
	}
	key, err := github.ParsePrivateKey(pemBytes)
	if err != nil {
		t.Fatalf("app %s: %v", app, err)
	}
	return github.AppCredentials{ClientID: clientID, PrivateKey: key}
}

// token returns an installation token of one App, limited to the sandbox
// repository and to the permissions of the App.
func (l *live) token(t *testing.T, app string) string {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	if token, ok := l.tokens[app]; ok {
		return token
	}
	client := github.NewAppClient(github.DefaultBaseURL, nil)
	created, err := client.CreateInstallationToken(context.Background(), l.credentials(t, app), app, l.owner, l.repo)
	if err != nil {
		t.Fatalf("app %s: %v", app, err)
	}
	l.tokens[app] = created.Token
	return created.Token
}

// botLogin returns the login of the bot user of one App: "<slug>[bot]". The
// slug comes from GitHub, so the test holds no App name.
func (l *live) botLogin(t *testing.T, app string) string {
	t.Helper()
	info, err := github.NewAppClient(github.DefaultBaseURL, nil).GetApp(context.Background(), l.credentials(t, app))
	if err != nil {
		t.Fatalf("app %s: %v", app, err)
	}
	return info.Slug + "[bot]"
}

// response is the answer of one API call. It never holds a request header.
type response struct {
	status int
	body   []byte
}

func (r response) json(t *testing.T, out any) {
	t.Helper()
	if err := json.Unmarshal(r.body, out); err != nil {
		t.Fatalf("decode the response (status %d): %v", r.status, err)
	}
}

// message returns the "message" field of an error answer of GitHub.
func (r response) message() string {
	var body struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(r.body, &body)
	return body.Message
}

// api calls the REST API. An empty token makes a call without authentication.
func (l *live) api(t *testing.T, token, method, path string, body any) response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	url := github.DefaultBaseURL + strings.ReplaceAll(path, "{repo}", l.owner+"/"+l.repo)
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return response{status: resp.StatusCode, body: data}
}

// record adds one row to the result table.
func (l *live) record(number, what, expected, actual string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	// An answer of GitHub can have line breaks and "|". Keep one table row.
	actual = strings.ReplaceAll(strings.Join(strings.Fields(actual), " "), "|", "\\|")
	l.rows = append(l.rows, resultRow{number, what, expected, actual})
}

// table returns the results as a Markdown table for the comment on GitHub.
func (l *live) table() string {
	var b strings.Builder
	b.WriteString("| # | What was checked | Expected | Actual |\n|---|---|---|---|\n")
	for _, row := range l.rows {
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", row.number, row.what, row.expected, row.actual)
	}
	return b.String()
}

// gitRepo is a local clone that pushes with the token of one App.
type gitRepo struct {
	dir string
	env []string
}

// newGitRepo makes a clone of the default branch in a temporary directory.
// git gets the token through its environment (GIT_CONFIG_*), so the token is
// not an argument and not in a file. The git settings of the user are not read.
func (l *live) newGitRepo(t *testing.T, token, authorName, authorEmail string) *gitRepo {
	t.Helper()
	basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	g := &gitRepo{dir: t.TempDir(), env: append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.https://github.com/.extraheader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic "+basic,
		"GIT_AUTHOR_NAME="+authorName, "GIT_AUTHOR_EMAIL="+authorEmail,
		"GIT_COMMITTER_NAME="+authorName, "GIT_COMMITTER_EMAIL="+authorEmail,
	)}
	g.mustRun(t, "init", "--quiet")
	g.mustRun(t, "remote", "add", "origin", "https://github.com/"+l.owner+"/"+l.repo+".git")
	g.mustRun(t, "fetch", "--quiet", "--depth", "1", "origin", "main")
	return g
}

// run returns the output of git with the token removed, to be safe.
func (g *gitRepo) run(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Env = g.dir, g.env
	out, err := cmd.CombinedOutput()
	text := string(out)
	for _, e := range g.env {
		if value, ok := strings.CutPrefix(e, "GIT_CONFIG_VALUE_0="); ok {
			text = strings.ReplaceAll(text, value, "[redacted]")
		}
	}
	return text, err
}

func (g *gitRepo) mustRun(t *testing.T, args ...string) string {
	t.Helper()
	out, err := g.run(args...)
	if err != nil {
		t.Fatalf("git %s: %v: %s", args[0], err, out)
	}
	return out
}

// commitFile makes a branch from the default branch with one changed file and
// returns the commit.
func (g *gitRepo) commitFile(t *testing.T, branch, path, content string) string {
	t.Helper()
	g.mustRun(t, "checkout", "--quiet", "-B", branch, "FETCH_HEAD")
	full := g.dir + "/" + path
	if i := strings.LastIndex(full, "/"); i >= 0 {
		if err := os.MkdirAll(full[:i], 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	g.mustRun(t, "add", path)
	g.mustRun(t, "commit", "--quiet", "-m", "test: live check "+branch)
	return strings.TrimSpace(g.mustRun(t, "rev-parse", "HEAD"))
}

// waitForCheck waits until the named check run of a commit is completed and
// returns its conclusion. It reads with the given token.
func (l *live) waitForCheck(t *testing.T, token, sha, name string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		resp := l.api(t, token, http.MethodGet, "/repos/{repo}/commits/"+sha+"/check-runs", nil)
		if resp.status != http.StatusOK {
			t.Fatalf("read the check runs: status %d: %s", resp.status, resp.message())
		}
		var runs struct {
			CheckRuns []struct {
				Name, Status, Conclusion string
			} `json:"check_runs"`
		}
		resp.json(t, &runs)
		for _, run := range runs.CheckRuns {
			if run.Name == name && run.Status == "completed" {
				return run.Conclusion
			}
		}
		time.Sleep(10 * time.Second)
	}
	t.Fatalf("the check %s of %s did not complete in time", name, sha[:7])
	return ""
}
