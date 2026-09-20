package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelpListsSubcommands(t *testing.T) {
	for _, flagName := range []string{"--help", "-h"} {
		t.Run(flagName, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runCLI([]string{flagName}, &stdout, &stderr)
			if code != 0 {
				t.Errorf("exit code = %d, want 0", code)
			}
			for _, name := range []string{"run", "status", "quota allow", "setup"} {
				if !strings.Contains(stdout.String(), "\n  "+name+" ") {
					t.Errorf("help does not list %q:\n%s", name, stdout.String())
				}
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want empty", stderr.String())
			}
		})
	}
}

func TestSubcommandThatIsNotBuiltFails(t *testing.T) {
	tests := []struct {
		args []string
		name string
	}{
		{[]string{"status"}, "status"},
		{[]string{"quota", "allow"}, "quota allow"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runCLI(tt.args, &stdout, &stderr)
			if code == 0 {
				t.Errorf("exit code = 0, want non-zero")
			}
			want := "cumin " + tt.name + ": not built yet\n"
			if stderr.String() != want {
				t.Errorf("stderr = %q, want %q", stderr.String(), want)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}

func TestBadUsagePrintsUsageAndFails(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		message string
	}{
		{"no command", nil, ""},
		{"unknown command", []string{"nope"}, `unknown command "nope"`},
		{"quota without allow", []string{"quota"}, `unknown command "quota"`},
		{"unknown flag", []string{"--nope"}, "flag provided but not defined"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runCLI(tt.args, &stdout, &stderr)
			if code == 0 {
				t.Errorf("exit code = 0, want non-zero")
			}
			if !strings.Contains(stderr.String(), tt.message) {
				t.Errorf("stderr does not contain %q:\n%s", tt.message, stderr.String())
			}
			if !strings.Contains(stderr.String(), "Usage:") {
				t.Errorf("stderr does not contain the usage:\n%s", stderr.String())
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const validConfig = `
repositories = ["example-org/example-repo"]
work_dir = "/tmp/cumin-work"
`

func TestRunLoadsSettingsBeforeItStops(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runCLI([]string{"run", "--config", writeConfig(t, validConfig)}, &stdout, &stderr)
	if code == 0 {
		t.Errorf("exit code = 0, want non-zero")
	}
	if want := "cumin run: not built yet\n"; stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestRunWithInvalidSettingsNamesTheKey(t *testing.T) {
	path := writeConfig(t, validConfig+"[roles.implementer]\ntime_limit = \"56m\"\n")
	var stdout, stderr bytes.Buffer
	code := runCLI([]string{"run", "--config", path}, &stdout, &stderr)
	if code == 0 {
		t.Errorf("exit code = 0, want non-zero")
	}
	if !strings.Contains(stderr.String(), "roles.implementer.time_limit:") {
		t.Errorf("stderr does not name the key:\n%s", stderr.String())
	}
	if strings.Contains(stderr.String(), "not built yet") {
		t.Errorf("run continued after invalid settings:\n%s", stderr.String())
	}
}

func TestRunWithoutConfigFlagUsesTheDefaultPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var stdout, stderr bytes.Buffer
	code := runCLI([]string{"run"}, &stdout, &stderr)
	if code == 0 {
		t.Errorf("exit code = 0, want non-zero")
	}
	want := filepath.Join(home, ".config", "cumin", "config.toml")
	if !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr does not name the default path %s:\n%s", want, stderr.String())
	}
}

func TestSetupChecksTheArgumentsBeforeItOpensAnything(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
		message  string
	}{
		{"no subcommand", []string{"setup"}, exitBadUsage, "usage: cumin setup github-apps"},
		{"unknown subcommand", []string{"setup", "repo"}, exitBadUsage, "usage: cumin setup github-apps"},
		{"no organization", []string{"setup", "github-apps"}, exitBadUsage, "usage: cumin setup github-apps"},
		{"extra argument", []string{"setup", "github-apps", "--org", "example-org", "extra"}, exitBadUsage, "usage: cumin setup github-apps"},
		{"App name too long", []string{"setup", "github-apps", "--org", "example-org", "--name-prefix", "a-prefix-that-is-far-too-long-"}, exitFailure, "GitHub allows 34"},
		{"wrong organization name", []string{"setup", "github-apps", "--org", "example/org"}, exitFailure, "not a name of an organization"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := runCLI(tt.args, &stdout, &stderr); code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}
			if !strings.Contains(stderr.String(), tt.message) {
				t.Errorf("stderr does not contain %q:\n%s", tt.message, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}
