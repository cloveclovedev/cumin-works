package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// toolOnPath writes an executable shell script with the given name into a
// new directory, and makes that directory, with the directories of the
// system tools, the PATH of cumin.
func toolOnPath(t *testing.T, name, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/usr/bin:/bin")
}

// A tool check passes only with exit code 0 inside its time limit
// (cumin-core.md, test 43 of the top-level requirements).
func TestRunToolCheck_PassesOnlyWithExitCodeZeroInsideTheTimeLimit(t *testing.T) {
	toolOnPath(t, "fake-tool", `case "$1" in ok) exit 0;; slow) sleep 30;; *) exit 3;; esac`)
	for _, tt := range []struct {
		name    string
		command []string
		limit   time.Duration
		want    string
	}{
		{"exit code 0", []string{"fake-tool", "ok"}, 10 * time.Second, ""},
		{"another exit code", []string{"fake-tool", "broken"}, 10 * time.Second, "exit code 3"},
		{"a command that is not found", []string{"fake-tool-that-is-missing"}, 10 * time.Second, "start fake-tool-that-is-missing"},
		{"a time limit that passes", []string{"fake-tool", "slow"}, 200 * time.Millisecond, "the time limit of 200ms passed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			started := time.Now()
			err := RunToolCheck(context.Background(), t.TempDir(), tt.command, tt.limit)
			switch {
			case tt.want == "" && err != nil:
				t.Errorf("err = %v, want a check that passes", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Errorf("err = %v, want one that holds %q", err, tt.want)
			}
			if took := time.Since(started); took > 10*time.Second {
				t.Errorf("the check took %s, want an end at its time limit", took)
			}
		})
	}
}

// The process of a tool check gets the PATH of an agent and the other Host
// variables of the fixed list, no token of a GitHub App, and no other
// variable of the environment of cumin. It runs in the given directory,
// which the runner creates.
func TestRunToolCheck_GetsThePathOfAnAgentAndNoToken(t *testing.T) {
	record := filepath.Join(t.TempDir(), "record")
	toolOnPath(t, "fake-tool", `{ env; echo "PWD_OF_CHECK=$(pwd -P)"; } > "`+record+`"`)
	path := os.Getenv("PATH")
	t.Setenv("GH_TOKEN", "token-of-the-operator")
	t.Setenv("GITHUB_TOKEN", "token-of-the-operator")
	t.Setenv("SSH_AUTH_SOCK", "/socket-of-the-operator")
	dir := filepath.Join(t.TempDir(), "work", "not-yet-there")

	if err := RunToolCheck(context.Background(), dir, []string{"fake-tool"}, 10*time.Second); err != nil {
		t.Fatalf("RunToolCheck: %v", err)
	}
	read, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(read)), "\n") {
		name, value, _ := strings.Cut(line, "=")
		got[name] = value
	}
	if got["PATH"] != path {
		t.Errorf("PATH of the check = %q, want the PATH of an agent %q", got["PATH"], path)
	}
	if got["HOME"] != os.Getenv("HOME") {
		t.Errorf("HOME of the check = %q, want %q", got["HOME"], os.Getenv("HOME"))
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got["PWD_OF_CHECK"] != real {
		t.Errorf("directory of the check = %q, want %q", got["PWD_OF_CHECK"], real)
	}
	for name, value := range got {
		if strings.Contains(value, "of-the-operator") || strings.HasPrefix(name, "GH_") || strings.HasPrefix(name, "GIT_") || name == "GITHUB_TOKEN" {
			t.Errorf("the check got %s=%s, want no token and no credential", name, value)
		}
	}
}
