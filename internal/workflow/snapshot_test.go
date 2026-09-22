package workflow

import (
	"fmt"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/platform/github"
)

// toSnapshot copies every field that the rules read, and nothing of the
// platform types reaches the snapshot.
func TestToSnapshot_CopiesTitleBlockedByAndPullRequests(t *testing.T) {
	read := github.RepositorySnapshot{RequirementIssues: []github.Issue{{
		Number: 6, Labels: []string{"cumin/type/requirement"},
		SubIssues: []github.Issue{{
			Number: 10, Title: "Add the login screen", Labels: []string{"cumin/status/implementing", "risk/low"},
			BlockedBy: []github.IssueRef{{Number: 9, Closed: true}},
			PullRequests: []github.PullRequest{
				{Number: 21, HeadCommit: "2222", Author: "example-implementer[bot]"},
			},
		}},
	}}}
	got := toSnapshot(read)
	want := Snapshot{RequirementIssues: []RequirementIssue{{
		Number: 6, Labels: []string{"cumin/type/requirement"},
		SubIssues: []SubIssue{{
			Number: 10, Title: "Add the login screen", Labels: []string{"cumin/status/implementing", "risk/low"},
			BlockedBy: []BlockedBy{{Number: 9, Closed: true}},
			PullRequests: []PullRequest{
				{Number: 21, HeadCommit: "2222", Author: "example-implementer[bot]"},
			},
		}},
	}}}
	if fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", want) {
		t.Errorf("toSnapshot =\n%+v\nwant\n%+v", got, want)
	}
}
