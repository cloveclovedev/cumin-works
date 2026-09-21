package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// required has only the keys that have no default.
const required = `
repositories = ["example-org/example-repo"]
work_dir = "/tmp/cumin-work"
`

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadAppliesDefaults(t *testing.T) {
	s, err := Load(writeFile(t, required))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(s.Repositories) != 1 || s.Repositories[0].String() != "example-org/example-repo" {
		t.Errorf("Repositories = %v", s.Repositories)
	}
	if s.WorkDir != "/tmp/cumin-work" {
		t.Errorf("WorkDir = %q", s.WorkDir)
	}
	if s.PollInterval != 60*time.Second {
		t.Errorf("PollInterval = %v, want 60s", s.PollInterval)
	}
	if s.MaxIssuesInProgress != 1 {
		t.Errorf("MaxIssuesInProgress = %d, want 1", s.MaxIssuesInProgress)
	}
	if s.MaxReviewRounds != 3 {
		t.Errorf("MaxReviewRounds = %d, want 3", s.MaxReviewRounds)
	}
	if s.MaxCheckFixRequests != 3 {
		t.Errorf("MaxCheckFixRequests = %d, want 3", s.MaxCheckFixRequests)
	}
	if s.MergeMethod != MergeSquash {
		t.Errorf("MergeMethod = %q, want squash", s.MergeMethod)
	}
	if s.RequestCommand != "" {
		t.Errorf("RequestCommand = %q, want empty", s.RequestCommand)
	}
	for _, role := range []Role{RoleChiefEngineer, RoleImplementer, RoleReviewer} {
		want := RoleSettings{TimeLimit: 50 * time.Minute, CLI: CLIClaudeCode, CLIPath: "claude"}
		if got := s.Roles[role]; got != want {
			t.Errorf("Roles[%s] = %+v, want %+v", role, got, want)
		}
	}
	if len(s.GitHubApps) != 0 {
		t.Errorf("GitHubApps = %v, want empty", s.GitHubApps)
	}
}

