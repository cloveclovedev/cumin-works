package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The tests cover the environment of the CLI process (#66): a fixed list
// of Host variables, the token for git and gh, and auto memory off.

// recordedEnv reads the environment that the fake CLI saw.
func recordedEnv(t *testing.T, record string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(record + ".env")
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		name, value, _ := strings.Cut(line, "=")
		env[name] = value
	}
	return env
}

// shellVariables are set by /bin/sh itself when it runs the fake CLI.
// They are not part of the environment that cumin passes.
var shellVariables = []string{"PWD", "OLDPWD", "SHLVL", "_"}

func TestEnv_OnlyTheFixedListAndTheCredentials(t *testing.T) {
	// Credentials of the Host that must not reach the agent.
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN", "SSH_AUTH_SOCK", "ANTHROPIC_API_KEY", "CLAUDECODE"} {
		t.Setenv(name, "host-"+name+"-marker")
	}
	t.Setenv("LANG", "en_US.UTF-8")

	path, record := fakeCLI(t, "done.jsonl", 0)
	if _, err := quiet(path).Run(context.Background(), request(t)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	env := recordedEnv(t, record)

	var want []string
	for _, name := range hostVariables {
		if _, ok := os.LookupEnv(name); ok {
			want = append(want, name)
		}
	}
	want = append(want,
		"CLAUDE_CODE_DISABLE_AUTO_MEMORY",
		"GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM", "GIT_TERMINAL_PROMPT", "GIT_SSH_COMMAND",
		"GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0",
		"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL",
		"GH_TOKEN", "GH_CONFIG_DIR", "GH_PROMPT_DISABLED", "GH_NO_UPDATE_NOTIFIER",
	)
	var got []string
	for name := range env {
		if !slices.Contains(shellVariables, name) {
			got = append(got, name)
		}
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("variables of the CLI:\n got %q\nwant %q", got, want)
	}
	for name, value := range env {
		if strings.Contains(value, "-marker") && name != "GH_TOKEN" && name != "GIT_CONFIG_VALUE_0" {
			t.Errorf("%s = %q reached the CLI", name, value)
		}
	}

	cred := testCredentials
	if env["GH_TOKEN"] != cred.Token {
		t.Errorf("GH_TOKEN = %q, want the token of the request", env["GH_TOKEN"])
	}
	basic := strings.TrimPrefix(env["GIT_CONFIG_VALUE_0"], "Authorization: Basic ")
	decoded, err := base64.StdEncoding.DecodeString(basic)
	if err != nil || string(decoded) != "x-access-token:"+cred.Token {
		t.Errorf("GIT_CONFIG_VALUE_0 = %q, want the basic header with the token", env["GIT_CONFIG_VALUE_0"])
	}
	for name, want := range map[string]string{
		"GIT_CONFIG_COUNT":                "1",
		"GIT_CONFIG_KEY_0":                "http.https://github.com/.extraheader",
		"GIT_CONFIG_GLOBAL":               "/dev/null",
		"GIT_CONFIG_NOSYSTEM":             "1",
		"GIT_TERMINAL_PROMPT":             "0",
		"GIT_SSH_COMMAND":                 "false",
		"GIT_AUTHOR_NAME":                 cred.AuthorName,
		"GIT_AUTHOR_EMAIL":                cred.AuthorEmail,
		"GIT_COMMITTER_NAME":              cred.AuthorName,
		"GIT_COMMITTER_EMAIL":             cred.AuthorEmail,
		"GH_PROMPT_DISABLED":              "1",
		"GH_NO_UPDATE_NOTIFIER":           "1",
		"CLAUDE_CODE_DISABLE_AUTO_MEMORY": "1",
		"LANG":                            "en_US.UTF-8",
	} {
		if env[name] != want {
			t.Errorf("%s = %q, want %q", name, env[name], want)
		}
	}
}

func TestEnv_GhConfigDirIsEmptyDuringTheRunAndGoneAfter(t *testing.T) {
	// The fake CLI lists the directory while it runs.
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-claude")
	listing := filepath.Join(dir, "listing")
	fixture, err := filepath.Abs(filepath.Join("testdata", "done.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nls -A \"$GH_CONFIG_DIR\" > " + listing + " && echo \"$GH_CONFIG_DIR\" >> " + listing + "\ncat " + fixture + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := quiet(path).Run(context.Background(), request(t)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	data, err := os.ReadFile(listing)
	if err != nil {
		t.Fatalf("the fake CLI could not list GH_CONFIG_DIR: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("GH_CONFIG_DIR was not empty during the run: %q", lines)
	}
	if _, err := os.Stat(lines[0]); !os.IsNotExist(err) {
		t.Errorf("GH_CONFIG_DIR %q still exists after the run (err %v)", lines[0], err)
	}
}

func TestEnv_RequestWithoutCredentialsIsRefusedBeforeTheStart(t *testing.T) {
	tests := []struct {
		name string
		cred Credentials
		want string
	}{
		{"no token", Credentials{AuthorName: "a", AuthorEmail: "b"}, "no token"},
		{"no name", Credentials{Token: "t", AuthorEmail: "b"}, "no author name"},
		{"no email", Credentials{Token: "t", AuthorName: "a"}, "no author email"},
		{"nothing", Credentials{}, "no token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, record := fakeCLI(t, "done.jsonl", 0)
			req := request(t)
			req.Credentials = tt.cred
			_, err := quiet(path).Run(context.Background(), req)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to say %q", err, tt.want)
			}
			if _, statErr := os.Stat(record); statErr == nil {
				t.Error("the fake CLI ran")
			}
		})
	}
}

// A CLI that prints its environment and the token header, on stdout as a
// line that is not JSON and on stderr, does not put the token into the
// debug logs.
func TestEnv_TokenInTheOutputOfTheCLIIsRedacted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-claude")
	fixture, err := filepath.Abs(filepath.Join("testdata", "done.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho \"token=$GH_TOKEN header=$GIT_CONFIG_VALUE_0\"\necho \"stderr token=$GH_TOKEN header=$GIT_CONFIG_VALUE_0\" >&2\ncat " + fixture + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	c := ClaudeCode{Path: path, Logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))}
	if _, err := c.Run(context.Background(), request(t)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + testCredentials.Token))
	for _, secret := range []string{testCredentials.Token, basic} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("the debug logs hold the secret %q:\n%s", secret[:8], logs.String())
		}
	}
	// The lines were logged, with the marker in place of the secret.
	if n := strings.Count(logs.String(), "[redacted]"); n < 4 {
		t.Errorf("want 4 or more redactions in the logs, got %d:\n%s", n, logs.String())
	}
}

func TestEnv_TokenIsNotLogged(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	path, _ := fakeCLI(t, "done.jsonl", 1) // an abnormal end logs the most
	c := ClaudeCode{Path: path, Logger: logger}
	_, err := c.Run(context.Background(), request(t))
	if err == nil {
		t.Fatal("Run: want an abnormal end")
	}
	for _, text := range []string{logs.String(), err.Error()} {
		if strings.Contains(text, testCredentials.Token) {
			t.Errorf("the token is in the output:\n%s", text)
		}
	}
	c = ClaudeCode{Path: filepath.Join(t.TempDir(), "no-such-cli"), Logger: logger}
	if _, err := c.Run(context.Background(), request(t)); err == nil || strings.Contains(err.Error(), testCredentials.Token) {
		t.Errorf("start failure: err = %v", err)
	}
}
