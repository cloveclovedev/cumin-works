package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The tests below run the real git and gh in the environment that cumin
// builds, without network. A fake home directory holds the settings of
// a Host user (an author, a credential helper). The tools must not read
// them, must use the identity and the token of the request, and must
// not reach SSH.

// fakeHome makes a home directory with the git settings of a Host user
// and points HOME of the test process at it, so that environment() copies
// it into the environment of the tools.
func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	gitconfig := "[user]\n\tname = host-user\n\temail = host-user@example.invalid\n[credential]\n\thelper = osxkeychain\n"
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(gitconfig), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	return home
}

// tool runs one command in dir with env and returns its combined output.
func tool(t *testing.T, dir string, env []string, name string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir, cmd.Env = dir, env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestEnv_RealGitReadsNoHostSettingsAndCommitsAsTheBot(t *testing.T) {
	home := fakeHome(t)
	env := environment(testCredentials, t.TempDir())
	repo := t.TempDir()
	if out, err := tool(t, repo, env, "git", "init", "--quiet"); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}

	// Control: without the isolation, git reads the fake home.
	plain := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home}
	out, err := tool(t, repo, plain, "git", "config", "--show-origin", "--list")
	if err != nil || !strings.Contains(out, home) {
		t.Fatalf("the control run did not read the fake home (err %v):\n%s", err, out)
	}

	// With the isolation: no configuration file outside the repository.
	out, err = tool(t, repo, env, "git", "config", "--show-origin", "--list")
	if err != nil {
		t.Fatalf("git config: %v: %s", err, out)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		origin, _, _ := strings.Cut(line, "\t")
		if strings.HasPrefix(origin, "file:") && !strings.HasPrefix(origin, "file:.git/") {
			t.Errorf("git read a file outside the repository: %s", line)
		}
		if strings.Contains(line, "host-user") || strings.Contains(line, "credential.helper") {
			t.Errorf("git read the settings of the Host user: %s", line)
		}
	}
	if !strings.Contains(out, "http.https://github.com/.extraheader=Authorization: Basic ") {
		t.Errorf("git does not see the token header:\n%s", out)
	}

	// A commit uses the identity of the request, not the one of the home.
	if out, err := tool(t, repo, env, "git", "commit", "--quiet", "--allow-empty", "-m", "test"); err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}
	out, err = tool(t, repo, env, "git", "log", "-1", "--format=%an <%ae> %cn <%ce>")
	if err != nil {
		t.Fatalf("git log: %v: %s", err, out)
	}
	want := testCredentials.AuthorName + " <" + testCredentials.AuthorEmail + "> " + testCredentials.AuthorName + " <" + testCredentials.AuthorEmail + ">"
	if got := strings.TrimSpace(out); got != want {
		t.Errorf("commit identity = %q, want %q", got, want)
	}

	// SSH is not available: the remote operation fails without a network.
	out, err = tool(t, repo, env, "git", "ls-remote", "git@github.com:example/example.git")
	if err == nil {
		t.Errorf("git ls-remote over SSH succeeded:\n%s", out)
	}
	if !strings.Contains(out, "Could not read from remote repository") {
		t.Errorf("git ls-remote over SSH failed for another reason:\n%s", out)
	}
}

func TestEnv_RealGhUsesTheTokenOfTheRequest(t *testing.T) {
	if _, err := exec.LookPath("gh"); err != nil {
		t.Skip("gh is not installed")
	}
	fakeHome(t)
	ghConfigDir := t.TempDir()
	env := environment(testCredentials, ghConfigDir)
	dir := t.TempDir()

	// gh reports the token that it uses. With GH_TOKEN set, that is the
	// token of the request, without a call to GitHub (gh help environment).
	out, err := tool(t, dir, env, "gh", "auth", "token")
	if err != nil {
		t.Fatalf("gh auth token: %v: %s", err, out)
	}
	if got := strings.TrimSpace(out); got != testCredentials.Token {
		t.Errorf("gh auth token = %q, want the token of the request", got)
	}

	// The empty configuration directory gives the defaults of gh.
	out, err = tool(t, dir, env, "gh", "config", "get", "git_protocol")
	if err != nil {
		t.Fatalf("gh config get: %v: %s", err, out)
	}
	if got := strings.TrimSpace(out); got != "https" {
		t.Errorf("gh git_protocol = %q, want https", got)
	}
}
