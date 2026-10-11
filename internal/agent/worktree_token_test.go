package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
)

// The tests cover requirement 2 of #790: cumin clones and fetches with the
// token of the App of the role, and the token stays in the environment of
// that one git command.

// gitTestToken is the token that the test remote accepts.
const gitTestToken = "ghs_exampleTokenOfTheGitCommands"

// tokenRemote is a remote over HTTP that refuses every request without the
// token, as a private repository does. It serves the files of the bare
// repository (the "dumb" protocol of git over HTTP), so it needs no
// program of git on the server side.
type tokenRemote struct {
	*remote
	url string

	mu      sync.Mutex
	allowed int
	refused int
}

func newTokenRemote(t *testing.T) *tokenRemote {
	t.Helper()
	r := &tokenRemote{remote: newRemote(t)}
	r.publish()
	files := http.FileServer(http.Dir(filepath.Dir(r.path)))
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+gitTestToken))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if req.Header.Get("Authorization") != want {
			r.refused++
			w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		r.allowed++
		files.ServeHTTP(w, req)
	}))
	t.Cleanup(server.Close)
	r.url = server.URL + "/" + filepath.Base(r.path)
	return r
}

// publish writes the files that the dumb protocol reads (git-update-server-info).
func (r *tokenRemote) publish() {
	r.t.Helper()
	gitCmd(r.t, r.path, "update-server-info")
}

func (r *tokenRemote) requests() (allowed, refused int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.allowed, r.refused
}

// tokenCalls records the checkouts that a Workspace asked a token for.
type tokenCalls struct {
	mu    sync.Mutex
	calls []Checkout
}

func (c *tokenCalls) token(_ context.Context, checkout Checkout) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, checkout)
	return gitTestToken, nil
}

func (c *tokenCalls) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

// requireNoToken fails when text holds the token in one of its forms.
func requireNoToken(t *testing.T, what, text string) {
	t.Helper()
	for _, secret := range (Credentials{Token: gitTestToken}).secrets() {
		if strings.Contains(text, secret) {
			t.Errorf("%s holds the token:\n%s", what, text)
		}
	}
}

// requireNoTokenUnder fails when a file under root holds the token.
func requireNoTokenUnder(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		requireNoToken(t, "the file "+path, string(content))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWorktree_CloneAndFetchCarryTheTokenOfTheRole(t *testing.T) {
	r := newTokenRemote(t)
	var logs bytes.Buffer
	calls := &tokenCalls{}
	w := newWorkspace(t, &logs)
	w.Token = calls.token
	c := checkout(3, config.RoleImplementer, "cumin/3-add-thing")

	// The clone, the fetch, and the read of the default branch.
	dir, err := w.Prepare(context.Background(), r.url, c)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); err != nil {
		t.Errorf("the worktree lacks the file of the remote: %v", err)
	}
	if calls.count() != 1 {
		t.Errorf("%d tokens for one prepare, want 1", calls.count())
	}
	if got := calls.calls[0]; got.Owner != c.Owner || got.Repo != c.Repo || got.Role != config.RoleImplementer {
		t.Errorf("the token is for %+v, want the repository and the role of the worktree", got)
	}

	// A worktree that exists is reused without git on the remote.
	if _, err := w.Prepare(context.Background(), r.url, c); err != nil {
		t.Fatalf("Prepare of the same worktree: %v", err)
	}
	if calls.count() != 1 {
		t.Errorf("%d tokens after a reused worktree, want 1", calls.count())
	}

	// The fetch of a continuation, then the fetch of a new prepare.
	pushed := r.commit("cumin/3-add-thing", "work.txt", "pushed\n")
	r.publish()
	removed, err := w.RemoveIfPushed(context.Background(), c)
	if err != nil || !removed {
		t.Fatalf("RemoveIfPushed = %v, %v; want the worktree removed", removed, err)
	}
	if dir, err = w.Prepare(context.Background(), r.url, c); err != nil {
		t.Fatalf("Prepare after the fetch: %v", err)
	}
	if head := gitCmd(t, dir, "rev-parse", "HEAD"); head != pushed {
		t.Errorf("HEAD = %s, want the pushed commit %s", head, pushed)
	}
	if calls.count() != 3 {
		t.Errorf("%d tokens, want 3: one for each prepare and one for the check", calls.count())
	}
	if _, refused := r.requests(); refused != 0 {
		t.Errorf("the remote refused %d requests, want every request with the token", refused)
	}

	// The token stays in the environment of git.
	if got := gitCmd(t, w.CloneDir(c), "remote", "get-url", "origin"); got != r.url {
		t.Errorf("the remote address = %q, want %q", got, r.url)
	}
	requireNoTokenUnder(t, w.Root)
	requireNoToken(t, "the log", logs.String())
}

