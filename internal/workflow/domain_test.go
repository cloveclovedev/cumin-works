package workflow

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

// I1 (issue-states.md): claim an open sub-issue with cumin/status/ready
// whose blocked-by issues are all closed, lowest number first, within the
// limit of issues in progress.
func TestDecide_I1(t *testing.T) {
	ready := func(number int, blockedBy ...BlockedBy) SubIssue {
		return SubIssue{Number: number, Labels: []string{LabelReady, "risk/low"}, BlockedBy: blockedBy}
	}
	withStatus := func(number int, status string) SubIssue {
		return SubIssue{Number: number, Labels: []string{status, "risk/low"}}
	}
	requirement := func(number int, labels []string, subs ...SubIssue) RequirementIssue {
		return RequirementIssue{Number: number, Labels: append([]string{LabelRequirement}, labels...), SubIssues: subs}
	}
	implementing := []string{LabelImplementing}

	tests := []struct {
		name          string
		snapshot      Snapshot
		maxInProgress int
		want          []Action
	}{
		{
			name:          "no ready sub-issue gives no action",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, implementing, withStatus(10, LabelAwaitingOwnerReview))}},
			maxInProgress: 1,
		},
		{
			name:          "one ready sub-issue gives one claim",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, implementing, ready(10))}},
			maxInProgress: 1,
			want:          []Action{Claim{Number: 10, RequirementIssue: 6}},
		},
		{
			name:          "two ready sub-issues with limit 1 give the lower number",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, implementing, ready(12), ready(10))}},
			maxInProgress: 1,
			want:          []Action{Claim{Number: 10, RequirementIssue: 6}},
		},
		{
			name:          "two ready sub-issues with limit 2 give both in ascending order",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, implementing, ready(12), ready(10))}},
			maxInProgress: 2,
			want:          []Action{Claim{Number: 10, RequirementIssue: 6}, Claim{Number: 12, RequirementIssue: 6}},
		},
		{
			name: "the order is by issue number across requirement issues",
			snapshot: Snapshot{RequirementIssues: []RequirementIssue{
				requirement(6, implementing, ready(20)),
				requirement(7, implementing, ready(11)),
			}},
			maxInProgress: 1,
			want:          []Action{Claim{Number: 11, RequirementIssue: 7}},
		},
		{
			name:          "a ready sub-issue with an open blocker is skipped and the next one is taken",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, implementing, ready(10, BlockedBy{Number: 9}), ready(11))}},
			maxInProgress: 1,
			want:          []Action{Claim{Number: 11, RequirementIssue: 6}},
		},
		{
			name:          "a ready sub-issue whose blockers are all closed is claimed",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, implementing, ready(10, BlockedBy{Number: 8, Closed: true}, BlockedBy{Number: 9, Closed: true}))}},
			maxInProgress: 1,
			want:          []Action{Claim{Number: 10, RequirementIssue: 6}},
		},
		{
			name:          "an implementing sub-issue fills limit 1",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, implementing, withStatus(10, LabelImplementing), ready(11))}},
			maxInProgress: 1,
		},
		{
			name:          "an awaiting-checks sub-issue fills limit 1",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, implementing, withStatus(10, LabelAwaitingChecks), ready(11))}},
			maxInProgress: 1,
		},
		{
			name:          "a reviewing sub-issue fills limit 1",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, implementing, withStatus(10, LabelReviewing), ready(11))}},
			maxInProgress: 1,
		},
		{
			name:          "a sub-issue that waits for the Owner does not fill the limit",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, implementing, withStatus(10, LabelAwaitingOwnerDecision), ready(11))}},
			maxInProgress: 1,
			want:          []Action{Claim{Number: 11, RequirementIssue: 6}},
		},
		{
			name:          "a closed implementing sub-issue does not fill the limit",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, implementing, SubIssue{Number: 10, Closed: true, Labels: []string{LabelImplementing}}, ready(11))}},
			maxInProgress: 1,
			want:          []Action{Claim{Number: 11, RequirementIssue: 6}},
		},
		{
			name: "a requirement issue in planning fills limit 1",
			snapshot: Snapshot{RequirementIssues: []RequirementIssue{
				requirement(6, implementing, ready(10)),
				requirement(7, []string{LabelPlanning}),
			}},
			maxInProgress: 1,
		},
		{
			name: "a requirement issue in implementing does not fill the limit",
			snapshot: Snapshot{RequirementIssues: []RequirementIssue{
				requirement(6, implementing, ready(10)),
				requirement(7, implementing),
			}},
			maxInProgress: 1,
			want:          []Action{Claim{Number: 10, RequirementIssue: 6}},
		},
		{
			name:          "a closed sub-issue with ready gives no claim",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, implementing, SubIssue{Number: 10, Closed: true, Labels: []string{LabelReady}})}},
			maxInProgress: 1,
		},
		{
			name:          "ready on a requirement issue gives a plan, not a claim",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, []string{LabelReady})}},
			maxInProgress: 1,
			want:          []Action{Plan{Number: 6}},
		},
		{
			name:          "an empty snapshot gives no action",
			snapshot:      Snapshot{},
			maxInProgress: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Decide(tt.snapshot, tt.maxInProgress, nil)
			if !slices.Equal(got, tt.want) {
				t.Errorf("Decide = %+v, want %+v", got, tt.want)
			}
			// The same snapshot in another order gives the same actions.
			shuffled := shuffle(tt.snapshot)
			if again := Decide(shuffled, tt.maxInProgress, nil); !slices.Equal(again, tt.want) {
				t.Errorf("Decide on the shuffled snapshot = %+v, want %+v", again, tt.want)
			}
		})
	}
}

