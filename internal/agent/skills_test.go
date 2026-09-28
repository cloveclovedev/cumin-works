package agent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/templates"
)

// WriteSkills writes one SKILL.md for each skill, with a frontmatter and
// the template as the body, overwrites an older file, and removes a skill
// that the binary no longer has.
func TestWriteSkills_WritesEachTemplateAsASkill(t *testing.T) {
	root := t.TempDir()
	dir := SkillDir(root, config.RoleImplementer)
	for _, name := range []string{"cumin-pull-request", "cumin-obsolete"} {
		stale := SkillPath(dir, name)
		if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := WriteSkills(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(SkillPath(dir, "cumin-obsolete")); !os.IsNotExist(err) {
		t.Errorf("the obsolete skill is still there (err = %v)", err)
	}
	if got, want := len(SkillsOf(config.RoleImplementer)), 3; got != want {
		t.Errorf("%d skills of the Implementer, want %d", got, want)
	}
	for _, skill := range SkillsOf(config.RoleImplementer) {
		data, err := os.ReadFile(SkillPath(dir, skill.Name))
		if err != nil {
			t.Errorf("skill %s: %v", skill.Name, err)
			continue
		}
		text := string(data)
		body, err := templates.Read(skill.Template)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(text, "---\nname: "+skill.Name+"\ndescription: "+skill.Description+"\n---\n") {
			t.Errorf("skill %s has no frontmatter with its name and description:\n%s", skill.Name, text[:min(len(text), 200)])
		}
		if !strings.HasSuffix(text, body) {
			t.Errorf("skill %s does not end with the template %s", skill.Name, skill.Template)
		}
		if strings.Contains(text, "**") {
			t.Errorf("skill %s uses bold text", skill.Name)
		}
	}
}

// Each role gets a directory of its own, and it holds only the skills of
// that role. A role is not offered a skill of another role, so that a
// skill cannot invite an action that the role must not take.
func TestWriteSkills_WritesOneDirectoryForEachRole(t *testing.T) {
	root := t.TempDir()
	if err := WriteSkills(root); err != nil {
		t.Fatal(err)
	}
	for _, role := range config.AllRoles() {
		dir := SkillDir(root, role)
		want := SkillNamesOf(role)
		if len(want) == 0 {
			t.Errorf("the role %s has no skill", role)
		}
		entries, err := os.ReadDir(filepath.Join(dir, ".claude", "skills"))
		if err != nil {
			t.Fatalf("read the skills of %s: %v", role, err)
		}
		var got []string
		for _, entry := range entries {
			got = append(got, entry.Name())
		}
		slices.Sort(got)
		sortedWant := slices.Clone(want)
		slices.Sort(sortedWant)
		if !slices.Equal(got, sortedWant) {
			t.Errorf("the directory of %s holds %v, want %v", role, got, sortedWant)
		}
	}
	// Every role writes a decision request; only the Implementer writes a
	// pull request description and a reply to a review comment.
	for _, role := range config.AllRoles() {
		if !slices.Contains(SkillNamesOf(role), "cumin-decision-request") {
			t.Errorf("the role %s has no cumin-decision-request", role)
		}
	}
	if names := SkillNamesOf(config.RolePlanner); slices.Contains(names, "cumin-pull-request") {
		t.Errorf("the Planner has cumin-pull-request: %v", names)
	}
}

// WriteSkills writes the directories again at every start, so that a
// skill which changed roles does not stay with the old one.
func TestWriteSkills_RemovesTheDirectoryOfEveryRole(t *testing.T) {
	root := t.TempDir()
	stale := SkillPath(SkillDir(root, config.RolePlanner), "cumin-pull-request")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteSkills(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("the skill of another role is still in the directory of the Planner (err = %v)", err)
	}
}
