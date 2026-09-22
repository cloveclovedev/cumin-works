package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
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

// The settings load first. Without the Client ID of one of the four Apps
// for the owner of a target repository, run stops with the key name, before
// it touches the Keychain or GitHub.
func TestRunWithoutAClientIDNamesTheKey(t *testing.T) {
	tests := []struct {
		name   string
		config string
		want   string
	}{
		{"no github_apps table", validConfig, "github_apps.example-org.cumin-core"},
		{"table without cumin-core", validConfig + "[github_apps.example-org]\nimplementer = \"client-id-implementer\"\n", "github_apps.example-org.cumin-core"},
		{"table without the Implementer", validConfig + "[github_apps.example-org]\ncumin-core = \"client-id-core\"\nchief-engineer = \"client-id-chief\"\nreviewer = \"client-id-reviewer\"\n", "github_apps.example-org.implementer"},
		{"table of another organization", validConfig + "[github_apps.other-org]\ncumin-core = \"client-id-core\"\n", "github_apps.example-org.cumin-core"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runCLI([]string{"run", "--config", writeConfig(t, tt.config)}, &stdout, &stderr)
			if code != exitFailure {
				t.Errorf("exit code = %d, want %d", code, exitFailure)
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Errorf("stderr does not name the key %s:\n%s", tt.want, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want no log line", stdout.String())
			}
		})
	}
}

func TestRunRejectsAnExtraArgument(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runCLI([]string{"run", "typo", "--config", writeConfig(t, validConfig)}, &stdout, &stderr)
	if code != exitBadUsage {
		t.Errorf("exit code = %d, want %d", code, exitBadUsage)
	}
	if !strings.Contains(stderr.String(), `unexpected argument "typo"`) {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestAppClientIDsRejectsTwoTablesOfOneOwner(t *testing.T) {
	settings := &config.Settings{
		Repositories: []config.Repository{{Owner: "example-org", Name: "one"}},
		GitHubApps: map[string]map[string]string{
			"Example-Org": {config.AppCuminCore: "client-id-a"},
			"example-org": {config.AppCuminCore: "client-id-b"},
		},
	}
	_, err := appClientIDs(settings)
	if err == nil || !strings.Contains(err.Error(), "Example-Org and example-org") {
		t.Errorf("err = %v, want the two tables", err)
	}
}

func TestAppClientIDsReadsEveryAppAndIgnoresTheCaseOfTheOwner(t *testing.T) {
	apps := map[string]string{config.AppCuminCore: "client-id-core"}
	for _, role := range config.AllRoles() {
		apps[string(role)] = "client-id-" + string(role)
	}
	settings := &config.Settings{
		Repositories: []config.Repository{{Owner: "Example-Org", Name: "one"}, {Owner: "example-org", Name: "two"}},
		GitHubApps:   map[string]map[string]string{"example-org": apps},
	}
	ids, err := appClientIDs(settings)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 {
		t.Fatalf("ids = %v, want one owner", ids)
	}
	if got := ids["example-org"]; len(got) != len(config.AllApps()) || got[config.AppCuminCore] != "client-id-core" ||
		got[string(config.RoleImplementer)] != "client-id-implementer" {
		t.Errorf("ids[example-org] = %v", got)
	}
}

func TestRemoteURLIsTheHTTPSAddressOfTheRepository(t *testing.T) {
	want := "https://github.com/example-org/example-repo.git"
	if got := remoteURL(config.Repository{Owner: "example-org", Name: "example-repo"}); got != want {
		t.Errorf("remoteURL = %q, want %q", got, want)
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
	if strings.Contains(stderr.String(), "github_apps") {
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
