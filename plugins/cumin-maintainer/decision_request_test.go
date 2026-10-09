package cuminmaintainer

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// quotedHeading matches a heading that a skill names in backticks.
var quotedHeading = regexp.MustCompile("`(#{1,6} [^`]+)`")

// The skill sorts a request into two kinds and answers only one of them. The
// form of the answer belongs to the templates, so the skill names no heading
// that the templates do not hold.
func TestDecisionRequestSkill_HoldsTheTwoKindsAndTheFormOfTheTemplate(t *testing.T) {
	text := readText(filepath.Join("skills", "decision-request"), "SKILL.md")
	for _, want := range []string{
		"## The two kinds\n",
		"Operational or technical",
		"Of the Maintainer",
		"When the kind is not clear, treat the request as the kind of the Maintainer.",
		"`templates/decision-request.md`",
		"`templates/stop-note.md`",
		"only inside the allowance that the Maintainer gave this session",
		"The session writes nothing on GitHub for this kind",
		"- `gh issue comment <number> --body-file <file>`",
		"- `gh issue edit <number> --add-label cumin/status/ready`",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("SKILL.md lacks %q", want)
		}
	}

	templates := filepath.Join("..", "..", "templates")
	lines := strings.Split(readText(templates, "decision-request.md")+"\n"+readText(templates, "stop-note.md"), "\n")
	headings := quotedHeading.FindAllStringSubmatch(text, -1)
	if len(headings) == 0 {
		t.Error("SKILL.md names no heading of the templates")
	}
	for _, heading := range headings {
		var found bool
		for _, line := range lines {
			found = found || strings.HasPrefix(line, heading[1])
		}
		if !found {
			t.Errorf("SKILL.md names the heading %q, which no template holds", heading[1])
		}
	}

	commands, _ := section(text, "Commands")
	read, recorded, ok := strings.Cut(commands, "Recorded by GitHub, so ask first:")
	if !ok {
		t.Fatal("the section \"## Commands\" does not mark the commands that GitHub records")
	}
	for _, command := range []string{"gh issue comment", "gh issue edit", "gh run rerun"} {
		if strings.Contains(read, command) || !strings.Contains(recorded, command) {
			t.Errorf("%q must stand only under the commands that GitHub records", command)
		}
	}
	// Every command in the text of the skill stands under "## Commands".
	for _, command := range regexp.MustCompile("`((?:gh|cumin) [^`]+)`").FindAllStringSubmatch(text, -1) {
		if !strings.Contains(commands, "- `"+command[1]+"`") {
			t.Errorf("the section \"## Commands\" lacks %q", command[1])
		}
	}
}
