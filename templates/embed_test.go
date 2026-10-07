package templates

import (
	"os"
	"strings"
	"testing"
)

// Every Markdown file of the directory is embedded, as it is.
func TestRead_ReturnsEveryTemplateFile(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		n++
		want, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		got, err := Read(entry.Name())
		if err != nil {
			t.Errorf("Read(%s): %v", entry.Name(), err)
			continue
		}
		if got != string(want) {
			t.Errorf("Read(%s) differs from the file", entry.Name())
		}
	}
	if n == 0 {
		t.Fatal("no template file found")
	}
}

func TestRead_RejectsAnUnknownName(t *testing.T) {
	for _, name := range []string{"missing.md", "../roles/implementer.md", ""} {
		if _, err := Read(name); err == nil {
			t.Errorf("Read(%q) succeeded, want an error", name)
		}
	}
}

// The decision request names the stopped issue, with no role and no kind
// of issue, so that the same lines are right for every role. The template
// and its example hold the same "Next step" lines, and the three headings
// that the Maintainer reads stay.
func TestDecisionRequest_NextStepNamesTheStoppedIssue(t *testing.T) {
	text, err := Read("decision-request.md")
	if err != nil {
		t.Fatal(err)
	}
	const nextStep = "To continue: write your decision as a comment, or edit the stopped issue. " +
		"Then add the label `cumin/status/ready` to the stopped issue.\n" +
		"Until then: the stopped issue waits. Other issues continue.\n"
	parts := strings.Split(text, "### Next step\n")
	if len(parts) != 3 {
		t.Fatalf("the template holds %d \"Next step\" sections, want 2: the template and its example", len(parts)-1)
	}
	for _, part := range parts[1:] {
		if !strings.HasPrefix(part, nextStep) {
			t.Errorf("a \"Next step\" section does not start with:\n%s\ngot:\n%s", nextStep, part)
		}
	}
	for _, heading := range []string{"### Situation\n", "### Options\n", "### Next step\n"} {
		if strings.Count(text, heading) != 2 {
			t.Errorf("the template and its example must each hold the heading %q", heading)
		}
	}
	if strings.Count(text, "\n### ") != 6 {
		t.Error("the template and its example must hold three `###` headings each, and no other")
	}
	if want := "The stopped issue is the issue of the run: the line \"Issue of the run\" in the facts of the start request."; !strings.Contains(text, want) {
		t.Errorf("the rules of the template do not say: %s", want)
	}
}