func TestLoadReadsEveryKey(t *testing.T) {
	s, err := Load(writeFile(t, `
repositories = ["example-org/first", "example-org/second"]
work_dir = "/tmp/cumin-work"
poll_interval = "30s"
max_issues_in_progress = 2
max_review_rounds = 5
max_check_fix_requests = 4
merge_method = "rebase"
request_command = "/opt/example/bin/request"

[roles.implementer]
time_limit = "55m"
cli_path = "/opt/example/bin/claude"
model = "example-model"

[github_apps.example-org]
cumin-core = "client-id-core"
chief-engineer = "client-id-chief"
implementer = "client-id-implementer"
reviewer = "client-id-reviewer"
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(s.Repositories) != 2 || s.Repositories[1] != (Repository{"example-org", "second"}) {
		t.Errorf("Repositories = %v", s.Repositories)
	}
	if s.PollInterval != 30*time.Second || s.MaxIssuesInProgress != 2 ||
		s.MaxReviewRounds != 5 || s.MaxCheckFixRequests != 4 || s.MergeMethod != MergeRebase ||
		s.RequestCommand != "/opt/example/bin/request" {
		t.Errorf("top-level settings = %+v", s)
	}
	want := RoleSettings{TimeLimit: 55 * time.Minute, CLI: CLIClaudeCode, CLIPath: "/opt/example/bin/claude", Model: "example-model"}
	if got := s.Roles[RoleImplementer]; got != want {
		t.Errorf("Roles[implementer] = %+v, want %+v", got, want)
	}
	// A role that is not in the file keeps the defaults.
	if got := s.Roles[RoleReviewer].TimeLimit; got != 50*time.Minute {
		t.Errorf("Roles[reviewer].TimeLimit = %v, want 50m", got)
	}
	if got := s.GitHubApps["example-org"][AppCuminCore]; got != "client-id-core" {
		t.Errorf("GitHubApps[example-org][cumin-core] = %q", got)
	}
}

func TestLoadExpandsHomeInWorkDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	s, err := Load(writeFile(t, `
repositories = ["example-org/example-repo"]
work_dir = "~/cumin-work"
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := filepath.Join(home, "cumin-work"); s.WorkDir != want {
		t.Errorf("WorkDir = %q, want %q", s.WorkDir, want)
	}
}

// Each case breaks one limit of the settings table. The error must name the
// full key.
func TestLoadRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantKey string
	}{
		{"no repositories", `work_dir = "/w"`, "repositories:"},
		{"empty repositories", `repositories = []` + "\n" + `work_dir = "/w"`, "repositories:"},
		{"repository without owner", `repositories = ["example-repo"]` + "\n" + `work_dir = "/w"`, "repositories[0]:"},
		{"repository with three parts", `repositories = ["a/b/c"]` + "\n" + `work_dir = "/w"`, "repositories[0]:"},
		{"duplicate repository", `repositories = ["example-org/a", "Example-Org/A"]` + "\n" + `work_dir = "/w"`, "repositories[1]:"},
		{"no work_dir", `repositories = ["example-org/a"]`, "work_dir:"},
		{"empty work_dir", `repositories = ["example-org/a"]` + "\n" + `work_dir = ""`, "work_dir:"},
		{"poll_interval zero", required + `poll_interval = "0s"`, "poll_interval:"},
		{"poll_interval negative", required + `poll_interval = "-1s"`, "poll_interval:"},
		{"poll_interval without unit", required + `poll_interval = 60`, `"poll_interval"`},
		{"max_issues_in_progress zero", required + `max_issues_in_progress = 0`, "max_issues_in_progress:"},
		{"max_review_rounds zero", required + `max_review_rounds = 0`, "max_review_rounds:"},
		{"max_check_fix_requests zero", required + `max_check_fix_requests = 0`, "max_check_fix_requests:"},
		{"merge_method unknown", required + `merge_method = "fast-forward"`, "merge_method:"},
		{"time_limit over 55 minutes", required + "[roles.implementer]\n" + `time_limit = "56m"`, "roles.implementer.time_limit:"},
		{"time_limit zero", required + "[roles.reviewer]\n" + `time_limit = "0s"`, "roles.reviewer.time_limit:"},
		{"cli not supported", required + "[roles.chief-engineer]\n" + `cli = "codex"`, "roles.chief-engineer.cli:"},
		{"empty cli_path", required + "[roles.implementer]\n" + `cli_path = ""`, "roles.implementer.cli_path:"},
		{"empty client ID", required + "[github_apps.example-org]\n" + `implementer = ""`, "github_apps.example-org.implementer:"},
		{"unknown GitHub App", required + "[github_apps.example-org]\n" + `tester = "client-id"`, "github_apps.example-org.tester:"},
		{"wrong type", required + `max_review_rounds = "three"`, `"max_review_rounds"`},
		{"wrong type in a table", required + "[roles.implementer]\n" + `model = 5`, `"roles.implementer.model"`},
		{"unknown key", required + `pol_interval = "60s"`, "pol_interval: unknown key"},
		{"unknown role", required + "[roles.tester]\n" + `cli = "claude-code"`, "roles.tester: unknown key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeFile(t, tt.content))
			if err == nil {
				t.Fatal("Load succeeded, want an error")
			}
			if !strings.Contains(err.Error(), tt.wantKey) {
				t.Errorf("error does not name %s:\n%v", tt.wantKey, err)
			}
		})
	}
}

func TestLoadAcceptsTimeLimitOf55Minutes(t *testing.T) {
	s, err := Load(writeFile(t, required+"[roles.implementer]\n"+`time_limit = "55m"`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := s.Roles[RoleImplementer].TimeLimit; got != 55*time.Minute {
		t.Errorf("TimeLimit = %v, want 55m", got)
	}
}

func TestLoadReportsEveryProblem(t *testing.T) {
	_, err := Load(writeFile(t, required+`
poll_interval = "0s"
merge_method = "fast-forward"
pol_interval = "60s"
`))
	if err == nil {
		t.Fatal("Load succeeded, want an error")
	}
	for _, key := range []string{"poll_interval:", "merge_method:", "pol_interval: unknown key"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error does not name %s:\n%v", key, err)
		}
	}
}

func TestLoadMissingFileNamesThePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.toml")
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load succeeded, want an error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error does not name the path %s:\n%v", path, err)
	}
}

// The example in the settings reference must stay loadable.
func TestLoadAcceptsTheDocumentedExample(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "ja", "development", "configuration.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, rest, found := strings.Cut(string(doc), "```toml\n")
	if !found {
		t.Fatal("configuration.md has no toml example")
	}
	example, _, _ := strings.Cut(rest, "```")

	t.Setenv("HOME", t.TempDir())
	if _, err := Load(writeFile(t, example)); err != nil {
		t.Errorf("the documented example does not load: %v", err)
	}
}
