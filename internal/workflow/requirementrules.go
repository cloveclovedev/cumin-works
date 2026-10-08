package workflow

// This file is pure, like domain.go: the requirement issue. It holds the
// moves of a requirement issue ("mark the requirement as in work", "ask
// about the remaining sub-issues"), the candidates of "request the split"
// and of "request the acceptance check", the ways out of
// cumin/status/planning (SplitEnd) and of cumin/status/accepting
// (AcceptanceEnd), and the rules on the actor of a status label.

import (
	"slices"
	"strings"
	"time"
)

// requirementMoves returns the actions "mark the requirement as in work"
// and "ask about the remaining sub-issues", and the way out of
// cumin/status/accepting (AcceptanceEnd) and of cumin/status/planning
// (SplitEnd), lowest requirement issue number first.
func requirementMoves(snapshot Snapshot) []Action {
	requirements := slices.Clone(snapshot.RequirementIssues)
	slices.SortFunc(requirements, func(a, b RequirementIssue) int { return a.Number - b.Number })
	var actions []Action
	for _, requirement := range requirements {
		switch {
		case startsImplementing(requirement):
			actions = append(actions, StartRequirement{Number: requirement.Number})
		case remainingNeedReview(requirement):
			actions = append(actions, ReviewRemaining{Number: requirement.Number})
		default:
			running := snapshot.Running[requirement.Number]
			if action := AcceptanceEnd(requirement, running); action != nil {
				actions = append(actions, action)
			} else if action := SplitEnd(requirement, running); action != nil {
				actions = append(actions, action)
			}
		}
	}
	return actions
}

// startsImplementing is the condition of "mark the requirement as in work".
// Without a status label, an open sub-issue with
// cumin/status/ready is enough: a Maintainer wrote the sub-issues without the
// Planner. In cumin/status/awaiting-plan-review and in
// cumin/status/awaiting-acceptance, cumin/status/ready must have been added
// after that label. A sub-issue that kept its
// cumin/status/ready from an earlier split must not move the requirement
// issue before a Maintainer looked at the new sub-issues (issue-states.md, the
// text below the table).
func startsImplementing(requirement RequirementIssue) bool {
	switch statusLabel(requirement.Labels) {
	case "":
		return slices.ContainsFunc(requirement.SubIssues, openReady)
	case LabelAwaitingPlanReview, LabelAwaitingAcceptance:
		if !requirement.LabelTimesRead {
			return false
		}
		return slices.ContainsFunc(requirement.SubIssues, func(sub SubIssue) bool {
			return openReady(sub) && sub.ReadyAt.After(requirement.ReviewAt)
		})
	}
	return false
}

// remainingNeedReview is the condition of "ask about the remaining
// sub-issues": the requirement issue is in
// cumin/status/implementing, one or more sub-issues are open, and none of
// the open ones has a status label.
func remainingNeedReview(requirement RequirementIssue) bool {
	if statusLabel(requirement.Labels) != LabelImplementing {
		return false
	}
	open := 0
	for _, sub := range requirement.SubIssues {
		if sub.Closed {
			continue
		}
		if statusLabel(sub.Labels) != "" {
			return false
		}
		open++
	}
	return open > 0
}

// NeedsComments reports whether "request the acceptance check" or "ask for
// the acceptance" needs the comments of the requirement issue: it is in
// cumin/status/accepting, or it is in cumin/status/implementing or in
// cumin/status/awaiting-plan-review and has one or more sub-issues, all
// closed.
func NeedsComments(requirement RequirementIssue) bool {
	if statusLabel(requirement.Labels) == LabelAccepting {
		return !statusOfAnother(requirement)
	}
	return everySubIssueClosed(requirement)
}

// everySubIssueClosed reports whether the requirement issue is in a
// starting state of "request the acceptance check"
// (cumin/status/implementing or cumin/status/awaiting-plan-review) with one
// or more sub-issues, all closed. In cumin/status/awaiting-plan-review, the
// last open sub-issue was an Owner task, or a Maintainer closed it by hand.
func everySubIssueClosed(requirement RequirementIssue) bool {
	status := statusLabel(requirement.Labels)
	if status != LabelImplementing && status != LabelAwaitingPlanReview || len(requirement.SubIssues) == 0 {
		return false
	}
	for _, sub := range requirement.SubIssues {
		if !sub.Closed {
			return false
		}
	}
	return true
}