// R1 (issue-states.md): plan an open requirement issue with
// cumin/status/ready whose blocked-by issues are all closed, with or
// without sub-issues. R1 and I1 share the limit of issues in progress.
func TestDecide_R1(t *testing.T) {
	requirement := func(number int, status string, blockedBy []BlockedBy, subs ...SubIssue) RequirementIssue {
		return RequirementIssue{Number: number, Labels: []string{LabelRequirement, status}, BlockedBy: blockedBy, SubIssues: subs}
	}
	readySub := SubIssue{Number: 10, Labels: []string{LabelReady, "risk/low"}}
	open := []BlockedBy{{Number: 3}}
	closed := []BlockedBy{{Number: 3, Closed: true}}

	tests := []struct {
		name          string
		snapshot      Snapshot
		maxInProgress int
		want          []Action
	}{
		{
			name:          "a ready requirement issue gives one plan",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, LabelReady, nil)}},
			maxInProgress: 1,
			want:          []Action{Plan{Number: 6}},
		},
		{
			name:          "a ready requirement issue with sub-issues gives a plan",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, LabelReady, nil, SubIssue{Number: 10, Closed: true})}},
			maxInProgress: 1,
			want:          []Action{Plan{Number: 6}},
		},
		{
			name:          "an open blocked-by issue gives no plan",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, LabelReady, open)}},
			maxInProgress: 1,
		},
		{
			name:          "a closed blocked-by issue gives a plan",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, LabelReady, closed)}},
			maxInProgress: 1,
			want:          []Action{Plan{Number: 6}},
		},
		{
			name:          "a requirement issue in planning gives no plan and fills the limit",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, LabelPlanning, nil), requirement(7, LabelReady, nil)}},
			maxInProgress: 1,
		},
		{
			name:          "a requirement issue in planning fills the limit for a sub-issue as well",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, LabelPlanning, nil), requirement(7, LabelImplementing, nil, readySub)}},
			maxInProgress: 1,
		},
		{
			name:          "a plan and a claim share the limit, lowest issue number first",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(12, LabelReady, nil), requirement(7, LabelImplementing, nil, readySub)}},
			maxInProgress: 1,
			want:          []Action{Claim{Number: 10, RequirementIssue: 7}},
		},
		{
			name:          "with room for two, the plan follows the claim",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(12, LabelReady, nil), requirement(7, LabelImplementing, nil, readySub)}},
			maxInProgress: 2,
			want:          []Action{Claim{Number: 10, RequirementIssue: 7}, Plan{Number: 12}},
		},
		{
			name:          "awaiting-owner-decision without ready gives no plan",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, LabelAwaitingOwnerDecision, nil)}},
			maxInProgress: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Decide(tt.snapshot, tt.maxInProgress, nil)
			if !slices.Equal(got, tt.want) {
				t.Errorf("Decide = %+v, want %+v", got, tt.want)
			}
			if again := Decide(shuffle(tt.snapshot), tt.maxInProgress, nil); !slices.Equal(again, tt.want) {
				t.Errorf("Decide on the shuffled snapshot = %+v, want %+v", again, tt.want)
			}
		})
	}
}

