package workflow

// This file is pure, like domain.go: the branch name of an implementation
// issue and the request text of the kind "implement". docs/ja/designs/
// poll.md, the topic on the request to the Implementer, records the
// rules.

import (
	"fmt"
	"strings"
)

// slugMaxLen is the longest slug of a branch name. Long titles make long
// branch names; 40 characters keep the whole name readable in a list.
const slugMaxLen = 40

// BranchName returns the branch of an implementation issue:
// cumin/<number>-<slug>, where the slug comes from the title. The slug is
// lower case; every run of characters other than a-z and 0-9 becomes one
// "-"; leading and trailing "-" are removed; the longest prefix of whole
// words that is slugMaxLen characters or shorter is kept, and a first
// word longer than that is cut; an empty slug becomes "issue".
func BranchName(number int, title string) string {
	return fmt.Sprintf("cumin/%d-%s", number, slug(title))
}

func slug(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "issue"
	}
	if len(s) <= slugMaxLen {
		return s
	}
	// The last word boundary at or before the limit. Without one, the
	// first word alone is longer than the limit, and it is cut.
	if cut := strings.LastIndex(s[:slugMaxLen+1], "-"); cut > 0 {
		return s[:cut]
	}
	return s[:slugMaxLen]
}

// ClaimBranch returns the branch of a claim (I1) and the pull request whose
// work the claim continues. When an open pull request closes the issue, the
// work continues on its branch, and the request kind is "continue"; of two
// or more, the one with the highest number is used, as I2 checks it.
// Otherwise the branch comes from the title, and pullRequest is 0.
func ClaimBranch(sub SubIssue) (branch string, pullRequest int) {
	if pr, ok := sub.LatestPullRequest(); ok && pr.HeadBranch != "" {
		return pr.HeadBranch, pr.Number
	}
	return BranchName(sub.Number, sub.Title), 0
}

// ImplementRequestText returns the request text of the kind "implement"
// (implementer.md, the request kinds): what the Implementer does this
// time. The role instruction holds everything that does not depend on the
// kind of the request; the text names the skill for the pull request, so
// that the template is loaded right before the action.
func ImplementRequestText(repository string, number int, branch, workDir string) string {
	return fmt.Sprintf(`Request: implement
Repository: %[1]s
Implementation issue: #%[2]d
Branch: %[3]s
Work directory: %[4]s

Read the issue #%[2]d of %[1]s, its parent requirement issue, and the documents that they link to. Implement the issue in the work directory, which is a git worktree already on the branch %[3]s. Commit on that branch and push it. Then open one pull request to the default branch; invoke the skill cumin-pull-request before you write the description, and write "Closes #%[2]d" in it. Then return the result.
`, repository, number, branch, workDir)
}

// ContinueRequestText returns the request text of the kind "continue"
// (implementer.md, the request kinds): the Owner added cumin/status/ready
// again to an issue whose pull request is open. The session is new, so the
// text says where the earlier work is and that it goes on in the same pull
// request.
func ContinueRequestText(repository string, number, pullRequest int, branch, workDir string) string {
	return fmt.Sprintf(`Request: continue
Repository: %[1]s
Implementation issue: #%[2]d
Pull request: #%[5]d
Branch: %[3]s
Work directory: %[4]s

The pull request #%[5]d already closes the issue #%[2]d, and the work continues in it. Read the issue #%[2]d of %[1]s, its parent requirement issue, the documents that they link to, the pull request #%[5]d with its reviews, and the comments of the Owner. The work directory is a git worktree already on the branch %[3]s of that pull request. Commit on that branch and push it. Do not open a new pull request; invoke the skill cumin-pull-request before you update the description of #%[5]d, and keep "Closes #%[2]d" in it. Then return the result.
`, repository, number, branch, workDir, pullRequest)
}