// lastClose is when the last sub-issue closed.
func lastClose(requirement RequirementIssue) time.Time {
	var last time.Time
	for _, sub := range requirement.SubIssues {
		if sub.ClosedAt.After(last) {
			last = sub.ClosedAt
		}
	}
	return last
}

// checked reports whether an acceptance check comment was written after the
// last sub-issue closed. An older comment belongs to an earlier round: a
// Maintainer added sub-issues after it (issue-states.md, the text below the
// table).
//
// A comment at the same second as the last close counts: the times of
// GitHub cannot order two events inside one second, and a check that never
// counts would ask again at every poll.
func checked(requirement RequirementIssue) bool {
	return requirement.CommentsRead && !requirement.AcceptanceCheckAt.IsZero() &&
		!requirement.AcceptanceCheckAt.Before(lastClose(requirement))
}

// AcceptanceEnd decides the way out of cumin/status/accepting from the
// facts on GitHub, for a requirement issue whose Planner does not run. The
// poll and the end of a Planner run both decide with it, so a restart of
// cumin during the check loses nothing.
//
//   - An acceptance check comment after the last close: ask for the
//     acceptance (Accept).
//   - A decision request of the Planner, written after the issue got
//     cumin/status/accepting: stop the acceptance check.
//   - Neither comment: request the acceptance check again, once for each
//     stay in cumin/status/accepting. The second time, stop the acceptance
//     check.
//
// It returns nil while the Planner runs, in every other state, while the
// comments or the label times were not read, and while the status label
// does not count (statusCounts): the next poll decides.
func AcceptanceEnd(requirement RequirementIssue, running bool) Action {
	if statusLabel(requirement.Labels) != LabelAccepting || running || !statusCounts(requirement) || !requirement.CommentsRead {
		return nil
	}
	switch {
	case checked(requirement):
		return Accept{Number: requirement.Number}
	case !requirement.LabelTimesRead:
		return nil
	case !requirement.QuestionAt.IsZero() && !requirement.QuestionAt.Before(requirement.ReviewAt):
		return StopAcceptance{Number: requirement.Number, Question: true}
	case requirement.AcceptanceRequestedAgain:
		return StopAcceptance{Number: requirement.Number}
	}
	return CheckAcceptance{Number: requirement.Number, Again: true}
}

// SplitEnd decides the way out of cumin/status/planning from the facts on
// GitHub, for a requirement issue whose Planner does not run. The poll and
// the end of a Planner run both decide with it, so a restart of cumin
// during the split, or a failed read after it, loses nothing.
//
//   - A decision request of the Planner, written after the issue got
//     cumin/status/planning: stop the split. cumin-core
//     posts the blocked_reason of the Planner, so its decision request
//     counts too (QuestionAt). The question
//     decides before the check of the split: an issue that is planned again
//     can hold sub-issues of an earlier split that pass the check.
//   - The split passes the check (VerifySplit) and a sub-issue is open: ask
//     for the plan review.
//   - The split passes the check and every sub-issue is closed: request the
//     acceptance check.
//   - The split fails the check: request the split again, once for each
//     stay in cumin/status/planning. The second time, stop the split.
//
// It returns nil while the Planner runs, in every other state, while the
// comments or the label times were not read, and while the status label
// does not count (statusCounts): the next poll decides.
func SplitEnd(requirement RequirementIssue, running bool) Action {
	if !SplitNeedsFacts(requirement, running) || !statusCounts(requirement) || !requirement.CommentsRead || !requirement.LabelTimesRead {
		return nil
	}
	verification := VerifySplit(requirement)
	switch {
	case !requirement.QuestionAt.IsZero() && !requirement.QuestionAt.Before(requirement.ReviewAt):
		return StopSplit{Number: requirement.Number, Question: true}
	case verification.Passed && SplitStatus(requirement) == LabelAccepting:
		return CheckAcceptance{Number: requirement.Number}
	case verification.Passed:
		return ReviewPlan{Number: requirement.Number}
	case requirement.SplitRequestedAgain:
		return StopSplit{Number: requirement.Number, Reason: SplitReason(verification)}
	}
	return Plan{Number: requirement.Number, Again: true}
}

// SplitNeedsFacts reports whether the way out of cumin/status/planning
// needs the comments and the label times of the requirement issue: it is in
// cumin/status/planning, and its Planner does not run. While the Planner
// runs, nothing is decided, so the poll reads nothing more. The same holds
// for a label that another account than cumin-core or a Maintainer added.
func SplitNeedsFacts(requirement RequirementIssue, running bool) bool {
	return statusLabel(requirement.Labels) == LabelPlanning && !running && !statusOfAnother(requirement)
}