func TestWorktree_RemoveClosedCarriesTheTokenOfTheRole(t *testing.T) {
	r := newTokenRemote(t)
	calls := &tokenCalls{}
	w := newWorkspace(t, &bytes.Buffer{})
	w.Token = calls.token
	c := checkout(3, config.RoleReviewer, "")

	// No worktree: nothing to check, so no token.
	if kept, err := w.RemoveClosed(context.Background(), c); err != nil || kept {
		t.Fatalf("RemoveClosed without a worktree = %v, %v", kept, err)
	}
	if calls.count() != 0 {
		t.Errorf("%d tokens without a worktree, want 0", calls.count())
	}

	dir, err := w.Prepare(context.Background(), r.url, c)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	// A commit that no branch holds makes the cleanup read the heads of
	// the pull requests too (git ls-remote).
	gitCmd(t, dir, "commit", "--quiet", "--allow-empty", "-m", "not pushed")
	kept, err := w.RemoveClosed(context.Background(), c)
	if err != nil || !kept {
		t.Fatalf("RemoveClosed = %v, %v; want the worktree kept", kept, err)
	}
	if calls.count() != 2 || calls.calls[1].Role != config.RoleReviewer {
		t.Errorf("token calls = %+v, want one for the prepare and one for the cleanup, of the Reviewer", calls.calls)
	}
	if _, refused := r.requests(); refused != 0 {
		t.Errorf("the remote refused %d requests, want every request with the token", refused)
	}
	requireNoTokenUnder(t, w.Root)
}

func TestWorktree_RemoteRefusesGitWithoutTheToken(t *testing.T) {
	r := newTokenRemote(t)
	w := newWorkspace(t, &bytes.Buffer{})
	c := checkout(3, config.RoleImplementer, "cumin/3-add-thing")

	if _, err := w.Prepare(context.Background(), r.url, c); err == nil {
		t.Fatal("Prepare without a token succeeded on a remote that needs one")
	}
	if allowed, refused := r.requests(); allowed != 0 || refused == 0 {
		t.Errorf("the remote allowed %d and refused %d requests, want only refused ones", allowed, refused)
	}
}

// A token that cannot be created fails the prepare. cumin does not try
// again without authentication.
func TestWorktree_PrepareFailsWhenTheTokenIsNotCreated(t *testing.T) {
	r := newTokenRemote(t)
	w := newWorkspace(t, &bytes.Buffer{})
	w.Token = func(context.Context, Checkout) (string, error) { return "", errors.New("status 401") }
	c := checkout(3, config.RoleImplementer, "cumin/3-add-thing")

	_, err := w.Prepare(context.Background(), r.url, c)
	if err == nil || !strings.Contains(err.Error(), "create the token of git: status 401") {
		t.Fatalf("Prepare = %v, want the error of the token", err)
	}
	if allowed, refused := r.requests(); allowed+refused != 0 {
		t.Errorf("git sent %d requests without a token, want 0", allowed+refused)
	}
}

