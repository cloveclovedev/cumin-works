package workflow_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
)

// unreadIssue is one way to put an issue over a read limit under the
// requirement issue #30 of the scene, beside the ready issue #10 of #6.
type unreadIssue struct {
	name string
	add  func(sc *scene)
	// issue is the issue over the limit, and limit is the limit as the log
	// names it.
	issue int
	limit string
}

var unreadIssues = []unreadIssue{
	{
		name: "a requirement issue with 37 sub-issues",
		add: func(sc *scene) {
			for n := 32; n <= 67; n++ {
				sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: n, Parent: 30, Closed: true})
			}
		},
		issue: 30, limit: "more than 36 sub-issues",
	},
	{
		name: "a sub-issue with more labels than cumin reads",
		add: func(sc *scene) {
			// An open sub-issue whose labels are not read in full fills the
			// limit of the issues in progress, so this one is closed.
			labels := []string{"risk/low"}
			for i := range 100 {
				labels = append(labels, fmt.Sprintf("area/%d", i))
			}
			sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 31, Parent: 30, Closed: true, Labels: labels})
		},
		issue: 31, limit: "more than 100 labels",
	},
	{
		name: "a sub-issue with more blocked-by issues than cumin reads",
		add: func(sc *scene) {
			blockedBy := make([]int, 101)
			for i := range blockedBy {
				blockedBy[i] = 200 + i
			}
			sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 31, Parent: 30, Labels: []string{"cumin/status/ready", "risk/low"}, BlockedBy: blockedBy})
		},
		issue: 31, limit: "more than 100 blocked-by issues",
	},
	{
		name: "a sub-issue with more open closing pull requests than cumin reads",
		add: func(sc *scene) {
			// The label gives the sub-issue to the second query, and does
			// not fill the limit of the issues in progress.
			if err := sc.fake.SetLabels(sc.repo, 31, []string{"cumin/status/awaiting-decision", "risk/low"}); err != nil {
				panic(err)
			}
			for n := 40; n <= 42; n++ {
				sc.fake.AddPullRequest(sc.repo, &githubtest.PullRequest{Number: n, Author: implementerSlug, AuthorIsBot: true, Closes: []int{31}})
			}
		},
		issue: 31, limit: "more than 2 open closing pull requests",
	},
}

// addUnread adds the requirement issue #30 with the sub-issue #31, and puts
// one of them over a read limit.
func (sc *scene) addUnread(unread unreadIssue) {
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 30, Labels: []string{githubtest.RequirementLabel, "cumin/status/implementing"}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 31, Parent: 30, Labels: []string{"cumin/status/ready", "risk/low"}})
	unread.add(sc)
}

// An issue that cumin cannot read in full does not stop the poll of the
// repository: the poll requests the ready issue beside it and returns no
// error. cumin decides nothing for the unread requirement issue and its
// sub-issues: no label changes and no agent starts for them. The log names
// the issue and the limit (cumin-core.md, "Issues that cumin cannot read in
// full").
func TestPoll_AnIssueOverAReadLimitIsLeftOutAndTheOtherIssuesGoOn(t *testing.T) {
	for _, unread := range unreadIssues {
		t.Run(unread.name, func(t *testing.T) {
			sc := newScene(t)
			sc.addUnlinkedPullRequest(21, sc.remoteHead)
			sc.addUnread(unread)
			before := map[int][]string{}
			for _, number := range []int{30, 31} {
				before[number] = sc.fake.Issue(sc.repo, number).Labels
			}
			service := sc.service()

			if err := service.Poll(context.Background()); err != nil {
				t.Fatalf("poll: %v", err)
			}
			service.Wait()

			if n := sc.agentRuns(t); n != 1 {
				t.Errorf("%d agent runs, want 1: the run of #10", n)
			}
			if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", "cumin/status/checking"}) {
				t.Errorf("labels of #10 = %v, want risk/low and cumin/status/checking", got)
			}
			for number, want := range before {
				if got := sc.fake.Issue(sc.repo, number).Labels; !slices.Equal(got, want) {
					t.Errorf("labels of #%d = %v, want them unchanged: %v", number, got, want)
				}
			}
			for _, r := range sc.fake.Requests() {
				if r.Method == http.MethodPut && strings.HasSuffix(r.Path, "/labels") && r.Path != putLabelsPath {
					t.Errorf("a label change on %s, want label changes of #10 only", r.Path)
				}
			}
			var line string
			for l := range strings.SplitSeq(sc.logs.String(), "\n") {
				if strings.Contains(l, "cumin cannot read the issue in full") {
					line = l
				}
			}
			for _, want := range []string{
				fmt.Sprintf(`"issue":%d`, unread.issue), `"requirement_issue":30`, fmt.Sprintf(`"limit":%q`, unread.limit),
			} {
				if !strings.Contains(line, want) {
					t.Errorf("the log line of the unread issue has no %s:\n%s", want, line)
				}
			}
		})
	}
}