// acceptanceChecks returns the starts of "request the acceptance check"
// before the limit: the requirement issue in cumin/status/implementing or
// in cumin/status/awaiting-plan-review, every sub-issue closed, the
// comments read, no acceptance check after the last close, the follow-up
// notes of the closed sub-issues written ("write the follow-up note"), and
// no agent of the requirement issue running.
func acceptanceChecks(snapshot Snapshot) []CheckAcceptance {
	var checks []CheckAcceptance
	for _, requirement := range snapshot.RequirementIssues {
		if everySubIssueClosed(requirement) && requirement.CommentsRead && !checked(requirement) &&
			requirement.FollowUpsDone && !snapshot.Running[requirement.Number] {
			checks = append(checks, CheckAcceptance{Number: requirement.Number})
		}
	}
	return checks
}

// AcceptanceCheckAt returns when the newest acceptance check comment was
// written: a comment of the Planner App whose first line is the heading
// "## Acceptance check" (issue-states.md, the text below the table). A
// comment of anyone else never counts. cumin does not read the result
// table.
func AcceptanceCheckAt(comments []Comment, planner string) time.Time {
	var newest time.Time
	for _, comment := range comments {
		if planner == "" || comment.Author != planner {
			continue
		}
		if strings.TrimSpace(firstLine(strings.TrimLeft(comment.Body, " \t\r\n"))) != acceptanceCheckHeading {
			continue
		}
		if comment.CreatedAt.After(newest) {
			newest = comment.CreatedAt
		}
	}
	return newest
}

// QuestionAt returns when the newest decision request of one of the authors
// was written: a comment whose first line starts with
// DecisionRequestHeading. The authors are the Planner App and, for the
// split, the App of cumin-core, which posts the blocked_reason of the
// Planner. An empty author matches nothing, and a comment of anyone else
// never counts.
func QuestionAt(comments []Comment, authors ...string) time.Time {
	var newest time.Time
	for _, comment := range comments {
		if comment.Author == "" || !slices.Contains(authors, comment.Author) || !strings.HasPrefix(strings.TrimSpace(firstLine(strings.TrimLeft(comment.Body, "\r\n"))), DecisionRequestHeading) {
			continue
		}
		if comment.CreatedAt.After(newest) {
			newest = comment.CreatedAt
		}
	}
	return newest
}

// acceptanceCheckHeading is the first heading of templates/acceptance-check.md.
const acceptanceCheckHeading = "## Acceptance check"

// StatusActor is the account of the newest event that added a status
// label. Login and Type are the ones of the event as GraphQL names them
// ("User" for a person, "Bot" for a GitHub App); both are empty when the
// account no longer exists or no event was found. Permission and UserType
// are the answer of GitHub on the permission of a person on the
// repository; they are empty for an account that is not a person.
type StatusActor struct {
	Login      string
	Type       string
	Permission string
	UserType   string
}

// StatusLabelCounts reports whether cumin treats a status label as a state
// (issue-states.md, the account that added a status label): the account of
// its newest label event is a Maintainer (IsMaintainer) or, for every label but
// cumin/status/ready, the cumin-core App. core is the login of the bot of
// cumin-core; GraphQL names a bot without "[bot]", so both forms match.
// Every rule that acts from a state calls it before it acts.
func StatusLabelCounts(label string, actor StatusActor, core string) bool {
	if actor.Login == "" {
		return false
	}
	if actor.Type == "User" {
		return IsMaintainer(actor.Permission, actor.UserType)
	}
	core = strings.TrimSuffix(core, "[bot]")
	return label != LabelReady && actor.Type == "Bot" && core != "" &&
		strings.TrimSuffix(actor.Login, "[bot]") == core
}

// StatusActorReads names the requirement issues whose newest status label
// the poll must read the actor of: the ones that cumin is about to act
// from, in cumin/status/planning or in cumin/status/accepting with no
// Planner running. While the Planner runs, and in every other state,
// nothing is decided from the label, so a poll with no such issue makes no
// read.
func StatusActorReads(snapshot Snapshot) []int {
	var numbers []int
	for _, requirement := range snapshot.RequirementIssues {
		status := statusLabel(requirement.Labels)
		if (status == LabelPlanning || status == LabelAccepting) && !snapshot.Running[requirement.Number] {
			numbers = append(numbers, requirement.Number)
		}
	}
	slices.Sort(numbers)
	return numbers
}

