package config

import (
	"strings"
	"testing"
	"time"
)

// hostSettings loads the Host settings with the given extra keys.
func hostSettings(t *testing.T, extra string) *Settings {
	t.Helper()
	s, err := Load(writeFile(t, required+extra))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return s
}

func withRepository(t *testing.T, host *Settings, file string) *Settings {
	t.Helper()
	s, err := host.WithRepository([]byte(file))
	if err != nil {
		t.Fatalf("WithRepository: %v", err)
	}
	return s
}

// The three levels of one key that a repository may override: the default,
// the Host settings file, and the file of the repository.
func TestWithRepository_ThreeLevelsOfOneKey(t *testing.T) {
	tests := []struct {
		name string
		host string
		file string
		want int
	}{
		{"the default", "", "", defaultMaxReviewRounds},
		{"the Host file", "max_review_rounds = 5\n", "", 5},
		{"the repository file over the Host file", "max_review_rounds = 5\n", "max_review_rounds = 2\n", 2},
		{"the repository file over the default", "", "max_review_rounds = 2\n", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := withRepository(t, hostSettings(t, tt.host), tt.file)
			if s.MaxReviewRounds != tt.want {
				t.Errorf("max_review_rounds = %d, want %d", s.MaxReviewRounds, tt.want)
			}
		})
	}
}

func TestWithRepository_SetsTheOtherOverridableKeys(t *testing.T) {
	host := hostSettings(t, "max_check_fix_requests = 2\nmerge_method = \"squash\"\n")
	s := withRepository(t, host, "max_check_fix_requests = 1\nmerge_method = \"rebase\"\n")
	if s.MaxCheckFixRequests != 1 {
		t.Errorf("max_check_fix_requests = %d, want 1", s.MaxCheckFixRequests)
	}
	if s.MergeMethod != MergeRebase {
		t.Errorf("merge_method = %q, want rebase", s.MergeMethod)
	}
}

func TestWithRepository_SetsTheCLIAndTheModelOfOneRole(t *testing.T) {
	host := hostSettings(t, "[roles.implementer]\ncli_path = \"/opt/claude\"\ntime_limit = \"40m\"\nmodel = \"opus\"\n")
	s := withRepository(t, host, "[roles.implementer]\nmodel = \"sonnet\"\n")

	implementer := s.Roles[RoleImplementer]
	if implementer.Model != "sonnet" {
		t.Errorf("model = %q, want sonnet", implementer.Model)
	}
	// The keys of the Host of the same role stay.
	if implementer.CLIPath != "/opt/claude" || implementer.TimeLimit != 40*time.Minute || implementer.CLI != CLIClaudeCode {
		t.Errorf("implementer = %+v, want the Host path, time limit, and CLI", implementer)
	}
	// The other roles stay.
	if s.Roles[RoleReviewer].Model != "" {
		t.Errorf("reviewer = %+v, want the Host settings", s.Roles[RoleReviewer])
	}
	// An empty model is a value: it means the default model of the CLI.
	s = withRepository(t, host, "[roles.implementer]\nmodel = \"\"\n")
	if s.Roles[RoleImplementer].Model != "" {
		t.Errorf("model = %q, want empty", s.Roles[RoleImplementer].Model)
	}
}

// The starter file that scripts/setup-repo.sh writes holds only
// protected_paths, which cumin does not read.
func TestWithRepository_ProtectedPathsChangesNothing(t *testing.T) {
	host := hostSettings(t, "max_review_rounds = 5\n")
	s := withRepository(t, host, "protected_paths = [\n  \".cumin/\",\n  \"CLAUDE.md\",\n]\n")
	if s.MaxReviewRounds != 5 || s.MergeMethod != MergeSquash {
		t.Errorf("settings = %+v, want the Host values", s)
	}
}

// The example of docs/ja/development/configuration.md, section on the
// repository settings file. A key of the root that stands after a table
// belongs to that table in TOML, so the example is checked here.
func TestWithRepository_TheExampleOfTheDocumentation(t *testing.T) {
	const example = `
max_review_rounds = 2
merge_method = "rebase"

protected_paths = [
  ".cumin/",
  "CLAUDE.md",
  "AGENTS.md",
  ".claude/",
]

[roles.implementer]
model = "sonnet"
`
	s := withRepository(t, hostSettings(t, ""), example)
	if s.MaxReviewRounds != 2 || s.MergeMethod != MergeRebase || s.Roles[RoleImplementer].Model != "sonnet" {
		t.Errorf("settings = %+v", s)
	}
}

func TestWithRepository_EmptyFileKeepsTheHostSettings(t *testing.T) {
	host := hostSettings(t, "max_review_rounds = 5\nmerge_method = \"merge\"\n")
	s := withRepository(t, host, "# nothing but a comment\n")
	if s.MaxReviewRounds != 5 || s.MergeMethod != MergeMerge {
		t.Errorf("settings = %+v, want the Host values", s)
	}
}

