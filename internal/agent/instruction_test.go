package agent

import (
	"slices"
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/disciplines"
	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/roles"
	"github.com/cloveclovedev/cumin-works/templates"
)

// disciplineFile is the file of the role in the discipline that ships with
// cumin, and whether that discipline has one.
func disciplineFile(t *testing.T, role config.Role) (string, bool) {
	t.Helper()
	text, ok, err := disciplines.Role(disciplines.Default, string(role))
	if err != nil {
		t.Fatal(err)
	}
	return text, ok
}

func writingRules(t *testing.T) string {
	t.Helper()
	rules, err := templates.WritingRules()
	if err != nil {
		t.Fatal(err)
	}
	return rules
}

func TestInstructionExistsForEveryRole(t *testing.T) {
	for _, role := range config.AllRoles() {
		text, err := instruction(role, "")
		if err != nil {
			t.Errorf("instruction(%s): %v", role, err)
			continue
		}
		if !strings.HasPrefix(text, "# ") || strings.Contains(text, "**") {
			t.Errorf("instruction(%s) must start with a heading and must not use bold text:\n%s", role, text)
		}
	}
}

func TestInstructionRejectsUnknownRole(t *testing.T) {
	if _, err := instruction("tester", ""); err == nil {
		t.Error("instruction(tester) succeeded, want an error")
	}
	// A role name must not escape the embedded directory.
	if _, err := instruction("../roles/implementer", ""); err == nil {
		t.Error("instruction with a path succeeded, want an error")
	}
	// A file name that is not a role is not an instruction either.
	if _, err := instruction(config.Role(strings.TrimSuffix(config.RiskCriteriaFile, ".md")), ""); err == nil {
		t.Error("instruction(risk-criteria) succeeded, want an error")
	}
}

