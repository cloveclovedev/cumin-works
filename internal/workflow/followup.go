package workflow

// This file is the pure part of I9 (copy the work left after a merge into a
// follow-up note): which closed sub-issues still need a read, what the note
// copies from the pull request, and the text of the note. The text follows
// templates/follow-up-note.md; a test keeps the two equal.
// docs/ja/designs/poll.md, the topic on the follow-up notes.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// RowI9 is the row of the follow-up note in issue-states.md.
const RowI9 = "I9"

// MergedPullRequest is the pull request that closed a sub-issue, as much as
// the follow-up note needs.
type MergedPullRequest struct {
	Number int
	Merged bool
	// Body is the description of the pull request.
	Body    string
	Threads []ReviewThread
}

// ReviewThread is one thread of review comments on a line, first comment
// first.
type ReviewThread struct {
	Path string
	// Line is 0 when GitHub gives no line.
	Line     int
	Comments []ReviewComment
}

// ReviewComment is one comment of a review thread.
type ReviewComment struct {
	// Author is the login, "<slug>[bot]" for a GitHub App.
	Author string
	Body   string
	URL    string
}

// FollowUpMark is the marker of one follow-up note that cumin wrote: the
// sub-issue, the pull request, when the comment was written, and the pull
// requests of the sub-issue that needed a note when cumin wrote it.
type FollowUpMark struct {
	Issue       int
	PullRequest int
	At          time.Time
	// Notes are the pull requests of the sub-issue that need a note: the
	// merged ones that leave work, this one included, and the linked ones
	// that were still open. The sub-issue is done when each of them has a
	// note. A marker without the list names its own pull request only.
	Notes []int
}

// followUpMarkerPattern matches the marker line that FollowUpNote writes.
var followUpMarkerPattern = regexp.MustCompile(`(?m)^<!-- cumin:follow-up-note issue=(\d+) pull-request=(\d+)(?: notes=(\d+(?:,\d+)*))? -->$`)

// FollowUpMarker is the hidden last line of a follow-up note. It lets cumin
// find its note after a restart, so that one pull request gets one note.
// notes are the pull requests of the sub-issue that need a note.
func FollowUpMarker(issue, pullRequest int, notes []int) string {
	list := make([]string, 0, len(notes))
	for _, n := range notes {
		list = append(list, strconv.Itoa(n))
	}
	return fmt.Sprintf("<!-- cumin:follow-up-note issue=%d pull-request=%d notes=%s -->", issue, pullRequest, strings.Join(list, ","))
}

// FollowUpMarks returns the markers in the comments that cumin wrote. The
// author must be cumin: the repository may be public, and a marker in the
// comment of anyone else must not stop a note.
func FollowUpMarks(comments []Comment, cumin string) []FollowUpMark {
	var marks []FollowUpMark
	for _, c := range comments {
		if cumin == "" || c.Author != cumin {
			continue
		}
		for _, m := range followUpMarkerPattern.FindAllStringSubmatch(c.Body, -1) {
			issue, err1 := strconv.Atoi(m[1])
			pr, err2 := strconv.Atoi(m[2])
			if err1 != nil || err2 != nil {
				continue
			}
			mark := FollowUpMark{Issue: issue, PullRequest: pr, At: c.CreatedAt, Notes: []int{pr}}
			if m[3] != "" {
				mark.Notes = nil
				for _, n := range strings.Split(m[3], ",") {
					if v, err := strconv.Atoi(n); err == nil {
						mark.Notes = append(mark.Notes, v)
					}
				}
			}
			marks = append(marks, mark)
		}
	}
	return marks
}

// FollowUpCandidates are the closed sub-issues of the requirement issue
// that I9 must read. A sub-issue is done when notes were written at or
// after its last close, and each pull request that those notes name as
// needing a note has its own. A write that failed halfway leaves a pull
// request without its note, so the sub-issue is read again. A sub-issue
// that was opened again and closed later is read again too;
// HasFollowUpNote then tells the pull requests apart.
func FollowUpCandidates(requirement RequirementIssue, marks []FollowUpMark) []SubIssue {
	var candidates []SubIssue
	for _, sub := range requirement.SubIssues {
		if !sub.Closed {
			continue
		}
		noted := map[int]bool{}
		var needed []int
		for _, m := range marks {
			// Times of GitHub have a resolution of one second, so a note
			// in the same second as the close counts.
			if m.Issue == sub.Number && !m.At.Before(sub.ClosedAt) {
				noted[m.PullRequest] = true
				needed = append(needed, m.Notes...)
			}
		}
		done := len(noted) > 0
		for _, n := range needed {
			if !noted[n] {
				done = false
			}
		}
		if !done {
			candidates = append(candidates, sub)
		}
	}
	return candidates
}