func TestLabelsAfterPlan_R1(t *testing.T) {
	got := LabelsAfterPlan([]string{LabelRequirement, LabelReady})
	if want := []string{LabelRequirement, LabelPlanning}; !slices.Equal(got, want) {
		t.Errorf("LabelsAfterPlan = %v, want %v", got, want)
	}
}

// R2 (issue-states.md): one or more sub-issues, each with exactly one risk
// label. The first failing sub-issue by number is named.
func TestVerifySplit_R2(t *testing.T) {
	sub := func(number int, labels ...string) SubIssue { return SubIssue{Number: number, Labels: labels} }
	tests := []struct {
		name string
		subs []SubIssue
		want SplitVerification
	}{
		{"no sub-issue", nil, SplitVerification{Failure: SplitNoSubIssue}},
		{"one risk label each", []SubIssue{sub(10, "risk/low"), sub(11, "risk/high", LabelReady)}, SplitVerification{Passed: true}},
		{"a closed sub-issue counts as well", []SubIssue{{Number: 10, Closed: true}}, SplitVerification{Failure: SplitNoRiskLabel, SubIssue: 10}},
		{"the lowest failing number is named", []SubIssue{sub(12), sub(11, "risk/low", "risk/medium")}, SplitVerification{Failure: SplitTwoRiskLabels, SubIssue: 11}},
		{"an owner task with risk/high passes", []SubIssue{sub(10, LabelOwnerTask, "risk/high")}, SplitVerification{Passed: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := VerifySplit(RequirementIssue{Number: 6, SubIssues: tt.subs}); got != tt.want {
				t.Errorf("VerifySplit = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// shuffle returns a copy of the snapshot with the issues in a random order.
func shuffle(snapshot Snapshot) Snapshot {
	r := rand.New(rand.NewPCG(1, 2))
	copied := Snapshot{RequirementIssues: slices.Clone(snapshot.RequirementIssues)}
	r.Shuffle(len(copied.RequirementIssues), func(i, j int) {
		copied.RequirementIssues[i], copied.RequirementIssues[j] = copied.RequirementIssues[j], copied.RequirementIssues[i]
	})
	for i := range copied.RequirementIssues {
		subs := slices.Clone(copied.RequirementIssues[i].SubIssues)
		r.Shuffle(len(subs), func(a, b int) { subs[a], subs[b] = subs[b], subs[a] })
		copied.RequirementIssues[i].SubIssues = subs
	}
	return copied
}

func TestIsStatusLabel(t *testing.T) {
	for name, want := range map[string]bool{
		LabelReady: true, LabelImplementing: true, LabelAwaitingOwnerDecision: true,
		LabelRequirement: false, "risk/low": false, "status": false,
	} {
		if got := IsStatusLabel(name); got != want {
			t.Errorf("IsStatusLabel(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestLabelsAfterClaim(t *testing.T) {
	got := LabelsAfterClaim([]string{"cumin/status/awaiting-owner-decision", "risk/low", "cumin/status/ready", "question"})
	if want := []string{"risk/low", "question", LabelImplementing}; !slices.Equal(got, want) {
		t.Errorf("LabelsAfterClaim = %v, want %v", got, want)
	}
	if got := LabelsAfterClaim(nil); !slices.Equal(got, []string{LabelImplementing}) {
		t.Errorf("LabelsAfterClaim(nil) = %v", got)
	}
}

func TestReplaceStatusLabel(t *testing.T) {
	got := ReplaceStatusLabel([]string{"cumin/status/implementing", "risk/low", "question"}, LabelAwaitingChecks)
	if want := []string{"risk/low", "question", LabelAwaitingChecks}; !slices.Equal(got, want) {
		t.Errorf("ReplaceStatusLabel = %v, want %v", got, want)
	}
}

// I2 (issue-states.md): after done, an open pull request closes the
// issue, its author is the Implementer App, and the head of the worktree
// is pushed.
func TestVerifyDone_I2(t *testing.T) {
	const bot = "example-implementer[bot]"
	const head = "2222222222222222222222222222222222222222"
	pr := func(number int, author, headCommit string) PullRequest {
		return PullRequest{Number: number, Author: author, HeadCommit: headCommit}
	}
	tests := []struct {
		name      string
		pulls     []PullRequest
		localHead string
		want      Verification
	}{
		{"the pull request of the bot at the pushed head passes", []PullRequest{pr(21, bot, head)}, head, Verification{Passed: true, PullRequest: 21}},
		{"no pull request", nil, head, Verification{Failure: FailureNoOpenPullRequest}},
		{"another author", []PullRequest{pr(21, "octocat", head)}, head, Verification{Failure: FailureAuthorMismatch, PullRequest: 21}},
		{"an author without an account never matches", []PullRequest{pr(21, "", head)}, head, Verification{Failure: FailureAuthorMismatch, PullRequest: 21}},
		{"a local commit that is not pushed", []PullRequest{pr(21, bot, head)}, "3333333333333333333333333333333333333333", Verification{Failure: FailureHeadNotPushed, PullRequest: 21}},
		{"an unknown local head never matches", []PullRequest{pr(21, bot, head)}, "", Verification{Failure: FailureHeadNotPushed, PullRequest: 21}},
		{"two open pull requests: the highest number is checked", []PullRequest{pr(21, bot, head), pr(25, "octocat", head)}, head, Verification{Failure: FailureAuthorMismatch, PullRequest: 25}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := VerifyDone(SubIssue{Number: 10, PullRequests: tt.pulls}, bot, tt.localHead)
			if got != tt.want {
				t.Errorf("VerifyDone = %+v, want %+v", got, tt.want)
			}
		})
	}
	if got := VerifyDone(SubIssue{PullRequests: []PullRequest{pr(21, "", head)}}, "", head); got.Passed {
		t.Error("an empty implementer login matched an empty author")
	}
	for _, f := range []VerificationFailure{FailureNone, FailureNoOpenPullRequest, FailureAuthorMismatch, FailureHeadNotPushed} {
		if s := f.String(); s == "" || strings.HasPrefix(s, "VerificationFailure(") {
			t.Errorf("%d has no name", int(f))
		}
	}
}

// I11 (issue-states.md): the cumin/status/* and risk/* labels of an open
// pull request that closes a sub-issue become those of the issue. Its other
// labels stay, and equal labels give no action.
func TestDecide_I11(t *testing.T) {
	snapshotOf := func(issue []string, prs ...PullRequest) Snapshot {
		return Snapshot{RequirementIssues: []RequirementIssue{{
			Number: 6, Labels: []string{LabelRequirement, LabelImplementing},
			SubIssues: []SubIssue{{Number: 10, Labels: issue, PullRequests: prs}},
		}}}
	}
	checks := []string{LabelAwaitingChecks, "risk/medium"}

	tests := []struct {
		name     string
		snapshot Snapshot
		want     []Action
	}{
		{
			name:     "a pull request without labels gets the status and the risk",
			snapshot: snapshotOf(checks, PullRequest{Number: 21}),
			want:     []Action{CopyLabels{Issue: 10, PullRequest: 21, Labels: []string{LabelAwaitingChecks, "risk/medium"}}},
		},
		{
			name:     "equal labels in another order give no action",
			snapshot: snapshotOf(checks, PullRequest{Number: 21, Labels: []string{"risk/medium", "docs", LabelAwaitingChecks}}),
		},
		{
			name:     "an old status and an old risk are replaced, and other labels stay",
			snapshot: snapshotOf(checks, PullRequest{Number: 21, Labels: []string{"docs", LabelImplementing, "risk/low"}}),
			want:     []Action{CopyLabels{Issue: 10, PullRequest: 21, Labels: []string{"docs", LabelAwaitingChecks, "risk/medium"}}},
		},
		{
			name:     "a status that the Owner added to the pull request is removed",
			snapshot: snapshotOf(checks, PullRequest{Number: 21, Labels: []string{LabelAwaitingChecks, LabelReady, "risk/medium"}}),
			want:     []Action{CopyLabels{Issue: 10, PullRequest: 21, Labels: []string{LabelAwaitingChecks, "risk/medium"}}},
		},
		{
			name:     "an issue without a risk label removes the risk of the pull request",
			snapshot: snapshotOf([]string{LabelAwaitingChecks}, PullRequest{Number: 21, Labels: []string{LabelAwaitingChecks, "risk/low"}}),
			want:     []Action{CopyLabels{Issue: 10, PullRequest: 21, Labels: []string{LabelAwaitingChecks}}},
		},
		{
			name:     "labels of the issue that are not copied stay off the pull request",
			snapshot: snapshotOf([]string{LabelOwnerTask, LabelReady, "risk/low"}, PullRequest{Number: 21, Labels: []string{LabelReady, "risk/low"}}),
		},
		{
			name: "each open pull request that closes the issue is made equal",
			snapshot: snapshotOf(checks,
				PullRequest{Number: 21, Labels: []string{LabelAwaitingChecks, "risk/medium"}},
				PullRequest{Number: 22}),
			want: []Action{CopyLabels{Issue: 10, PullRequest: 22, Labels: []string{LabelAwaitingChecks, "risk/medium"}}},
		},
		{
			name: "a pull request that closes two issues follows the lower number, in any order",
			snapshot: Snapshot{RequirementIssues: []RequirementIssue{{
				Number: 6, Labels: []string{LabelRequirement, LabelImplementing},
				SubIssues: []SubIssue{
					{Number: 11, Labels: []string{LabelReviewing, "risk/low"}, PullRequests: []PullRequest{{Number: 21}}},
					{Number: 10, Labels: checks, PullRequests: []PullRequest{{Number: 21}}},
				},
			}}},
			want: []Action{CopyLabels{Issue: 10, PullRequest: 21, Labels: []string{LabelAwaitingChecks, "risk/medium"}}},
		},
		{
			name: "the actions are in the order of the pull request numbers",
			snapshot: Snapshot{RequirementIssues: []RequirementIssue{{
				Number: 6, Labels: []string{LabelRequirement, LabelImplementing},
				SubIssues: []SubIssue{
					{Number: 10, Labels: checks, PullRequests: []PullRequest{{Number: 23}}},
					{Number: 11, Labels: checks, PullRequests: []PullRequest{{Number: 22}}},
				},
			}}},
			want: []Action{
				CopyLabels{Issue: 11, PullRequest: 22, Labels: []string{LabelAwaitingChecks, "risk/medium"}},
				CopyLabels{Issue: 10, PullRequest: 23, Labels: []string{LabelAwaitingChecks, "risk/medium"}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []Action
			for _, action := range Decide(tt.snapshot, 1, nil) {
				if _, ok := action.(CopyLabels); ok {
					got = append(got, action)
				}
			}
			if !slices.EqualFunc(got, tt.want, func(a, b Action) bool {
				ca, okA := a.(CopyLabels)
				cb, okB := b.(CopyLabels)
				return okA && okB && ca.Issue == cb.Issue && ca.PullRequest == cb.PullRequest && slices.Equal(ca.Labels, cb.Labels)
			}) {
				t.Errorf("Decide = %#v, want %#v", got, tt.want)
			}
		})
	}
}

// The labels of a pull request decide nothing (issue-states.md, principle 5):
// a pull request with cumin/status/ready does not make its issue claimable,
// and a pull request without it does not stop a ready issue.
func TestDecide_TheLabelsOfAPullRequestDecideNothing(t *testing.T) {
	sub := func(labels []string, prLabels []string) Snapshot {
		return Snapshot{RequirementIssues: []RequirementIssue{{
			Number: 6, Labels: []string{LabelRequirement, LabelImplementing},
			SubIssues: []SubIssue{{Number: 10, Labels: labels, PullRequests: []PullRequest{{Number: 21, Labels: prLabels}}}},
		}}}
	}
	claims := func(actions []Action) int {
		n := 0
		for _, action := range actions {
			if _, ok := action.(Claim); ok {
				n++
			}
		}
		return n
	}
	if n := claims(Decide(sub([]string{LabelAwaitingOwnerReview, "risk/low"}, []string{LabelReady, "risk/low"}), 1, nil)); n != 0 {
		t.Errorf("%d claims for a ready pull request of an issue in review, want 0", n)
	}
	if n := claims(Decide(sub([]string{LabelReady, "risk/low"}, []string{LabelAwaitingOwnerDecision}), 1, nil)); n != 1 {
		t.Errorf("%d claims for a ready issue, want 1", n)
	}
}
