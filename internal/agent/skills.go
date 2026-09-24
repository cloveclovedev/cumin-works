package agent

// This file turns the templates of GitHub text into skills of Claude Code.
// cumin run writes the skills once at start, one directory for each role,
// under the state directory of the Host. An agent start passes the
// directory of its own role with --add-dir, so that a role is offered only
// the skills of its own work. The agent loads a skill right before the
// action that needs it, instead of carrying every template in its system
// prompt. docs/ja/designs/agent-run.md, the topic on the start of Claude
// Code, records the decision.

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/templates"
)

// Skill is one skill that cumin writes for an agent: the body is a
// template file, as it is; the name, the description, and the roles that
// receive it come from here.
type Skill struct {
	Name        string
	Description string
	// Template is the file in templates/ that is the body of the skill.
	Template string
	// Roles are the roles that receive the skill. A role is offered only
	// the skills of its own work, so that a skill cannot invite an action
	// that the role must not take.
	Roles []config.Role
}

// skills are the skills of the agents. The directory of a role holds only
// its own, and the names carry the prefix "cumin-" so that a skill of
// cumin is told from a skill of the repository.
var skills = []Skill{
	{
		Name:        "cumin-pull-request",
		Description: "Write or update the description of a pull request in the form that cumin reads. Use before gh pr create and before gh pr edit.",
		Template:    "pull-request.md",
		Roles:       []config.Role{config.RoleImplementer},
	},
	{
		Name:        "cumin-review-reply",
		Description: "Reply to a review comment with one of the statuses Fixed, Not changed, Deferred, or Answer. Use before you reply to a review comment.",
		Template:    "review-reply.md",
		Roles:       []config.Role{config.RoleImplementer},
	},
	{
		Name:        "cumin-decision-request",
		Description: "Write a decision request for the Owner. Use before you return the result blocked, to write blocked_reason.",
		Template:    "decision-request.md",
		Roles:       config.AllRoles(),
	},
}

// Skills returns every skill that cumin has, as a copy: the roles of a
// skill decide which directory holds it and what the start record must
// list, so a caller must not be able to change them.
func Skills() []Skill {
	return copySkills(skills)
}

// SkillsOf returns the skills of one role, in the order of the list.
func SkillsOf(role config.Role) []Skill {
	var of []Skill
	for _, skill := range skills {
		if slices.Contains(skill.Roles, role) {
			of = append(of, skill)
		}
	}
	return copySkills(of)
}

// copySkills copies the list and the roles of each skill.
func copySkills(list []Skill) []Skill {
	out := make([]Skill, 0, len(list))
	for _, skill := range list {
		skill.Roles = slices.Clone(skill.Roles)
		out = append(out, skill)
	}
	return out
}

// SkillNamesOf returns the names of the skills of one role, which is what
// the start record of a run must list (claudecode.go).
func SkillNamesOf(role config.Role) []string {
	var names []string
	for _, skill := range SkillsOf(role) {
		names = append(names, skill.Name)
	}
	return names
}

// SkillDir is the directory of one role under root: the directory that the
// start of that role passes with --add-dir.
func SkillDir(root string, role config.Role) string {
	return filepath.Join(root, string(role))
}

// SkillPath is the path of the skill file under the directory of a role:
// <dir>/.claude/skills/<name>/SKILL.md, where Claude Code finds the skills
// of a directory passed with --add-dir (official: Skills).
func SkillPath(dir, name string) string {
	return filepath.Join(dir, ".claude", "skills", name, "SKILL.md")
}

// WriteSkills removes the directory of each role under root and writes the
// skills of that role again, so that the directories always match the
// binary, also after a skill was renamed, removed, or given to another
// role. root is the directory that holds one directory for each role.
func WriteSkills(root string) error {
	for _, role := range config.AllRoles() {
		dir := SkillDir(root, role)
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("write the skills of %s: %w", role, err)
		}
		for _, skill := range SkillsOf(role) {
			body, err := templates.Read(skill.Template)
			if err != nil {
				return fmt.Errorf("write the skill %s: %w", skill.Name, err)
			}
			path := SkillPath(dir, skill.Name)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return fmt.Errorf("write the skill %s: %w", skill.Name, err)
			}
			content := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s", skill.Name, skill.Description, body)
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return fmt.Errorf("write the skill %s: %w", skill.Name, err)
			}
		}
	}
	return nil
}
