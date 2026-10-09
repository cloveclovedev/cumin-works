package workflow

// This file is pure, like domain.go: the questions of the poll about a
// whole repository. It holds whether the repository is in work and whether
// its poll is due, whether cumin moves on without a Maintainer, the issues
// to clean up, and "copy the labels to the pull request".

import (
	"maps"
	"slices"
	"strings"
	"time"
)

// HasIssueInWork reports whether an issue of the repository is in work:
// an open requirement issue with cumin/status/ready,
// cumin/status/planning, or cumin/status/accepting, or an open sub-issue with cumin/status/ready,
// cumin/status/implementing, cumin/status/checking,
// cumin/status/reviewing, or cumin/status/merging. An issue that waits for
// a Maintainer is not in work. A requirement issue in
// cumin/status/awaiting-plan-review with every sub-issue closed is in work:
// cumin moves it on with "request the acceptance check".
func (s Snapshot) HasIssueInWork() bool {
	for _, requirement := range s.RequirementIssues {
		if slices.Contains(requirement.Labels, LabelReady) || slices.Contains(requirement.Labels, LabelPlanning) ||
			slices.Contains(requirement.Labels, LabelAccepting) || planReviewMovesOn(requirement) {
			return true
		}
		for _, sub := range requirement.SubIssues {
			if sub.Closed {
				continue
			}
			for _, label := range []string{LabelReady, LabelImplementing, LabelChecking, LabelReviewing, LabelMerging} {
				if slices.Contains(sub.Labels, label) {
					return true
				}
			}
		}
	}
	return false
}

// LastPoll is what the last poll of a repository left for the decision on
// its next poll. The zero value means that no poll ran yet.
type LastPoll struct {
	// Ran says that a poll of the repository ran.
	Ran bool
	// Failed says that the poll, or one of its actions, failed.
	Failed bool
	// Acted says that the poll took an action.
	Acted bool
	// IssueInWork is Snapshot.HasIssueInWork of the snapshot of the poll.
	IssueInWork bool
}

// RepositoryInWork decides whether a repository is in work, so that cumin
// polls it at every poll interval. agentRun says that an agent run of the
// repository is in progress, or that one ended since the last poll. A
// repository that is not in work has only issues that wait for a Maintainer,
// and cumin polls it at the idle poll interval.
func RepositoryInWork(last LastPoll, agentRun bool) bool {
	return agentRun || !last.Ran || last.Failed || last.Acted || last.IssueInWork
}

// PollIsDue decides, at a tick of the poll interval, whether cumin polls a
// repository now. A repository in work is polled at every tick. A
// repository that is not in work is polled at the tick that is nearest to
// the idle poll interval after its last poll: half a poll interval of
// tolerance keeps a tick that comes a moment early from waiting one more
// tick.
func PollIsDue(inWork bool, sinceLastPoll, pollInterval, idlePollInterval time.Duration) bool {
	return inWork || sinceLastPoll >= idlePollInterval-pollInterval/2
}

// IssuesToCleanUp are the closed sub-issues of the snapshot whose agent
// does not run now. The Host keeps nothing for them (agent-run.md, the
// topic on the work directory).
func IssuesToCleanUp(snapshot Snapshot) []int {
	var numbers []int
	for _, requirement := range snapshot.RequirementIssues {
		for _, sub := range requirement.SubIssues {
			if sub.Closed && !snapshot.Running[sub.Number] {
				numbers = append(numbers, sub.Number)
			}
		}
	}
	return numbers
}