// The work of an unread requirement issue still fills the limit of the
// issues in progress (max_issues_in_progress is 1 in the scene): no rule
// moves it, but it goes on. A sub-issue whose labels were not read in full
// counts too. The poll starts no ready issue beside it, and returns no error.
func TestPoll_TheWorkOfAnUnreadIssueFillsTheLimitOfIssuesInProgress(t *testing.T) {
	manyLabels := []string{"risk/low"}
	for i := range 100 {
		manyLabels = append(manyLabels, fmt.Sprintf("area/%d", i))
	}
	cases := map[string]func(sc *scene){
		"a sub-issue in checking with too many open closing pull requests": func(sc *scene) {
			sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 31, Parent: 30, Labels: []string{"cumin/status/checking", "risk/low"}})
			for n := 40; n <= 42; n++ {
				sc.fake.AddPullRequest(sc.repo, &githubtest.PullRequest{Number: n, Author: implementerSlug, AuthorIsBot: true, Closes: []int{31}})
			}
		},
		"a sub-issue in checking under a requirement issue with 37 sub-issues": func(sc *scene) {
			sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 31, Parent: 30, Labels: []string{"cumin/status/checking", "risk/low"}})
			for n := 32; n <= 67; n++ {
				sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: n, Parent: 30, Closed: true})
			}
		},
		"a sub-issue whose labels are not read in full": func(sc *scene) {
			sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 31, Parent: 30, Labels: manyLabels})
		},
	}
	for name, add := range cases {
		t.Run(name, func(t *testing.T) {
			sc := newScene(t)
			sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 30, Labels: []string{githubtest.RequirementLabel, "cumin/status/implementing"}})
			add(sc)
			service := sc.service()

			if err := service.Poll(context.Background()); err != nil {
				t.Fatalf("poll: %v", err)
			}
			service.Wait()

			if n := sc.agentRuns(t); n != 0 {
				t.Errorf("%d agent runs, want none: the unread issue fills the limit", n)
			}
			if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"cumin/status/ready", "risk/low"}) {
				t.Errorf("labels of #10 = %v, want it to stay ready", got)
			}
			if n := sc.labelChanges(); n != 0 {
				t.Errorf("%d label changes, want none", n)
			}
		})
	}
}

// Every other failure of a read stays an error of the poll, also beside an
// issue over a read limit: a GraphQL error, and a wrong .cumin/config.toml.
func TestPoll_AnotherFailureOfAReadStillFailsThePoll(t *testing.T) {
	failures := map[string]func(sc *scene){
		"a GraphQL error": func(sc *scene) {
			sc.fake.FailNext(http.MethodPost, "/graphql", http.StatusNotFound)
		},
		"a wrong .cumin/config.toml": func(sc *scene) {
			sc.fake.SetFile(sc.repo, ".cumin/config.toml", wrongFile(`work_dir = "/tmp/elsewhere"`))
		},
	}
	for name, fail := range failures {
		t.Run(name, func(t *testing.T) {
			sc := newScene(t)
			sc.addUnread(unreadIssues[0])
			fail(sc)
			service := sc.service()

			err := service.Poll(context.Background())
			service.Wait()
			if err == nil {
				t.Fatal("the poll succeeded, want the failure of the read")
			}
			if n := sc.agentRuns(t); n != 0 {
				t.Errorf("%d agent runs, want none", n)
			}
			if n := sc.labelChanges(); n != 0 {
				t.Errorf("%d label changes, want none", n)
			}
		})
	}
}
