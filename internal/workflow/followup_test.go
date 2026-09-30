package workflow_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
	"github.com/cloveclovedev/cumin-works/internal/workflow"
	"github.com/cloveclovedev/cumin-works/templates"
)

const (
	// cuminSlug is the App of cumin-core in the scene. The comments that
	// cumin writes carry it as their author.
	cuminSlug  = "example-cumin-core"
	cuminLogin = cuminSlug + "[bot]"
	// reviewerSlug is the author of the review comments. The fake knows
	// one App, so the Reviewer is the bot of that App.
	reviewerSlug = implementerSlug
)

// followUpBody is a pull request description with text under "Follow-up".
const followUpBody = `## What
Add the login screen.

## Follow-up
- The error text of the login screen is not translated yet.
`

// followUpScene changes the scene of I9.
type followUpScene struct {
	// closedByPerson makes a person close the sub-issue instead of #21.
	closedByPerson bool
	// notMerged closes #21 without a merge.
	notMerged bool
	// requirementClosed closes the requirement issue #6.
	requirementClosed bool
}

// newFollowUpScene is the scene of I9: the sub-issue #10 of the open
// requirement issue #6 was closed an hour ago by the merged pull request
// #21. The fake CLI answers as a Planner that returned done, for the
// acceptance check that R4 asks for.
func newFollowUpScene(t *testing.T, body string, threads []githubtest.ReviewThread, opts ...followUpScene) *scene {
	t.Helper()
	var o followUpScene
	if len(opts) > 0 {
		o = opts[0]
	}
	sc := newScene(t, cliOptions{fixture: "planner-done.jsonl"})
	sc.fake.SetCommentAuthor(cuminSlug)
	closedBy := 21
	if o.closedByPerson {
		closedBy = 0
	}
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{
		Number: 10, Parent: 6, Title: subIssueTitle, Closed: true, ClosedAt: time.Now().Add(-time.Hour),
		ClosedBy: closedBy, Labels: []string{"risk/low"},
	})
	if o.requirementClosed {
		sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 6, Closed: true, Labels: []string{githubtest.RequirementLabel, "cumin/status/implementing"}})
	}
	sc.fake.AddPullRequest(sc.repo, &githubtest.PullRequest{
		Number: 21, Closed: true, Merged: !o.notMerged, Closes: []int{10}, Body: body, Threads: threads,
	})
	return sc
}

// reviewComment is the first comment of a thread by the Reviewer App.
func reviewComment(body, url string) githubtest.ReviewComment {
	return githubtest.ReviewComment{Author: reviewerSlug, AuthorIsBot: true, Body: body, URL: url}
}

// reply is a reply of the Implementer App in a thread.
func reply(body string) githubtest.ReviewComment {
	return githubtest.ReviewComment{Author: implementerSlug, AuthorIsBot: true, Body: body}
}

// followUpNotes returns the comments on the requirement issue #6 that are
// follow-up notes.
func followUpNotes(sc *scene) []githubtest.Comment {
	var notes []githubtest.Comment
	for _, c := range sc.fake.Comments(sc.repo, 6) {
		if strings.HasPrefix(c.Body, "## Follow-up from ") {
			notes = append(notes, c)
		}
	}
	return notes
}

// closerReads counts the queries of the pull request that closed an issue.
func closerReads(t *testing.T, sc *scene) int {
	t.Helper()
	n := 0
	for _, r := range sc.fake.Requests() {
		if r.Path != "/graphql" {
			continue
		}
		var body struct {
			Variables map[string]any `json:"variables"`
		}
		if err := json.Unmarshal(r.Body, &body); err != nil {
			t.Fatal(err)
		}
		if _, ok := body.Variables["threads"]; ok {
			n++
		}
	}
	return n
}

