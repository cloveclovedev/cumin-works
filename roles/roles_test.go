package roles

import (
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/templates"
)

func TestInstructionExistsForEveryRole(t *testing.T) {
	for _, role := range []config.Role{config.RoleChiefEngineer, config.RoleImplementer, config.RoleReviewer} {
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
}

// The Implementer instruction holds its templates, read from templates/,
// and the statements that the requirement of the Implementer asks for.
func TestInstruction_ImplementerHoldsItsTemplatesAndRules(t *testing.T) {
	text, err := Instruction(config.RoleImplementer)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"writing-rules.md", "pull-request.md", "review-reply.md", "decision-request.md"} {
		want, err := templates.Read(name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(text, want) {
			t.Errorf("the Implementer instruction does not hold the template %s as it is", name)
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
	if strings.Count(text, "# Templates\n") != 1 {
		t.Error("the Implementer instruction must have one heading \"# Templates\"")
	}
}

// The other roles have no templates yet; their instruction is the file.
func TestInstruction_OtherRolesHaveNoTemplates(t *testing.T) {
	for _, role := range []config.Role{config.RoleChiefEngineer, config.RoleReviewer} {
		text, err := Instruction(role)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(text, "# Templates") || strings.Contains(text, "# Template:") {
			t.Errorf("Instruction(%s) holds templates, want none", role)
		}
	}
}
