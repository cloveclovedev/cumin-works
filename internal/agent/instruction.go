package agent

// This file composes the instruction that an agent receives. The parts
// come from four places and stand in one order: the role file (the
// contract with cumin), the file of the role in its discipline (the
// standards of the field of work), the writing rules, and the risk
// criteria of the target repository. docs/ja/requirements/agents/common.md,
// the section on the composition of the instruction, records the order.
//
// The three text packages read their own Markdown and know nothing of each
// other. This file is the one place that knows where each part comes from,
// so a part that gains a source outside the binary later is added here.
// The risk criteria already has three levels, which the caller resolved
// for the repository; it arrives as data of the start request, next to the
// token and the work directory.

import (
	"fmt"
	"strings"

	"github.com/cloveclovedev/cumin-works/disciplines"
	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/roles"
	"github.com/cloveclovedev/cumin-works/templates"
)

// instructionSeparator stands between the parts of an instruction.
const instructionSeparator = "\n---\n\n"

// instruction returns the instruction of the role, with the risk criteria
// of the repository at the end.
//
// A role whose discipline has no file gets the other parts, and that is
// not an error: a discipline need not have a file for every role. An empty
// riskCriteria is left out, so that a run without a repository behind it
// (a test, the minimal run that reads the quota) carries no empty section.
func instruction(role config.Role, riskCriteria string) (string, error) {
	roleFile, err := roles.File(role)
	if err != nil {
		return "", err
	}
	parts := []string{roleFile}

	discipline, ok, err := disciplines.Role(disciplines.Default, string(role))
	if err != nil {
		return "", fmt.Errorf("instruction for %s: %w", role, err)
	}
	if ok {
		parts = append(parts, discipline)
	}

	rules, err := templates.WritingRules()
	if err != nil {
		return "", fmt.Errorf("instruction for %s: %w", role, err)
	}
	parts = append(parts, rules)

	if strings.TrimSpace(riskCriteria) != "" {
		parts = append(parts, riskCriteria)
	}
	return strings.Join(parts, instructionSeparator), nil
}