// Core-10 (cumin-core.md): a merged pull request with text under
// "Follow-up" and one open non-blocking comment gives one note in the form
// of the template. A restart of cumin adds no second note.
func TestCore10_ANoteInTheFixedFormOnceAcrossRestarts(t *testing.T) {
	threads := []githubtest.ReviewThread{
		{Path: "lib/login.dart", Line: 12, Comments: []githubtest.ReviewComment{
			reviewComment("suggestion (non-blocking): Move the validation into its own function.\n\nWhy: shorter.", "https://example.test/c1"),
			reply("Deferred: outside the scope of the issue."),
		}},
		{Path: "lib/login.dart", Line: 30, Comments: []githubtest.ReviewComment{
			reviewComment("nitpick (non-blocking): Rename the variable.", "https://example.test/c2"),
			reply("Fixed in abc1234."),
		}},
		{Path: "lib/login.dart", Line: 40, Comments: []githubtest.ReviewComment{
			reviewComment("praise (non-blocking): Clear tests.", "https://example.test/c3"),
		}},
		{Path: "lib/login.dart", Line: 50, Comments: []githubtest.ReviewComment{
			reviewComment("issue (blocking): The token is logged.", "https://example.test/c4"),
		}},
		{Path: "lib/login.dart", Line: 60, Comments: []githubtest.ReviewComment{
			{Author: "octocat", Body: "suggestion (non-blocking): A comment of another account.", URL: "https://example.test/c5"},
		}},
	}
	sc := newFollowUpScene(t, followUpBody, threads)

	sc.pollAndWait(t, sc.service())
	notes := followUpNotes(sc)
	if len(notes) != 1 {
		t.Fatalf("%d follow-up notes, want 1", len(notes))
	}
	want := "## Follow-up from #21 (" + subIssueTitle + ")\n\n" +
		"From the pull request description:\n- The error text of the login screen is not translated yet.\n\n" +
		"Open non-blocking review comments:\n" +
		"- `lib/login.dart:12` — suggestion (non-blocking): Move the validation into its own function. (https://example.test/c1)\n\n" +
		"To do any of this work: write a new requirement issue that names the items. This list is only a record.\n\n" +
		"<!-- cumin:follow-up-note issue=10 pull-request=21 -->\n"
	if notes[0].Body != want {
		t.Errorf("the note:\n%s\nwant:\n%s", notes[0].Body, want)
	}

	service := sc.service()
	sc.pollAndWait(t, service)
	sc.pollAndWait(t, service)
	if n := len(followUpNotes(sc)); n != 1 {
		t.Errorf("%d follow-up notes after a restart, want 1", n)
	}
	// With the note in place, the sub-issue is not read again.
	if n := closerReads(t, sc); n != 1 {
		t.Errorf("%d reads of the pull request, want 1", n)
	}
}

// Core-11 (cumin-core.md): "Follow-up" is None and every non-blocking
// comment is Fixed, so no note is written.
func TestCore11_NothingLeftGivesNoNote(t *testing.T) {
	body := "## What\nAdd the login screen.\n\n## Follow-up\n<!-- Work outside the scope. Or \"None\". -->\nNone\n"
	threads := []githubtest.ReviewThread{
		{Path: "lib/login.dart", Line: 12, Comments: []githubtest.ReviewComment{
			reviewComment("suggestion (non-blocking): Move the validation.", "https://example.test/c1"),
			reply("Fixed in abc1234."),
		}},
		{Path: "lib/login.dart", Line: 20, Comments: []githubtest.ReviewComment{
			reviewComment("note (non-blocking): This matches the design.", "https://example.test/c2"),
		}},
	}
	sc := newFollowUpScene(t, body, threads)
	sc.pollAndWait(t, sc.service())

	if n := len(followUpNotes(sc)); n != 0 {
		t.Errorf("%d follow-up notes, want none", n)
	}
}

// Principle 6 (issue-states.md): cumin does nothing on a closed
// requirement issue. It writes no note and reads none of its sub-issues.
func TestI9_AClosedRequirementIssueGetsNoNote(t *testing.T) {
	sc := newFollowUpScene(t, followUpBody, nil, followUpScene{requirementClosed: true})
	sc.pollAndWait(t, sc.service())

	if n := len(followUpNotes(sc)); n != 0 {
		t.Errorf("%d follow-up notes, want none", n)
	}
	if n := closerReads(t, sc); n != 0 {
		t.Errorf("%d reads of a pull request, want none", n)
	}
}

