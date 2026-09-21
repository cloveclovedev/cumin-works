package github_test

import (
	"strings"
	"testing"
)

// protectedPathMatches reports if the path matches one entry of a list of
// protected paths. It mirrors the rules of the protected-path workflow
// (scripts/setup-repo/protected-paths.yml, tested by test_protected_paths.py):
// a name with no "/" matches at any depth; an entry with a leading "/" or an
// inner "/" matches only from the root of the repository; an entry with a
// trailing "/" is a directory and protects what is below it. The comparison
// ignores case. The live tests use it to check the fixtures of the sandbox
// before they create anything.
func protectedPathMatches(entries []string, path string) bool {
	path = strings.ToLower(path)
	parts := strings.Split(path, "/")
	for _, entry := range entries {
		entry = strings.ToLower(entry)
		name := strings.Trim(entry, "/")
		if name == "" {
			continue
		}
		directory := strings.HasSuffix(entry, "/")
		anchored := strings.HasPrefix(entry, "/") || strings.Contains(name, "/")
		if anchored {
			if (path == name && !directory) || strings.HasPrefix(path, name+"/") {
				return true
			}
			continue
		}
		for i, part := range parts {
			if part == name && (!directory || i < len(parts)-1) {
				return true
			}
		}
	}
	return false
}

// The rows come from MatchingRules.test_table in
// scripts/setup-repo/test_protected_paths.py.
func TestProtectedPathMatches(t *testing.T) {
	cases := []struct {
		entry, path string
		want        bool
	}{
		{"CLAUDE.md", "CLAUDE.md", true},
		{"CLAUDE.md", "sub/dir/CLAUDE.md", true},
		{"CLAUDE.md", "sub/claude.md", true},
		{"CLAUDE.md", "CLAUDE.md/notes.txt", true},
		{"CLAUDE.md", "docs/CLAUDE.md.bak", false},
		{"CLAUDE.md", "docs/MY-CLAUDE.md", false},
		{".claude/", ".claude/settings.json", true},
		{".claude/", "app/.claude/skills/x/SKILL.md", true},
		{".claude/", "app/.CLAUDE/settings.json", true},
		{".claude/", ".claude", false},
		{".claude/", "docs/.claude.md", false},
		{".cumin/", ".cumin/config.toml", true},
		{"/docs/requirements/", "docs/requirements/overview.md", true},
		{"/docs/requirements/", "sub/docs/requirements/overview.md", false},
		{"/CLAUDE.md", "CLAUDE.md", true},
		{"/CLAUDE.md", "sub/CLAUDE.md", false},
		{"/CLAUDE.md", "live/CLAUDE.md", false},
		{"docs/requirements/", "docs/requirements/a/b.md", true},
		{"docs/requirements/", "sub/docs/requirements/b.md", false},
		{"docs/requirements/", "docs/requirements", false},
		{"docs/requirements", "docs/requirements/b.md", true},
		{"docs/requirements", "docs/requirements", true},
		{"docs/requirements", "docs/requirements-old/b.md", false},
		{"CLAUDE.md", "src/main.go", false},
	}
	for _, c := range cases {
		if got := protectedPathMatches([]string{c.entry}, c.path); got != c.want {
			t.Errorf("entry %q, path %q: got %v, want %v", c.entry, c.path, got, c.want)
		}
	}
	// The default list of the workflow, with the two files of the live checks.
	defaults := []string{".cumin/", "CLAUDE.md", "AGENTS.md", ".claude/"}
	if !protectedPathMatches(defaults, "live/CLAUDE.md") || protectedPathMatches(defaults, "live/20260921-120000.md") {
		t.Error("the default list does not fit the live checks")
	}
}