// statusCounts reports whether the actor of the status label was read and
// is the cumin-core App or a Maintainer. A rule that acts from the state holds
// only then.
func statusCounts(requirement RequirementIssue) bool {
	return requirement.StatusRead && requirement.StatusCounts
}

// statusOfAnother reports whether the actor of the status label was read
// and is neither the cumin-core App nor a Maintainer. Such an issue waits
// for a Maintainer.
func statusOfAnother(requirement RequirementIssue) bool {
	return requirement.StatusRead && !requirement.StatusCounts
}

// subStatusOfAnother reports whether the facts of the working label of a
// sub-issue were read, and say that another account than cumin-core or a
// Maintainer added that label. No rule moves such an issue. Facts that were not
// read do not say it.
func subStatusOfAnother(sub SubIssue) bool {
	return sub.Implementing != nil && !sub.Implementing.StatusCounts ||
		sub.Reviewing != nil && !sub.Reviewing.StatusCounts ||
		sub.Merging != nil && !sub.Merging.StatusCounts
}

// readyOfMaintainer reports whether the newest cumin/status/ready was read and
// is a Maintainer's. "request the split" and "request the implementation"
// hold only then (issue-states.md, the ready of a Maintainer).
func readyOfMaintainer(read bool, owner string) bool { return read && owner != "" }

// readyOfAnother reports whether the newest cumin/status/ready was read and
// is not a Maintainer's. Such an issue waits for a Maintainer.
func readyOfAnother(read bool, owner string) bool { return read && owner == "" }

// NeedsLabelTimes reports whether a rule needs the label times of the
// requirement issue: "mark the requirement as in work" needs them
// (startNeedsLabelTimes), the requirement issue is in
// cumin/status/accepting, whose end compares a decision request of the
// Planner with the time of that label, an open
// sub-issue waits in cumin/status/checking, whose wait is counted
// from the time of that label, or an open sub-issue in
// cumin/status/awaiting-merge-decision has a request for changes of a person
// on its head commit, which "send back for changes" compares with the time
// of that label. Only then does the poll read the times, so that the poll
// query keeps its cost.
func NeedsLabelTimes(requirement RequirementIssue) bool {
	return startNeedsLabelTimes(requirement) ||
		statusLabel(requirement.Labels) == LabelAccepting && !statusOfAnother(requirement) ||
		slices.ContainsFunc(requirement.SubIssues, openChecking) ||
		slices.ContainsFunc(requirement.SubIssues, openWithChangeRequest)
}

// startNeedsLabelTimes reports whether "mark the requirement as in work"
// needs the label times of the requirement issue: it waits in
// cumin/status/awaiting-plan-review or in
// cumin/status/awaiting-acceptance, and an open sub-issue carries
// cumin/status/ready.
func startNeedsLabelTimes(requirement RequirementIssue) bool {
	status := statusLabel(requirement.Labels)
	return (status == LabelAwaitingPlanReview || status == LabelAwaitingAcceptance) &&
		slices.ContainsFunc(requirement.SubIssues, openReady)
}

func openReady(sub SubIssue) bool {
	return !sub.Closed && slices.Contains(sub.Labels, LabelReady)
}

// readyRequirementIssues returns the plans of "request the split" before
// the limit: the candidates (requirementCandidates) whose newest
// cumin/status/ready a Maintainer added. A candidate whose ready is of
// another account, or was not read, is skipped.
func readyRequirementIssues(snapshot Snapshot) []Plan {
	var plans []Plan
	for _, plan := range requirementCandidates(snapshot) {
		requirement, _ := snapshot.RequirementIssue(plan.Number)
		if readyOfMaintainer(requirement.ReadyRead, requirement.ReadyIssueOwner) {
			plans = append(plans, plan)
		}
	}
	return plans
}

// requirementCandidates returns the candidates of "request the split"
// before the check of the Maintainer: open requirement issues with
// cumin/status/ready whose blocked-by issues are all closed. Whether the
// requirement issue has sub-issues does not matter (issue-states.md,
// "request the split").
func requirementCandidates(snapshot Snapshot) []Plan {
	var plans []Plan
	for _, requirement := range snapshot.RequirementIssues {
		if slices.Contains(requirement.Labels, LabelReady) && !anyOpen(requirement.BlockedBy) {
			plans = append(plans, Plan{Number: requirement.Number})
		}
	}
	return plans
}

// labelsOf is the labels of the requirement issue that requirementIssueNow
// read, or none when it could not be read.
func labelsOf(requirement RequirementIssue, err error) []string {
	if err != nil {
		return nil
	}
	return requirement.Labels
}
