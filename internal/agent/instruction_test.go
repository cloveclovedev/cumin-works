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
		// A retry must leave one comment, not two.
		"edit it only when it was written after the last sub-issue closed",
		// The acceptance check reads the pull requests, the follow-up
		// notes of cumin, and the earlier plan summary of the Planner.
		"the pull requests that closed the sub-issues, with their descriptions",
		"the follow-up notes that cumin wrote on the requirement issue",
		"your own earlier plan summary",
		// The App of the Planner cannot read the pull requests API.
		"Read a pull request through the issues API",
		// Only these three write a source; anyone may comment otherwise.
		"is not a source",
		// The Planner must be able to tell who the Owner is.
		"The Owner is the account that adds `cumin/status/ready`",
		"read the timelines of the sub-issues",
		// The last lines of the decision request name the implementation
		// issue; the Planner writes about the requirement issue.
		"Write the requirement issue there instead",
		// The Planner must know which files the Implementer cannot change.
		"`protected_paths` of `.cumin/config.toml`",
		"`.github/workflows/`",
		// The list alone does not say which files it covers.
		"matches that name at any depth, as a file or as a directory",
		// A short answer of the Owner replies to the question in the
		// decision request, which cumin posted.
		"the decision request that cumin posted for you",
		// A blocked run is not run again, so nothing may exist yet.
		"Work out the whole split before you create anything on GitHub",
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
		"A change that the risk criteria makes `risk/high` stands alone",
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

// The Reviewer instruction names its own skills and says what the
// requirement of the Reviewer asks for. It holds no skill of another role.
func TestInstruction_ReviewerNamesItsSkillsAndHoldsItsContract(t *testing.T) {
	text, err := instruction(config.RoleReviewer, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range SkillsOf(config.RoleReviewer) {
		if !strings.Contains(text, "`"+skill.Name+"`") {
			t.Errorf("the Reviewer instruction does not name the skill %s", skill.Name)
		}
	}
	for _, skill := range Skills() {
		if slices.Contains(skill.Roles, config.RoleReviewer) {
			continue
		}
		if strings.Contains(text, "`"+skill.Name+"`") {
			t.Errorf("the Reviewer instruction names the skill %s of another role", skill.Name)
		}
	}
	for _, want := range []string{
		// The two request kinds of the requirement.
		"`review`", "`explain the cause`",
		// One review on the head commit, which cumin checks.
		"Exactly one review for each request, with the pull request review API",
		"Set `commit_id` to that commit",
		"Never submit a review with `COMMENT` only",
		// A retry after an abnormal end must not submit a second review.
		"its summary line names the same round, you already reviewed for this request",
		"When a decision request of yours is newer than your last review",
		// The work directory is read-only for the Reviewer.
		"Do not commit, and do not push",
		"Do not resolve review threads",
		"Do not merge, close, or reopen the pull request",
		// The three reasons for blocked of the requirement.
		"The `risk/*` label of the implementation issue does not match the change",
		"The pull request has almost nothing to do with the implementation issue",
		"An acceptance criterion of the issue is so vague",
		"Do not put the result of the review in the JSON",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the Reviewer instruction does not say: %s", want)
		}
	}
	if strings.Contains(text, "# Template:") {
		t.Error("the Reviewer instruction holds a template of one action; those are skills")
	}
}

// The craft of the Reviewer: round 1 finds as much as it can with the
// review skills of the CLI, and later rounds check only the fixes, so that
// the review ends (the rules that the Owner approved on #157).
func TestInstruction_ReviewerHoldsTheCraftOfItsDiscipline(t *testing.T) {
	text, err := instruction(config.RoleReviewer, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Round 1: find as much as you can",
		"invoke the skill `code-review` with the arguments `high origin/HEAD...HEAD`",
		"invoke the skill `security-review`",
		"no `--comment`, no `--fix`, no `ultra`",
		"read the code and confirm the problem yourself",
		"When your CLI has no such skill, or a skill fails, check the same points yourself",
		"Round 2 and later: check the fixes",
		"`git diff <last reviewed commit>..HEAD`",
		"A new blocking comment is allowed only for wrong behavior or a security problem inside that diff",
		"Do not run the review skills again",
		"An acceptance criterion of the issue that is not met",
		"Does the change touch authentication, cryptography, or sessions?",
		"Format and naming rules: do not review them",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the Reviewer instruction does not say: %s", want)
		}
	}
	// The craft stays out of the contract, and the contract out of the
	// craft.
	role, err := roles.File(config.RoleReviewer)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(role, "security-review") || strings.Contains(role, "The six security questions") {
		t.Error("the Reviewer role file holds the craft of its discipline")
	}
	discipline, ok := disciplineFile(t, config.RoleReviewer)
	if !ok {
		t.Fatal("the Reviewer has no discipline file")
	}
	if strings.Contains(discipline, "commit_id") || strings.Contains(discipline, "`cumin-") {
		t.Error("the Reviewer discipline file holds the contract with cumin")
	}
}
