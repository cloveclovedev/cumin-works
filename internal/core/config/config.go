// Package config loads the Host settings of cumin from a TOML file.
//
// The settings, defaults, and limits come from the settings table in
// docs/ja/requirements/cumin-core.md. docs/ja/development/configuration.md
// lists the keys.
package config

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Defaults and limits from the settings table.
const (
	defaultPollInterval        = 60 * time.Second
	defaultMaxIssuesInProgress = 1
	defaultMaxReviewRounds     = 3
	defaultMaxCheckFixRequests = 3
	defaultAgentTimeLimit      = 50 * time.Minute
	// A GitHub App installation token expires one hour after it is issued.
	maxAgentTimeLimit = 55 * time.Minute
)

// Role is an agent role.
type Role string

const (
	RoleChiefEngineer Role = "chief-engineer"
	RoleImplementer   Role = "implementer"
	RoleReviewer      Role = "reviewer"
)

// AppCuminCore is the name of the GitHub App of cumin itself in the
// github_apps table. The other names are the agent roles.
const AppCuminCore = "cumin-core"

// CLIClaudeCode is the only agent CLI that v0.1 supports.
const CLIClaudeCode = "claude-code"

// defaultCLIPath is the executable of the CLI, found on PATH.
const defaultCLIPath = "claude"

// MergeMethod is how cumin merges a pull request.
type MergeMethod string

const (
	MergeSquash MergeMethod = "squash"
	MergeMerge  MergeMethod = "merge"
	MergeRebase MergeMethod = "rebase"
)

// Settings holds the Host settings after defaults and limit checks.
type Settings struct {
	Repositories        []Repository
	PollInterval        time.Duration
	MaxIssuesInProgress int // for each repository
	WorkDir             string
	MaxReviewRounds     int
	MaxCheckFixRequests int
	MergeMethod         MergeMethod
	// RequestCommand is the executable that a request runs, with the
	// repository and the issue number as arguments. Empty runs nothing. It
	// stands in until the agent start is connected to the poll.
	RequestCommand string
	Roles          map[Role]RoleSettings
	Quota          QuotaSettings
	// GitHubApps maps an organization to the Client ID of each GitHub App.
	// The inner key is AppCuminCore or a Role. `cumin setup` writes the
	// table, so it can be empty.
	GitHubApps map[string]map[string]string
}

// Repository is a target repository.
type Repository struct {
	Owner string
	Name  string
}

func (r Repository) String() string { return r.Owner + "/" + r.Name }

// RoleSettings holds the settings of one agent role.
type RoleSettings struct {
	TimeLimit time.Duration
	CLI       string
	// CLIPath is the executable of the CLI. A relative name is found on
	// PATH. Tests point it at a fake CLI.
	CLIPath string
	Model   string // empty means the default model of the CLI
}

// DefaultPath returns the default path of the Host settings file.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find the home directory: %w", err)
	}
	return filepath.Join(home, ".config", "cumin", "config.toml"), nil
}

// duration accepts only a string such as "60s". A bare TOML integer is an
// error, because it has no unit.
type duration time.Duration

func (d *duration) UnmarshalText(text []byte) error {
	v, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	*d = duration(v)
	return nil
}

// file mirrors the TOML file. Load fills it with the defaults before it
// decodes, so a key that is not in the file keeps its default.
type file struct {
	Repositories        []string `toml:"repositories"`
	PollInterval        duration `toml:"poll_interval"`
	MaxIssuesInProgress int      `toml:"max_issues_in_progress"`
	WorkDir             string   `toml:"work_dir"`
	MaxReviewRounds     int      `toml:"max_review_rounds"`
	MaxCheckFixRequests int      `toml:"max_check_fix_requests"`
	MergeMethod         string   `toml:"merge_method"`
	RequestCommand      string   `toml:"request_command"`
	Roles               struct {
		ChiefEngineer fileRole `toml:"chief-engineer"`
		Implementer   fileRole `toml:"implementer"`
		Reviewer      fileRole `toml:"reviewer"`
	} `toml:"roles"`
	Quota      fileQuota                    `toml:"quota"`
	GitHubApps map[string]map[string]string `toml:"github_apps"`
}

type fileRole struct {
	TimeLimit duration `toml:"time_limit"`
	CLI       string   `toml:"cli"`
	CLIPath   string   `toml:"cli_path"`
	Model     string   `toml:"model"`
}

func defaults() file {
	role := fileRole{TimeLimit: duration(defaultAgentTimeLimit), CLI: CLIClaudeCode, CLIPath: defaultCLIPath}
	f := file{
		PollInterval:        duration(defaultPollInterval),
		MaxIssuesInProgress: defaultMaxIssuesInProgress,
		MaxReviewRounds:     defaultMaxReviewRounds,
		MaxCheckFixRequests: defaultMaxCheckFixRequests,
		MergeMethod:         string(MergeSquash),
		Quota:               defaultQuota(),
	}
	f.Roles.ChiefEngineer, f.Roles.Implementer, f.Roles.Reviewer = role, role, role
	return f
}