// A sub-issue that a person closed, or that a pull request closed without a
// merge, has no work to copy.
func TestI9_ASubIssueClosedWithoutAMergeGetsNoNote(t *testing.T) {
	tests := []struct {
		name  string
		scene followUpScene
	}{
		{"closed by a person", followUpScene{closedByPerson: true}},
		{"closed by a pull request that was not merged", followUpScene{notMerged: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := newFollowUpScene(t, followUpBody, nil, tt.scene)
			sc.pollAndWait(t, sc.service())

			if n := len(followUpNotes(sc)); n != 0 {
				t.Errorf("%d follow-up notes, want none", n)
			}
		})
	}
}

// The repository may be public, so a marker in a comment of another
// account does not count as the note of cumin.
func TestI9_AMarkerOfAnotherAuthorDoesNotStopTheNote(t *testing.T) {
	sc := newFollowUpScene(t, followUpBody, nil)
	sc.fake.AddComment(sc.repo, 6, githubtest.Comment{
		Author: "octocat", At: time.Now(),
		Body: "Nothing here.\n\n" + workflow.FollowUpMarker(10, 21) + "\n",
	})
	sc.pollAndWait(t, sc.service())

	if n := len(followUpNotes(sc)); n != 1 {
		t.Errorf("%d follow-up notes, want 1", n)
	}
}

// A failed write leaves no note; the next poll writes it.
func TestI9_AFailedWriteIsTriedAgainAtTheNextPoll(t *testing.T) {
	sc := newFollowUpScene(t, followUpBody, nil)
	sc.fake.FailNext("POST", "/repos/example-org/example-repo/issues/6/comments", 500)
	service := sc.service()
	sc.pollAndWait(t, service)
	if n := len(followUpNotes(sc)); n != 0 {
		t.Fatalf("%d follow-up notes after the failure, want none", n)
	}
	sc.pollAndWait(t, service)
	if n := len(followUpNotes(sc)); n != 1 {
		t.Errorf("%d follow-up notes, want 1", n)
	}
}

// The note that cumin writes follows templates/follow-up-note.md: every
// line of the template block with a fixed text, in the same order.
func TestFollowUpNote_FollowsTheTemplate(t *testing.T) {
	t.Parallel()
	template, err := templates.Read("follow-up-note.md")
	if err != nil {
		t.Fatal(err)
	}
	pr := workflow.MergedPullRequest{Number: 21, Merged: true, Body: followUpBody, Threads: []workflow.ReviewThread{
		{Path: "a.go", Line: 3, Comments: []workflow.ReviewComment{{Author: "r[bot]", Body: "todo (non-blocking): Add a test.", URL: "https://example.test/c"}}},
	}}
	note, ok := workflow.FollowUpNote(workflow.SubIssue{Number: 10, Title: "Title"}, pr, "r[bot]")
	if !ok {
		t.Fatal("no note")
	}
	fields := []string{"## Follow-up from #", "From the pull request description:", "Open non-blocking review comments:", "- `", "To do any of this work: write a new requirement issue that names the items. This list is only a record.", "<!-- cumin:follow-up-note issue="}
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

// Either part may be empty; the empty part says None.
func TestFollowUpNote_AnEmptyPartSaysNone(t *testing.T) {
	t.Parallel()
	sub := workflow.SubIssue{Number: 10, Title: "Title"}
	onlyText, ok := workflow.FollowUpNote(sub, workflow.MergedPullRequest{Number: 21, Body: followUpBody}, "r[bot]")
	if !ok || !strings.Contains(onlyText, "Open non-blocking review comments:\nNone\n") {
		t.Errorf("a note without open comments:\n%s", onlyText)
	}
	threads := []workflow.ReviewThread{{Path: "a.go", Comments: []workflow.ReviewComment{{Author: "r[bot]", Body: "todo (non-blocking): Add a test.", URL: "u"}}}}
	onlyComments, ok := workflow.FollowUpNote(sub, workflow.MergedPullRequest{Number: 21, Threads: threads}, "r[bot]")
	if !ok || !strings.Contains(onlyComments, "From the pull request description:\nNone\n") {
		t.Errorf("a note without follow-up text:\n%s", onlyComments)
	}
	// A thread without a line names the file only.
	if !strings.Contains(onlyComments, "- `a.go` — todo (non-blocking): Add a test. (u)") {
		t.Errorf("a comment without a line:\n%s", onlyComments)
	}
}

func TestFollowUpSection(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, body, want string
	}{
		{"text up to the next heading", "## Follow-up\nOne.\nTwo.\n## Other\nNo.", "One.\nTwo."},
		{"text at the end", "## What\nX\n\n## Follow-up\n\n- One item.\n", "- One item."},
		{"None", "## Follow-up\nNone\n", ""},
		{"None with a period", "## Follow-up\nNone.\n", ""},
		{"only the hint of the template", "## Follow-up\n<!-- Work outside the scope. -->\n", ""},
		{"the hint and text", "## Follow-up\n<!-- hint -->\nOne.\n", "One."},
		{"no section", "## What\nX\n", ""},
		{"a deeper heading stays", "## Follow-up\n### Later\nOne.\n", "### Later\nOne."},
		{"Windows line ends", "## Follow-up\r\nOne.\r\n", "One."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := workflow.FollowUpSection(tt.body); got != tt.want {
				t.Errorf("FollowUpSection = %q, want %q", got, tt.want)
			}
		})
	}
}