func TestWithRepository_AKeyOfTheHostIsAnError(t *testing.T) {
	tests := []struct {
		file string
		want string
	}{
		{"repositories = [\"example-org/other\"]\n", "repositories: " + reasonHostOnly},
		{"work_dir = \"/tmp/other\"\n", "work_dir: " + reasonHostOnly},
		{"poll_interval = \"5s\"\n", "poll_interval: " + reasonHostOnly},
		{"max_issues_in_progress = 4\n", "max_issues_in_progress: " + reasonHostOnly},
		{"[quota.five_hour]\nthreshold = 100\n", "quota: " + reasonHostOnly},
		{"[github_apps.example-org]\ncumin-core = \"client-id\"\n", "github_apps: " + reasonHostOnly},
		{"[roles.implementer]\ncli_path = \"/tmp/claude\"\n", "roles.implementer.cli_path: " + reasonHostOnly},
		{"[roles.implementer]\ntime_limit = \"55m\"\n", "roles.implementer.time_limit: " + reasonHostOnly},
	}
	for _, tt := range tests {
		t.Run(strings.SplitN(tt.want, ":", 2)[0], func(t *testing.T) {
			_, err := hostSettings(t, "").WithRepository([]byte(tt.file))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
			// One mistake is reported once, even when the parser reports the
			// table and every key below it.
			if err != nil && strings.Count(err.Error(), "\n") != 0 {
				t.Errorf("err has more than one line:\n%v", err)
			}
		})
	}
}

func TestWithRepository_AnUnknownKeyIsAnError(t *testing.T) {
	tests := []struct {
		name string
		file string
		want string
	}{
		{"a key that no table has", "nonsense = 1\n", "nonsense: " + reasonUnknown},
		{"a key below a role", "[roles.implementer]\nnonsense = 1\n", "roles.implementer.nonsense: " + reasonUnknown},
		{"a role that does not exist", "[roles.tester]\ncli = \"claude-code\"\n", "roles.tester: " + reasonNotARole},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := hostSettings(t, "").WithRepository([]byte(tt.file))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

// A value outside its limit gives the same error as in the Host file, so
// that the two files cannot drift apart.
func TestWithRepository_AValueOutsideItsLimitGivesTheErrorOfTheHostFile(t *testing.T) {
	tests := []struct {
		name string
		file string
	}{
		{"max_review_rounds", "max_review_rounds = 0\n"},
		{"max_check_fix_requests", "max_check_fix_requests = 0\n"},
		{"merge_method", "merge_method = \"fast-forward\"\n"},
		{"roles.implementer.cli", "[roles.implementer]\ncli = \"codex\"\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, repositoryErr := hostSettings(t, "").WithRepository([]byte(tt.file))
			if repositoryErr == nil {
				t.Fatal("WithRepository accepted the value")
			}
			_, hostErr := Load(writeFile(t, required+tt.file))
			if hostErr == nil {
				t.Fatal("Load accepted the value")
			}
			if !strings.Contains(hostErr.Error(), repositoryErr.Error()) {
				t.Errorf("the repository error\n  %v\nis not the error of the Host file\n  %v", repositoryErr, hostErr)
			}
		})
	}
}

func TestWithRepository_ReportsEveryProblemTogether(t *testing.T) {
	_, err := hostSettings(t, "").WithRepository([]byte("max_review_rounds = 0\nwork_dir = \"/tmp\"\nnonsense = 1\n"))
	if err == nil {
		t.Fatal("WithRepository accepted the file")
	}
	for _, want := range []string{"max_review_rounds: ", "work_dir: ", "nonsense: "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err does not name %q:\n%v", want, err)
		}
	}
}

func TestWithRepository_SyntaxErrorIsAnError(t *testing.T) {
	_, err := hostSettings(t, "").WithRepository([]byte("max_review_rounds = \n"))
	if err == nil {
		t.Error("WithRepository accepted a file that does not parse")
	}
}

func TestWithRepository_DoesNotChangeTheHostSettings(t *testing.T) {
	host := hostSettings(t, "max_review_rounds = 5\n[roles.implementer]\nmodel = \"opus\"\n")
	s := withRepository(t, host, "max_review_rounds = 2\nmerge_method = \"merge\"\n[roles.implementer]\nmodel = \"sonnet\"\n")

	if host.MaxReviewRounds != 5 || host.MergeMethod != MergeSquash {
		t.Errorf("the Host settings changed: %+v", host)
	}
	if host.Roles[RoleImplementer].Model != "opus" {
		t.Errorf("the Host role changed: %+v", host.Roles[RoleImplementer])
	}
	if s.Roles[RoleImplementer].Model != "sonnet" {
		t.Errorf("the repository role = %+v, want sonnet", s.Roles[RoleImplementer])
	}
}
