package roles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/disciplines"
	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/templates"
)

func TestInstructionExistsForEveryRole(t *testing.T) {
	for _, role := range []config.Role{config.RolePlanner, config.RoleImplementer, config.RoleReviewer} {
		text, err := Instruction(role)
		if err != nil {
			t.Errorf("Instruction(%s): %v", role, err)
			continue
		}
		if !strings.HasPrefix(text, "# ") || strings.Contains(text, "**") {
			t.Errorf("Instruction(%s) must start with a heading and must not use bold text:\n%s", role, text)
		}
	}
}

func TestInstructionRejectsUnknownRole(t *testing.T) {
	if _, err := Instruction("tester"); err == nil {
		t.Error("Instruction(tester) succeeded, want an error")
	}
	// A role name must not escape the embedded directory.
	if _, err := Instruction("../roles/implementer"); err == nil {
		t.Error("Instruction with a path succeeded, want an error")
	}
	// A file name that is not a role is not an instruction either.
	if _, err := Instruction(config.Role(strings.TrimSuffix(RiskCriteriaFile, ".md"))); err == nil {
		t.Error("Instruction(risk-criteria) succeeded, want an error")
	}
}

// The Implementer instruction holds the writing rules, read from
// templates/, names its three skills, and says what the requirement of
// the Implementer asks for.
func TestInstruction_ImplementerHoldsTheWritingRulesAndNamesItsSkills(t *testing.T) {
	text, err := Instruction(config.RoleImplementer)
	if err != nil {
		t.Fatal(err)
	}
	rules, err := templates.Read("writing-rules.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, rules) {
		t.Error("the Implementer instruction does not hold the writing rules as they are")
	}
	for _, skill := range Skills() {
		if !strings.Contains(text, "`"+skill.Name+"`") {
			t.Errorf("the Implementer instruction does not name the skill %s", skill.Name)
		}
	}
	for _, want := range []string{
		"under \"Follow-up\" in the pull request description, and nowhere else",
		"needs a change to a protected path or to `.github/workflows`, stop and return `blocked`",
		"only useful, not needed, write it under \"Follow-up\"",
		"`done` only means that cumin may start to check",
		"`Closes #<issue number>`",
		"Never open a second pull request",
		"Do not wait for checks or for reviews",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the Implementer instruction does not say: %s", want)
		}
	}
	if strings.Contains(text, "# Template:") {
		t.Error("the Implementer instruction holds a template of one action; those are skills")
	}
}

// The instruction of every role ends with the writing rules, whether or
// not the discipline of the role has a file.
func TestInstruction_EveryRoleEndsWithTheWritingRules(t *testing.T) {
	rules, err := templates.Read(writingRules)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []config.Role{config.RolePlanner, config.RoleImplementer, config.RoleReviewer} {
		text, err := Instruction(role)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(text, partSeparator+rules) {
			t.Errorf("Instruction(%s) does not end with the writing rules", role)
		}
	}
}

// A role whose discipline has no file gets the role file and the writing
// rules, and no error. The instruction is those two parts and nothing
// else.
func TestInstruction_ARoleWithoutADisciplineFileIsTheRoleAndTheRules(t *testing.T) {
	rules, err := templates.Read(writingRules)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []config.Role{config.RolePlanner, config.RoleImplementer, config.RoleReviewer} {
		if _, ok, err := disciplines.Role(string(role)); err != nil {
			t.Fatal(err)
		} else if ok {
			continue // A discipline file of this role is tested below.
		}
		text, err := Instruction(role)
		if err != nil {
			t.Fatalf("Instruction(%s): %v", role, err)
		}
		file, err := files.ReadFile(string(role) + ".md")
		if err != nil {
			t.Fatal(err)
		}
		if want := string(file) + partSeparator + rules; text != want {
			t.Errorf("Instruction(%s) is not the role file and the writing rules", role)
		}
	}
}

// A role whose discipline has a file gets the role file, then that file,
// then the writing rules, in that order.
func TestInstruction_ADisciplineFileStandsBetweenTheRoleAndTheRules(t *testing.T) {
	rules, err := templates.Read(writingRules)
	if err != nil {
		t.Fatal(err)
	}
	var tested int
	for _, role := range []config.Role{config.RolePlanner, config.RoleImplementer, config.RoleReviewer} {
		discipline, ok, err := disciplines.Role(string(role))
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			continue
		}
		tested++
		text, err := Instruction(role)
		if err != nil {
			t.Fatalf("Instruction(%s): %v", role, err)
		}
		file, err := files.ReadFile(string(role) + ".md")
		if err != nil {
			t.Fatal(err)
		}
		if want := string(file) + partSeparator + discipline + partSeparator + rules; text != want {
			t.Errorf("Instruction(%s) is not the role file, the discipline file, and the writing rules", role)
		}
	}
	t.Logf("%d of the three roles have a discipline file", tested)
}

// The craft of software engineering reaches the Implementer through the
// file of its discipline. These sentences stood in roles/implementer.md
// before the split, and the composed instruction still holds them.
func TestInstruction_ImplementerHoldsTheCraftOfItsDiscipline(t *testing.T) {
	text, err := Instruction(config.RoleImplementer)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"The tests, the build, and the lint of the repository pass in the work directory",
		`Run the commands under "How to verify" in the issue`,
		"Write commit messages in the Conventional Commits form",
		"Write the title of the pull request in the Conventional Commits form",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the Implementer instruction does not say: %s", want)
		}
	}
}

// Every role file says that a discipline only adds to it, so that a
// discipline cannot weaken a rule of the contract with cumin.
func TestRoleFiles_SayThatTheRoleWinsOverItsDiscipline(t *testing.T) {
	for _, role := range []config.Role{config.RolePlanner, config.RoleImplementer, config.RoleReviewer} {
		data, err := files.ReadFile(string(role) + ".md")
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			"A discipline adds to this file and never weakens a rule of it",
			"If the two disagree, this file wins",
		} {
			if !strings.Contains(string(data), want) {
				t.Errorf("%s.md does not say: %s", role, want)
			}
		}
	}
}

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

// The templates that cumin writes itself (the follow-up note and the stop
// note) must reach no agent: they are the form of a comment of cumin, not
// of an agent, and an agent that read them could imitate them on GitHub.
func TestTemplatesOfCumin_ReachNoAgent(t *testing.T) {
	cuminOnly := []string{"follow-up-note.md", "stop-note.md"}
	for _, name := range cuminOnly {
		text, err := templates.Read(name)
		if err != nil {
			t.Fatalf("Read(%s): %v", name, err)
		}
		for _, skill := range Skills() {
			if skill.Template == name {
				t.Errorf("the skill %s carries %s", skill.Name, name)
			}
		}
		for _, role := range config.AllRoles() {
			instruction, err := Instruction(role)
			if err != nil {
				t.Fatalf("Instruction(%s): %v", role, err)
			}
			if strings.Contains(instruction, text) {
				t.Errorf("the instruction of %s holds %s", role, name)
			}
		}
	}
}
