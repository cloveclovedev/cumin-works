package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const settingsWithComments = `# Host settings of cumin. A person edits this file.
repositories = ["example-org/example-repo"]  # the target
work_dir = "/tmp/cumin-work"

[roles.implementer]
time_limit = "45m"   # shorter than the default

[github_apps.other-org]
cumin-core = "Iv23liOTHER"

# The quota comes last.
[quota.five_hour]
threshold = 70
`

func readFile(t *testing.T, path string) string {
	t.Helper()
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

func TestSetGitHubAppClientID_KeepsEveryOtherLine(t *testing.T) {
	path := writeFile(t, settingsWithComments)

	for _, c := range []struct{ app, clientID string }{
		{"cumin-core", "Iv23liCORE"},
		{"chief-engineer", "Iv23liCHIEF"},
		{"implementer", "Iv23liIMPL"},
		{"reviewer", "Iv23liREV"},
	} {
		if err := SetGitHubAppClientID(path, "example-org", c.app, c.clientID); err != nil {
			t.Fatalf("SetGitHubAppClientID(%s): %v", c.app, err)
		}
	}

	got := readFile(t, path)
	want := settingsWithComments + `
[github_apps.example-org]
cumin-core = "Iv23liCORE"
chief-engineer = "Iv23liCHIEF"
implementer = "Iv23liIMPL"
reviewer = "Iv23liREV"
`
	if got != want {
		t.Errorf("the file is:\n%s\nwant:\n%s", got, want)
	}

	// The whole file still loads, with the old and the new values.
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.GitHubApps["example-org"]["reviewer"] != "Iv23liREV" || s.GitHubApps["other-org"]["cumin-core"] != "Iv23liOTHER" {
		t.Errorf("GitHubApps = %v", s.GitHubApps)
	}
	if s.Roles[RoleImplementer].TimeLimit.String() != "45m0s" {
		t.Errorf("another setting changed: %v", s.Roles[RoleImplementer].TimeLimit)
	}
}

func TestSetGitHubAppClientID_ReplacesAndInsertsInsideAnExistingTable(t *testing.T) {
	path := writeFile(t, `work_dir = "/tmp/w"

[github_apps.example-org]   # registered by cumin setup
cumin-core = "Iv23liOLD"  # old value
reviewer = "Iv23liREV"

[quota.weekly]
threshold = 60
`)
	if err := SetGitHubAppClientID(path, "example-org", "cumin-core", "Iv23liNEW"); err != nil {
		t.Fatal(err)
	}
	if err := SetGitHubAppClientID(path, "example-org", "implementer", "Iv23liIMPL"); err != nil {
		t.Fatal(err)
	}
	want := `work_dir = "/tmp/w"

[github_apps.example-org]   # registered by cumin setup
cumin-core = "Iv23liNEW"
reviewer = "Iv23liREV"
implementer = "Iv23liIMPL"

[quota.weekly]
threshold = 60
`
	if got := readFile(t, path); got != want {
		t.Errorf("the file is:\n%s\nwant:\n%s", got, want)
	}
}

func TestSetGitHubAppClientID_CreatesTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cumin", "config.toml")
	if err := SetGitHubAppClientID(path, "example-org", "cumin-core", "Iv23liCORE"); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, path), "[github_apps.example-org]\ncumin-core = \"Iv23liCORE\"\n"; got != want {
		t.Errorf("the file is %q, want %q", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestSetGitHubAppClientID_RefusesAFileThatItCannotChangeSafely(t *testing.T) {
	for name, content := range map[string]string{
		"github_apps as an inline table": "github_apps = { example-org = { reviewer = \"Iv23liREV\" } }\n",
		"github_apps with dotted keys":   "github_apps.example-org.reviewer = \"Iv23liREV\"\n",
		"a file that is not TOML":        "work_dir = \n",
	} {
		t.Run(name, func(t *testing.T) {
			path := writeFile(t, content)
			err := SetGitHubAppClientID(path, "example-org", "cumin-core", "Iv23liCORE")
			if err == nil {
				t.Fatal("no error")
			}
			if !strings.Contains(err.Error(), `cumin-core = "Iv23liCORE"`) {
				t.Errorf("err = %v, want the line to write by hand", err)
			}
			if got := readFile(t, path); got != content {
				t.Errorf("the file changed: %q", got)
			}
		})
	}
}

func TestSetGitHubAppClientID_RejectsWrongNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	for name, c := range map[string]struct{ org, app, clientID string }{
		"unknown App":          {"example-org", "owner", "Iv23li"},
		"organization":         {"example/org", "reviewer", "Iv23li"},
		"client ID with quote": {"example-org", "reviewer", "Iv23\"li"},
		"empty client ID":      {"example-org", "reviewer", ""},
	} {
		if err := SetGitHubAppClientID(path, c.org, c.app, c.clientID); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("a rejected call created the file")
	}
}

func TestReadGitHubApps(t *testing.T) {
	apps, err := ReadGitHubApps(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil || len(apps) != 0 {
		t.Errorf("missing file: apps = %v, err = %v", apps, err)
	}

	// The other settings can be incomplete while cumin setup runs.
	path := writeFile(t, "[github_apps.example-org]\nreviewer = \"Iv23liREV\"\n")
	apps, err = ReadGitHubApps(path)
	if err != nil || apps["example-org"]["reviewer"] != "Iv23liREV" {
		t.Errorf("apps = %v, err = %v", apps, err)
	}

	if _, err := ReadGitHubApps(writeFile(t, "work_dir = \n")); err == nil {
		t.Error("invalid TOML: no error")
	}
}
