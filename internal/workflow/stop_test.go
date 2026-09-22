package workflow_test

import (
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/workflow"
	"github.com/cloveclovedev/cumin-works/templates"
)

// The note that cumin writes must follow templates/stop-note.md: the
// heading and every field of the template block, in the same order. The
// template is the contract between cumin and the Owner.
func TestStopNote_FollowsTheTemplate(t *testing.T) {
	t.Parallel()
	template, err := templates.Read("stop-note.md")
	if err != nil {
		t.Fatal(err)
	}
	note := workflow.StopNote("I2", "The Implementer reported done, but no open pull request closes this issue.", 0, false)

	fields := []string{"## Stopped for the Owner", "Row: ", "Reason: ", "Pull request: ", "Retried: ", "To continue: "}
	at := -1
	for _, field := range fields {
		if !strings.Contains(template, field) {
			t.Errorf("the template has no %q", field)
		}
		i := strings.Index(note, field)
		if i < 0 {
			t.Errorf("the note has no %q:\n%s", field, note)
			continue
		}
		if i < at {
			t.Errorf("the note has %q out of the order of the template:\n%s", field, note)
		}
		at = i
	}
	if strings.Contains(note, "**") {
		t.Error("the note uses bold text")
	}
}

func TestStopNote_NamesThePullRequestAndTheRetry(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		pullRequest int
		retried     bool
		want        []string
	}{
		{"no pull request, no retry", 0, false, []string{"Pull request: None", "Retried: no"}},
		{"a pull request, no retry", 21, false, []string{"Pull request: #21", "Retried: no"}},
		{"a pull request after one retry", 21, true, []string{"Pull request: #21", "Retried: once"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			note := workflow.StopNote("I2", "A reason.", test.pullRequest, test.retried)
			for _, want := range test.want {
				if !strings.Contains(note, want) {
					t.Errorf("the note has no %q:\n%s", want, note)
				}
			}
		})
	}
}

// Every failed check of I2 has its own sentence, and each one says what
// cumin checked and what it found.
func TestVerificationReason_OneSentenceForEachCheck(t *testing.T) {
	t.Parallel()
	failures := []workflow.VerificationFailure{
		workflow.FailureNoOpenPullRequest,
		workflow.FailureAuthorMismatch,
		workflow.FailureHeadNotPushed,
	}
	seen := map[string]bool{}
	for _, failure := range failures {
		reason := workflow.VerificationReason(failure)
		if reason == "" || !strings.HasSuffix(reason, ".") {
			t.Errorf("the reason of %s is not one sentence: %q", failure, reason)
		}
		if seen[reason] {
			t.Errorf("two checks give the same reason: %q", reason)
		}
		seen[reason] = true
		if strings.Contains(reason, "\n") {
			t.Errorf("the reason of %s has more than one line: %q", failure, reason)
		}
	}
}