// The Implementer instruction holds the writing rules, read from
// templates/, names its three skills, and says what the requirement of
// the Implementer asks for.
func TestInstruction_ImplementerHoldsTheWritingRulesAndNamesItsSkills(t *testing.T) {
	text, err := instruction(config.RoleImplementer, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, writingRules(t)) {
		t.Error("the Implementer instruction does not hold the writing rules as they are")
	}
	for _, skill := range SkillsOf(config.RoleImplementer) {
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

// Without the risk criteria, the instruction of every role ends with the
// writing rules, whether or not the discipline of the role has a file.
func TestInstruction_EveryRoleEndsWithTheWritingRules(t *testing.T) {
	rules := writingRules(t)
	for _, role := range config.AllRoles() {
		text, err := instruction(role, "")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(text, instructionSeparator+rules) {
			t.Errorf("instruction(%s) does not end with the writing rules", role)
		}
	}
}

// A role whose discipline has no file gets the role file and the writing
// rules, and no error. The instruction is those two parts and nothing
// else.
func TestInstruction_ARoleWithoutADisciplineFileIsTheRoleAndTheRules(t *testing.T) {
	rules := writingRules(t)
	for _, role := range config.AllRoles() {
		if _, ok := disciplineFile(t, role); ok {
			continue // A discipline file of this role is tested below.
		}
		text, err := instruction(role, "")
		if err != nil {
			t.Fatalf("instruction(%s): %v", role, err)
		}
		file, err := roles.File(role)
		if err != nil {
			t.Fatal(err)
		}
		if want := file + instructionSeparator + rules; text != want {
			t.Errorf("instruction(%s) is not the role file and the writing rules", role)
		}
	}
}

// A role whose discipline has a file gets the role file, then that file,
// then the writing rules, in that order.
func TestInstruction_ADisciplineFileStandsBetweenTheRoleAndTheRules(t *testing.T) {
	rules := writingRules(t)
	var tested int
	for _, role := range config.AllRoles() {
		discipline, ok := disciplineFile(t, role)
		if !ok {
			continue
		}
		tested++
		text, err := instruction(role, "")
		if err != nil {
			t.Fatalf("instruction(%s): %v", role, err)
		}
		file, err := roles.File(role)
		if err != nil {
			t.Fatal(err)
		}
		if want := file + instructionSeparator + discipline + instructionSeparator + rules; text != want {
			t.Errorf("instruction(%s) is not the role file, the discipline file, and the writing rules", role)
		}
	}
	t.Logf("%d of the three roles have a discipline file", tested)
}

// The risk criteria of the repository is the last part of the instruction,
// as it is, and only when there is one.
func TestInstruction_TheRiskCriteriaOfTheRepositoryStandsLast(t *testing.T) {
	const criteria = "# Risk criteria of this repository\n\nEverything is risk/high.\n"
	for _, role := range config.AllRoles() {
		withOut, err := instruction(role, "")
		if err != nil {
			t.Fatal(err)
		}
		with, err := instruction(role, criteria)
		if err != nil {
			t.Fatal(err)
		}
		if want := withOut + instructionSeparator + criteria; with != want {
			t.Errorf("instruction(%s) with the risk criteria is not the instruction plus the text", role)
		}
		blank, err := instruction(role, " \n\t\n")
		if err != nil {
			t.Fatal(err)
		}
		if blank != withOut {
			t.Errorf("instruction(%s) with a blank risk criteria is not the instruction without one", role)
		}
	}
}

// The craft of software engineering reaches the Implementer through the
// file of its discipline. These sentences stood in roles/implementer.md
// before the split, and the composed instruction still holds them.
func TestInstruction_ImplementerHoldsTheCraftOfItsDiscipline(t *testing.T) {
	text, err := instruction(config.RoleImplementer, "")
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
	for _, role := range config.AllRoles() {
		file, err := roles.File(role)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			"A discipline adds to this file and never weakens a rule of it",
			"If the two disagree, this file wins",
		} {
			if !strings.Contains(file, want) {
				t.Errorf("%s.md does not say: %s", role, want)
			}
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
			composed, err := instruction(role, "")
			if err != nil {
				t.Fatalf("instruction(%s): %v", role, err)
			}
			if strings.Contains(composed, text) {
				t.Errorf("the instruction of %s holds %s", role, name)
			}
		}
	}
}

// The Planner instruction names its own skills and says what the
// requirement of the Planner asks for. It holds no skill of another role:
// a skill in the context of a role invites the action of that skill.
func TestInstruction_PlannerNamesItsSkillsAndHoldsItsContract(t *testing.T) {
	text, err := instruction(config.RolePlanner, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range SkillsOf(config.RolePlanner) {
		if !strings.Contains(text, "`"+skill.Name+"`") {
			t.Errorf("the Planner instruction does not name the skill %s", skill.Name)
		}
	}
	for _, skill := range Skills() {
		if slices.Contains(skill.Roles, config.RolePlanner) {
			continue
		}
		if strings.Contains(text, "`"+skill.Name+"`") {
			t.Errorf("the Planner instruction names the skill %s of another role", skill.Name)
		}
	}
	for _, want := range []string{
		"from the split to the acceptance check",
		"Exactly one `risk/*` label on each implementation issue",
		"The milestone of the requirement issue on each implementation issue",
		"only between sub-issues of the same requirement issue",
		"`cumin/type/owner-task`",
		"`## Acceptance check`",
		"Do not write code",
		"Do not change the body of any issue",
		"read the sub-issues that the requirement issue already has",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the Planner instruction does not say: %s", want)
		}
	}
	if strings.Contains(text, "# Template:") {
		t.Error("the Planner instruction holds a template of one action; those are skills")
	}
}

// The craft of software engineering reaches the Planner through the file of
// its discipline: the sizing questions, the limit on the number of issues,
// and where the risk criteria stands.
func TestInstruction_PlannerHoldsTheCraftOfItsDiscipline(t *testing.T) {
	text, err := instruction(config.RolePlanner, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"One purpose. You can describe the change in one sentence without \"and\"",
		"Do not go over 400 lines or 10 files",
		"Twelve is the limit",
		"The risk criteria of the repository stands at the end of this instruction",
		"take the higher one",
		"What good work looks like",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the Planner instruction does not say: %s", want)
		}
	}
}

// The discipline file of the Implementer holds the two sections that #125
// left open: what good work is, and the reasons of the craft for blocked.
func TestInstruction_ImplementerHoldsGoodWorkAndTheCraftReasonsForBlocked(t *testing.T) {
	text, err := instruction(config.RoleImplementer, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"The change is the smallest one that meets every acceptance criterion",
		"A test fails without your change and passes with it",
		"No command can show that the work is done",
		"already fail on the commit that your branch starts from",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the Implementer instruction does not say: %s", want)
		}
	}
}