// A sub-issue that was opened again and closed after its note is read
// again; the note of the same pull request then stops a second note.
func TestFollowUpCandidates_ANoteBeforeTheLastCloseReadsAgain(t *testing.T) {
	t.Parallel()
	closed := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	requirement := workflow.RequirementIssue{Number: 6, SubIssues: []workflow.SubIssue{
		{Number: 10, Closed: true, ClosedAt: closed},
		{Number: 11, Closed: true, ClosedAt: closed},
		{Number: 12},
	}}
	marks := []workflow.FollowUpMark{
		{Issue: 10, PullRequest: 21, At: closed},
		{Issue: 11, PullRequest: 22, At: closed.Add(-time.Hour)},
	}
	got := workflow.FollowUpCandidates(requirement, marks)
	if len(got) != 1 || got[0].Number != 11 {
		t.Errorf("candidates = %v, want #11 only", got)
	}
	if !workflow.HasFollowUpNote(marks, 22) {
		t.Error("the note of #22 is not found")
	}
}

// A thread on code that has moved since has no line now; the note names the
// line that the comment was written on.
func TestI9_AThreadOnMovedCodeNamesItsOriginalLine(t *testing.T) {
	threads := []githubtest.ReviewThread{
		{Path: "lib/login.dart", OriginalLine: 7, Comments: []githubtest.ReviewComment{
			reviewComment("todo (non-blocking): Add a test for the empty password.", "https://example.test/c1"),
		}},
	}
	sc := newFollowUpScene(t, "## Follow-up\nNone\n", threads)
	sc.pollAndWait(t, sc.service())

	notes := followUpNotes(sc)
	if len(notes) != 1 || !strings.Contains(notes[0].Body, "- `lib/login.dart:7` — todo (non-blocking)") {
		t.Errorf("the notes: %v", notes)
	}
}

// One pull request that closes two sub-issues gets one note, also in the
// poll that writes it.
func TestI9_OnePullRequestThatClosesTwoSubIssuesGetsOneNote(t *testing.T) {
	sc := newFollowUpScene(t, followUpBody, nil)
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{
		Number: 11, Parent: 6, Title: "Add the logout button", Closed: true, ClosedAt: time.Now().Add(-time.Hour),
		ClosedBy: 21, Labels: []string{"risk/low"},
	})
	sc.pollAndWait(t, sc.service())

	if n := len(followUpNotes(sc)); n != 1 {
		t.Errorf("%d follow-up notes, want 1", n)
	}
}