// FollowUpSince is the oldest close among the closed sub-issues: every note
// of cumin that counts was written after it. The poll reads the comments of
// the requirement issue from there. It is zero when no sub-issue is closed.
func FollowUpSince(requirement RequirementIssue) time.Time {
	var since time.Time
	for _, sub := range requirement.SubIssues {
		if sub.Closed && (since.IsZero() || sub.ClosedAt.Before(since)) {
			since = sub.ClosedAt
		}
	}
	return since
}

// HasFollowUpNote reports whether a note for the pull request exists.
func HasFollowUpNote(marks []FollowUpMark, pullRequest int) bool {
	for _, m := range marks {
		if m.PullRequest == pullRequest {
			return true
		}
	}
	return false
}

// FollowUpSection returns the text of the "## Follow-up" section of a pull
// request description, as it is, without the HTML comments of the
// template. It is empty when the section is missing, empty, or "None".
func FollowUpSection(body string) string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimRight(line, " \t") == "## Follow-up" {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return ""
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "# ") || strings.HasPrefix(lines[i], "## ") {
			end = i
			break
		}
	}
	text := strings.TrimSpace(htmlComment.ReplaceAllString(strings.Join(lines[start:end], "\n"), ""))
	if strings.EqualFold(strings.TrimSuffix(text, "."), "None") {
		return ""
	}
	return text
}

// htmlComment matches an HTML comment, such as the hints of the template.
var htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)

// commentLabel matches the first line of a review comment in the format of
// templates/review.md: "<label> (<blocking|non-blocking>): ...".
var commentLabel = regexp.MustCompile(`^([a-z]+) \((blocking|non-blocking)\):`)

// OpenNonBlockingComment is one non-blocking review comment that nobody
// answered.
type OpenNonBlockingComment struct {
	Path      string
	Line      int
	FirstLine string
	URL       string
}

// OpenNonBlockingComments returns the threads whose first comment is a
// non-blocking comment of the Reviewer App (reviewer, "<slug>[bot]"), and
// that no reply answers. A reply answers when it starts with "Fixed" or
// "Answer" (templates/review-reply.md). The labels praise and note are not
// work, so they are skipped.
func OpenNonBlockingComments(threads []ReviewThread, reviewer string) []OpenNonBlockingComment {
	var open []OpenNonBlockingComment
	for _, thread := range threads {
		if len(thread.Comments) == 0 || reviewer == "" {
			continue
		}
		first := thread.Comments[0]
		if first.Author != reviewer {
			continue
		}
		line := strings.TrimSpace(firstLine(first.Body))
		m := commentLabel.FindStringSubmatch(line)
		if m == nil || m[2] != "non-blocking" || m[1] == "praise" || m[1] == "note" {
			continue
		}
		answered := false
		for _, reply := range thread.Comments[1:] {
			text := strings.TrimSpace(reply.Body)
			if strings.HasPrefix(text, "Fixed") || strings.HasPrefix(text, "Answer") {
				answered = true
				break
			}
		}
		if !answered {
			open = append(open, OpenNonBlockingComment{Path: thread.Path, Line: thread.Line, FirstLine: line, URL: first.URL})
		}
	}
	return open
}

// FollowUpNote returns the follow-up note of a merged pull request that is
// linked to close the sub-issue, in the form of
// templates/follow-up-note.md, and whether there is anything to list. With
// nothing to list, cumin writes no note (Core-11). notes are the pull
// requests of the sub-issue that need a note, for the marker; nil means
// this one only.
func FollowUpNote(sub SubIssue, pr MergedPullRequest, reviewer string, notes []int) (string, bool) {
	if notes == nil {
		notes = []int{pr.Number}
	}
	followUp := FollowUpSection(pr.Body)
	open := OpenNonBlockingComments(pr.Threads, reviewer)
	if followUp == "" && len(open) == 0 {
		return "", false
	}
	if followUp == "" {
		followUp = "None"
	}
	var list strings.Builder
	if len(open) == 0 {
		list.WriteString("None\n")
	}
	for _, c := range open {
		place := c.Path
		if c.Line > 0 {
			place = fmt.Sprintf("%s:%d", c.Path, c.Line)
		}
		fmt.Fprintf(&list, "- `%s` — %s (%s)\n", place, c.FirstLine, c.URL)
	}
	return fmt.Sprintf(`## Follow-up from #%d (%s)

### From the pull request description
%s

### Open non-blocking review comments
%s
To do any of this work: write a new requirement issue that names the items. This list is only a record.

%s
`, pr.Number, sub.Title, followUp, list.String(), FollowUpMarker(sub.Number, pr.Number, notes)), true
}
