package agent

// This file turns the templates of GitHub text into skills of Claude Code.
// cumin run writes the skills once at start into one directory under the
// state directory of the Host, and every agent start passes that directory
// with --add-dir. The agent loads a skill right before the action that
// needs it, instead of carrying every template in its system prompt.
// docs/ja/designs/agent-run.md, the topic on the start of Claude Code,
// records the decision.

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cloveclovedev/cumin-works/templates"
)

// Skill is one skill that cumin writes for the agents: the body is a
// template file, as it is; the name and the description come from here.
type Skill struct {
	Name        string
	Description string
	// Template is the file in templates/ that is the body of the skill.
	Template string
}

// skills are the skills of every role. The directory is shared by all
// roles, so the names carry the prefix "cumin-".
var skills = []Skill{
	{
		Name:        "cumin-pull-request",
		Description: "Write or update the description of a pull request in the form that cumin reads. Use before gh pr create and before gh pr edit.",
		Template:    "pull-request.md",
	},
	{
		Name:        "cumin-review-reply",
		Description: "Reply to a review comment with one of the statuses Fixed, Not changed, Deferred, or Answer. Use before you reply to a review comment.",
		Template:    "review-reply.md",
	},
	{
		Name:        "cumin-decision-request",
		Description: "Write a decision request for the Owner. Use before you return the result blocked, to write blocked_reason.",
		Template:    "decision-request.md",
	},
}

// Skills returns the skills of every role.
func Skills() []Skill {
	return append([]Skill(nil), skills...)
}

// SkillPath is the path of the skill file under the skills directory:
// <dir>/.claude/skills/<name>/SKILL.md, where Claude Code finds the skills
// of a directory passed with --add-dir (official: Skills).
func SkillPath(dir, name string) string {
	return filepath.Join(dir, ".claude", "skills", name, "SKILL.md")
}

// WriteSkills removes the skills under dir and writes the current ones,
// so that the directory always matches the binary, also after a skill was
// renamed or removed. dir is the directory to pass with --add-dir.
func WriteSkills(dir string) error {
	if err := os.RemoveAll(filepath.Join(dir, ".claude", "skills")); err != nil {
		return fmt.Errorf("write the skills: %w", err)
	}
	for _, skill := range skills {
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
	return nil
}
