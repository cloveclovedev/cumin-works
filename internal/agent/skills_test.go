package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/templates"
)

// WriteSkills writes one SKILL.md for each skill, with a frontmatter and
// the template as the body, overwrites an older file, and removes a skill
// that the binary no longer has.
func TestWriteSkills_WritesEachTemplateAsASkill(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"cumin-pull-request", "cumin-obsolete"} {
		stale := SkillPath(dir, name)
		if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := WriteSkills(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(SkillPath(dir, "cumin-obsolete")); !os.IsNotExist(err) {
		t.Errorf("the obsolete skill is still there (err = %v)", err)
	}
	if len(Skills()) != 3 {
		t.Errorf("%d skills, want 3", len(Skills()))
	}
	for _, skill := range Skills() {
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
