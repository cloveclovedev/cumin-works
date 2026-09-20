package roles

import (
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
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