// Load reads the settings file at path. Each error names the key, or the
// path when the file cannot be read.
func Load(path string) (*Settings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read the settings file: %w", err)
	}

	f := defaults()
	md, err := toml.Decode(string(data), &f)
	if err != nil {
		// The parser stops at the first syntax or type error. Its message
		// has the line and the full key.
		return nil, fmt.Errorf("settings file %s: %w", path, err)
	}
	// Report unknown keys and invalid values together.
	var errs []error
	for _, key := range md.Undecoded() {
		errs = append(errs, fmt.Errorf("%s: unknown key", key))
	}
	s, err := f.settings()
	if err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("settings file %s:\n%w", path, errors.Join(errs...))
	}
	return s, nil
}

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// settings checks the limits and converts the file to Settings. It reports
// every problem, not only the first.
func (f file) settings() (*Settings, error) {
	var errs []error
	fail := func(key, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%s: %s", key, fmt.Sprintf(format, args...)))
	}

	s := &Settings{
		PollInterval:        time.Duration(f.PollInterval),
		MaxIssuesInProgress: f.MaxIssuesInProgress,
		MaxReviewRounds:     f.MaxReviewRounds,
		MaxCheckFixRequests: f.MaxCheckFixRequests,
		MergeMethod:         MergeMethod(f.MergeMethod),
		RequestCommand:      f.RequestCommand,
		Roles:               map[Role]RoleSettings{},
		GitHubApps:          f.GitHubApps,
	}

	if len(f.Repositories) == 0 {
		fail("repositories", "is required: list one target repository or more")
	}
	seen := map[string]bool{}
	for i, name := range f.Repositories {
		key := fmt.Sprintf("repositories[%d]", i)
		if !repositoryPattern.MatchString(name) {
			fail(key, "%q must have the form <owner>/<repo>", name)
			continue
		}
		// GitHub does not distinguish upper case and lower case in names.
		lower := strings.ToLower(name)
		if seen[lower] {
			fail(key, "%q is listed more than once", name)
			continue
		}
		seen[lower] = true
		owner, repo, _ := strings.Cut(name, "/")
		s.Repositories = append(s.Repositories, Repository{Owner: owner, Name: repo})
	}

	if s.PollInterval <= 0 {
		fail("poll_interval", "must be more than 0")
	}
	if s.MaxIssuesInProgress < 1 {
		fail("max_issues_in_progress", "must be 1 or more")
	}
	if s.MaxReviewRounds < 1 {
		fail("max_review_rounds", "must be 1 or more")
	}
	if s.MaxCheckFixRequests < 1 {
		fail("max_check_fix_requests", "must be 1 or more")
	}
	switch s.MergeMethod {
	case MergeSquash, MergeMerge, MergeRebase:
	default:
		fail("merge_method", "%q must be one of squash, merge, rebase", f.MergeMethod)
	}

	workDir, err := expandHome(f.WorkDir)
	switch {
	case f.WorkDir == "":
		fail("work_dir", "is required")
	case err != nil:
		fail("work_dir", "%v", err)
	}
	s.WorkDir = workDir

	roles := []struct {
		role Role
		file fileRole
	}{
		{RoleChiefEngineer, f.Roles.ChiefEngineer},
		{RoleImplementer, f.Roles.Implementer},
		{RoleReviewer, f.Roles.Reviewer},
	}
	for _, r := range roles {
		key := "roles." + string(r.role)
		limit := time.Duration(r.file.TimeLimit)
		if limit <= 0 || limit > maxAgentTimeLimit {
			fail(key+".time_limit", "must be more than 0, and %d minutes or less", int(maxAgentTimeLimit.Minutes()))
		}
		if r.file.CLI != CLIClaudeCode {
			fail(key+".cli", "%q is not supported: use %q", r.file.CLI, CLIClaudeCode)
		}
		if r.file.CLIPath == "" {
			fail(key+".cli_path", "must not be empty")
		}
		s.Roles[r.role] = RoleSettings{TimeLimit: limit, CLI: r.file.CLI, CLIPath: r.file.CLIPath, Model: r.file.Model}
	}

	s.Quota = f.Quota.settings(fail)

	// Sorted, so that the same file always gives the same error text.
	for _, org := range slices.Sorted(maps.Keys(f.GitHubApps)) {
		apps := f.GitHubApps[org]
		for _, app := range slices.Sorted(maps.Keys(apps)) {
			clientID := apps[app]
			key := "github_apps." + org + "." + app
			switch app {
			case AppCuminCore, string(RoleChiefEngineer), string(RoleImplementer), string(RoleReviewer):
			default:
				fail(key, "unknown GitHub App name")
				continue
			}
			if clientID == "" {
				fail(key, "must not be empty")
			}
		}
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return s, nil
}

// expandHome replaces a leading "~/" with the home directory.
func expandHome(path string) (string, error) {
	rest, ok := strings.CutPrefix(path, "~/")
	if !ok {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find the home directory: %w", err)
	}
	return filepath.Join(home, rest), nil
}