// labelCopies returns the actions of "copy the labels to the pull
// request": one for each open pull request that closes a sub-issue and
// whose copied labels differ from the issue.
// The copy is of the labels in the snapshot, so a label that this poll
// changes reaches the pull request at the next poll. No other rule reads
// the labels of a pull request (principle 5). A pull request that closes two
// issues follows the one with the lowest number, so that the two do not
// replace each other's labels at every poll, and the order of the snapshot
// changes nothing.
func labelCopies(snapshot Snapshot) []Action {
	type source struct {
		issue int
		pr    PullRequest
		want  []string
	}
	sources := map[int]source{}
	for _, requirement := range snapshot.RequirementIssues {
		for _, sub := range requirement.SubIssues {
			for _, pr := range sub.PullRequests {
				if old, ok := sources[pr.Number]; ok && old.issue < sub.Number {
					continue
				}
				sources[pr.Number] = source{issue: sub.Number, pr: pr, want: PullRequestLabels(sub.Labels, pr.Labels)}
			}
		}
	}
	var actions []Action
	for _, number := range slices.Sorted(maps.Keys(sources)) {
		src := sources[number]
		if sameLabels(src.want, src.pr.Labels) {
			continue
		}
		actions = append(actions, CopyLabels{Issue: src.issue, PullRequest: number, Labels: src.want})
	}
	return actions
}

// PullRequestLabels returns the labels that a pull request has after "copy
// the labels to the pull request": its own labels that are neither
// cumin/status/* nor risk/*, then those two kinds from the issue.
func PullRequestLabels(issue, pullRequest []string) []string {
	after := []string{}
	for _, label := range pullRequest {
		if !isCopiedLabel(label) {
			after = append(after, label)
		}
	}
	for _, label := range issue {
		if isCopiedLabel(label) {
			after = append(after, label)
		}
	}
	return after
}

// isCopiedLabel reports whether "copy the labels to the pull request"
// copies the label from the issue.
func isCopiedLabel(name string) bool {
	return IsStatusLabel(name) || strings.HasPrefix(name, riskLabelPrefix)
}

// sameLabels reports whether a and b hold the same labels, in any order.
func sameLabels(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(slices.Compact(a), slices.Compact(b))
}

// MovesWithoutMaintainer reports whether an issue exists that cumin moves on
// without a Maintainer, so that cumin is not waiting (issue-states.md, the
// table under "tell that cumin waits"): an open sub-issue that waits for
// the required checks,
// an issue in planning, implementing, reviewing, accepting, or merging,
// a requirement issue in cumin/status/awaiting-plan-review with every
// sub-issue closed ("request the acceptance check" moves it on),
// or a ready issue that can start and waits only for room under the limit.
// A ready issue with an open blocked-by issue, a ready issue whose ready
// another account than a Maintainer added, a status label that another
// account than cumin-core or a Maintainer added, and an issue that waits
// for a Maintainer do not count. A ready that was not read counts: the poll
// reads it when a slot is free.
func (s Snapshot) MovesWithoutMaintainer() bool {
	if s.HasIssueChecking() {
		return true
	}
	// The next poll decides the way out of a working label from the facts,
	// also when no agent runs.
	for _, requirement := range s.RequirementIssues {
		if (slices.Contains(requirement.Labels, LabelPlanning) || slices.Contains(requirement.Labels, LabelAccepting)) &&
			!statusOfAnother(requirement) {
			return true
		}
		if planReviewMovesOn(requirement) && !statusOfAnother(requirement) {
			return true
		}
		for _, sub := range requirement.SubIssues {
			if sub.Closed || subStatusOfAnother(sub) {
				continue
			}
			for _, label := range []string{LabelImplementing, LabelReviewing, LabelMerging} {
				if slices.Contains(sub.Labels, label) {
					return true
				}
			}
		}
	}
	for _, plan := range requirementCandidates(s) {
		if requirement, _ := s.RequirementIssue(plan.Number); !readyOfAnother(requirement.ReadyRead, requirement.ReadyIssueOwner) {
			return true
		}
	}
	for _, claim := range subIssueCandidates(s) {
		if sub, _ := s.SubIssue(claim.Number); !readyOfAnother(sub.ReadyRead, sub.ReadyIssueOwner) {
			return true
		}
	}
	return false
}

// settingsSource names where the settings of a poll came from, for the log.
func settingsSource(fromRepository bool) string {
	if fromRepository {
		return "repository"
	}
	return "host"
}
