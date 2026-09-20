package main

import (
	"bytes"
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
		{[]string{"run"}, "run"},
		{[]string{"status"}, "status"},
		{[]string{"quota", "allow"}, "quota allow"},
		{[]string{"setup"}, "setup"},
		{[]string{"setup", "github-apps", "--org", "example-org"}, "setup"},
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
