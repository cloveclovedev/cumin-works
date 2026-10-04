package disciplines

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// The built-in risk criteria comes from the directory of the default
// discipline. The test reads the file from disk, so that a move of the
// file fails here and not only where an agent would read the text.
func TestRiskCriteria_ComesFromTheDirectoryOfTheDefaultDiscipline(t *testing.T) {
	text, err := RiskCriteria(Default)
	if err != nil {
		t.Fatal(err)
	}
	onDisk, err := os.ReadFile(filepath.Join(Default, "risk-criteria.md"))
	if err != nil {
		t.Fatalf("read the file of the discipline: %v", err)
	}
	if text != string(onDisk) {
		t.Errorf("RiskCriteria(Default) is not %s/risk-criteria.md", Default)
	}
	if len(text) == 0 {
		t.Error("the built-in risk criteria is empty")
	}
}

// A discipline has a file for some roles and none for others. A role
// without one is not an error: the instruction of that role is then the
// role file and the writing rules (package roles).
func TestRole_TellsAFileOfTheRoleFromNoFile(t *testing.T) {
	fsys := fstest.MapFS{
		"a-discipline/implementer.md": {Data: []byte("# The craft of the Implementer\n")},
	}
	text, ok, err := roleFile(fsys, "a-discipline", "implementer")
	if err != nil || !ok {
		t.Fatalf("roleFile(implementer) = %q, %v, %v; want the file", text, ok, err)
	}
	if text != "# The craft of the Implementer\n" {
		t.Errorf("text = %q, want the file as it is", text)
	}
	if text, ok, err := roleFile(fsys, "a-discipline", "reviewer"); err != nil || ok || text != "" {
		t.Errorf("roleFile(reviewer) = %q, %v, %v; want no file and no error", text, ok, err)
	}
	if _, _, err := roleFile(fsys, "a-discipline", "../a-discipline/implementer"); err == nil {
		t.Error("roleFile with a path succeeded, want an error")
	}
	if _, _, err := roleFile(fsys, "a-discipline", ""); err == nil {
		t.Error("roleFile with an empty role succeeded, want an error")
	}
	if _, _, err := roleFile(fsys, "../a-discipline", "implementer"); err == nil {
		t.Error("roleFile with a path as the discipline succeeded, want an error")
	}
	if _, err := RiskCriteria("../software-engineering"); err == nil {
		t.Error("RiskCriteria with a path succeeded, want an error")
	}
}

// Every role that cumin drives is asked for, so that a typo in a file name
// of the default discipline cannot pass unseen.
func TestRole_AnswersForEveryRoleOfTheDefaultDiscipline(t *testing.T) {
	for _, role := range []string{"planner", "implementer", "reviewer"} {
		text, ok, err := Role(Default, role)
		if err != nil {
			t.Errorf("Role(%s): %v", role, err)
			continue
		}
		if ok && text == "" {
			t.Errorf("Role(%s) found an empty file", role)
		}
		t.Logf("%s: has a file in %s = %v", role, Default, ok)
	}
}

// Every role plans a long check against the limit of its run. The rule
// names the two lines of the start request by their labels (package agent
// writes them), so that the agent finds the limit and the end time there.
// The rule also says what the role does when a long check does not fit:
// stop at the part that fits and report the runs, and return blocked when
// a criterion itself needs more time than the run has.
func TestRole_HoldsTheRuleOnLongChecksForEveryRole(t *testing.T) {
	for _, role := range []string{"planner", "implementer", "reviewer"} {
		text, ok, err := Role(Default, role)
		if err != nil || !ok {
			t.Errorf("Role(%s) = %v, %v; want a file", role, ok, err)
			continue
		}
		for _, want := range []string{
			"## Long checks",
			"Time limit of the run",
			"End time of the run",
			"ends well before",
			"does not end before \"End time of the run\", stop at the part that fits",
			"how many runs of how many",
			"needs a check that is longer than the run",
			"Return `blocked` and write the reason",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("the discipline of the %s does not hold %q", role, want)
			}
		}
	}
}

// A review after an approval covers only the diff since the approved
// commit, at the depth of round 1. The rule names the line of the start
// request by its label (the request of a review holds it), so that the Reviewer
// finds the approved commit there. A request without that line gets the
// full review of round 1.
func TestRole_HoldsTheRuleOnTheApprovedCommitForTheReviewer(t *testing.T) {
	text, ok, err := Role(Default, "reviewer")
	if err != nil || !ok {
		t.Fatalf("Role(reviewer) = %v, %v; want a file", ok, err)
	}
	for _, want := range []string{
		"Approved commit",
		"`git diff <approved commit>..HEAD`",
		"Do not review the approved part again",
		"`high <approved commit>..HEAD`",
		"and invoke the skill `security-review`",
		"against that diff only",
		"`git diff --name-only origin/HEAD...HEAD`",
		"First check that the approved commit is an ancestor of the head commit",
		"When it is not, do the full round 1 review above, and say so in the summary",
		"A request with no `Approved commit` line gets the full round 1 review",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the discipline of the reviewer does not hold %q", want)
		}
	}
}

// The Planner sizes an implementation issue against the time of one run of
// the Implementer and of the Reviewer. The criterion names the two lines of
// the start request by their labels (package agent writes them). Work that
// no split makes fit goes back to the Owner under "Please check", with the
// three choices of the Owner.
func TestRole_HoldsTheCriterionOnTheTimeOfOneRunForThePlanner(t *testing.T) {
	text, ok, err := Role(Default, "planner")
	if err != nil || !ok {
		t.Fatalf("Role(planner) = %v, %v; want a file", ok, err)
	}
	for _, want := range []string{
		"10. It fits in one run.",
		"Time limit of the Implementer",
		"Time limit of the Reviewer",
		"Split it or rewrite it when the answer to one of them is no",
		"Name the work under \"Please check\" of the plan summary",
		"raise `roles.<role>.time_limit`, change the criterion, or drop the work",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the discipline of the planner does not hold %q", want)
		}
	}
	if strings.Contains(text, "a repeated test run") {
		t.Error("the discipline of the planner still holds \"a repeated test run\" as its long check")
	}
}