// git prints the header of the request when the Host asks it to trace
// without redaction. The error of cumin still holds no token.
func TestWorktree_GitErrorHoldsNoToken(t *testing.T) {
	r := newTokenRemote(t)
	t.Setenv("GIT_CURL_VERBOSE", "1")
	t.Setenv("GIT_TRACE_REDACT", "0")
	var logs bytes.Buffer
	calls := &tokenCalls{}
	w := newWorkspace(t, &logs)
	w.Token = calls.token
	c := checkout(3, config.RoleImplementer, "cumin/3-add-thing")

	_, err := w.Prepare(context.Background(), strings.TrimSuffix(r.url, "remote.git")+"missing.git", c)
	if err == nil {
		t.Fatal("Prepare of a repository that the remote lacks succeeded")
	}
	requireNoToken(t, "the error", err.Error())
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Errorf("the output of git did not hold the header, so the test proves nothing:\n%v", err)
	}
	requireNoToken(t, "the log", logs.String())
}

// A credential helper of the Host user could answer for the remote. cumin
// never asks it: git reads no configuration of the Host user.
func TestWorktree_GitOnTheRemoteUsesNoCredentialHelperOfTheHost(t *testing.T) {
	r := newTokenRemote(t)
	dir := t.TempDir()
	marker := filepath.Join(dir, "helper-was-called")
	hostConfig := filepath.Join(dir, "gitconfig")
	helper := "[credential]\n\thelper = \"!f() { touch '" + marker + "'; echo username=x-access-token; echo password=" + gitTestToken + "; }; f\"\n"
	if err := os.WriteFile(hostConfig, []byte(helper), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", hostConfig)
	// A header in the environment of cumin does not reach git either.
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "http.extraheader")
	t.Setenv("GIT_CONFIG_VALUE_0", "Authorization: Basic "+(Credentials{Token: gitTestToken}).basicHeader())
	w := newWorkspace(t, &bytes.Buffer{})
	c := checkout(3, config.RoleImplementer, "cumin/3-add-thing")

	if _, err := w.Prepare(context.Background(), r.url, c); err == nil {
		t.Error("Prepare without a token succeeded: git used a credential of the Host")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("git called the credential helper of the Host user")
	}
	if allowed, _ := r.requests(); allowed != 0 {
		t.Errorf("the remote allowed %d requests, want 0", allowed)
	}
}

func TestExtraHeaderKey(t *testing.T) {
	tests := []struct{ remote, want string }{
		{"https://github.com/example-org/example-repo.git", "http.https://github.com/.extraheader"},
		{"http://127.0.0.1:8080/remote.git", "http.http://127.0.0.1:8080/.extraheader"},
		{"/tmp/remote.git", ""},
		{"git://127.0.0.1:9418/example-org/example-repo", ""},
		{"git@github.com:example-org/example-repo.git", ""},
	}
	for _, tt := range tests {
		if got := extraHeaderKey(tt.remote); got != tt.want {
			t.Errorf("extraHeaderKey(%q) = %q, want %q", tt.remote, got, tt.want)
		}
	}
}

func TestGitToken_IsLimitedToTheRepositoryAndToTheReadOfContents(t *testing.T) {
	fake, client := newFakeGitHub(t)
	var logs bytes.Buffer
	s := newService(t, "", client, &logs)

	token, err := s.GitToken(context.Background(), checkout(3, config.RoleImplementer, ""))
	if err != nil {
		t.Fatalf("GitToken: %v", err)
	}
	if token != serviceToken {
		t.Errorf("token = %q, want the token of the fake GitHub", token)
	}
	requests := fake.TokenRequests()
	if len(requests) != 1 {
		t.Fatalf("%d token requests, want 1", len(requests))
	}
	if repos := requests[0].Repositories; len(repos) != 1 || repos[0] != "example-repo" {
		t.Errorf("token repositories = %v", repos)
	}
	if perms := requests[0].Permissions; len(perms) != 1 || perms["contents"] != "read" {
		t.Errorf("token permissions = %v, want only contents: read", perms)
	}
	if strings.Contains(logs.String(), serviceToken) {
		t.Errorf("the info logs hold the token:\n%s", logs.String())
	}

	// No App of the role: no token, and no request to GitHub.
	if _, err := s.GitToken(context.Background(), checkout(3, config.RoleReviewer, "")); err == nil {
		t.Error("GitToken without an App of the role succeeded")
	}
}
