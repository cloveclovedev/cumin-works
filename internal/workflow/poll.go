package workflow

// This file does one poll: it creates the missing labels, decides whether a
// repository is due, reads the snapshot, and applies the actions that the
// pure rules decide (pollRepository). It also keeps what the loop knows of
// the last poll of each repository. docs/ja/designs/poll.md.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
)

// ensureLabels creates the missing labels of each target repository. A
// failure is logged; the poll still runs, so that a repository without the
// labels does not stop the others.
func (s *Service) ensureLabels(ctx context.Context) {
	for _, target := range s.Targets {
		log := s.logger().With("repository", target.Repository.String())
		token, err := target.Token(ctx)
		if err != nil {
			log.Error("create the missing labels: no token", "error", err.Error())
			continue
		}
		created, err := s.GitHub.EnsureLabels(ctx, token, target.Repository.Owner, target.Repository.Name, s.Labels)
		for _, name := range created {
			log.Info("created the label", "label", name)
		}
		if err != nil {
			log.Error("create the missing labels failed", "error", err.Error())
		}
	}
}

// ensurePriorityLabels creates the default priority labels of a repository
// whose settings name none, when they are missing, as ensureLabels does for
// the other labels of cumin. The settings of the repository decide it, so
// it runs in the poll: once after each read of the settings, and again at
// the next poll after a failure. Labels that a settings file names belong
// to the organization, and cumin never creates or changes them.
func (s *Service) ensurePriorityLabels(ctx context.Context, log *slog.Logger, token string, target Target, settings *RepositorySettings, readAgain bool) {
	key := repositoryKey(target.Repository)
	s.settingsMu.Lock()
	if readAgain {
		delete(s.priorityLabelsDone, key)
	}
	done := s.priorityLabelsDone[key]
	s.settingsMu.Unlock()
	if done {
		return
	}
	if settings.Settings.PriorityLabels == nil {
		labels := DefaultPriorityLabels(config.DefaultPriorityLabels())
		created, err := s.GitHub.EnsureLabels(ctx, token, target.Repository.Owner, target.Repository.Name, labels)
		for _, name := range created {
			log.Info("created the label", "label", name)
		}
		if err != nil {
			log.Error("create the missing priority labels failed", "error", err.Error())
			return
		}
	}
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	if s.priorityLabelsDone == nil {
		s.priorityLabelsDone = map[string]bool{}
	}
	s.priorityLabelsDone[key] = true
}

// Poll does one poll of every target repository: read the snapshot, decide,
// and apply the actions. A failure in one repository does not stop the
// others, and a failure that repeats is notified (pollFailed). The
// returned error joins the failures.
func (s *Service) Poll(ctx context.Context) error {
	var errs []error
	var all pollResult
	finishing := s.stopRequested()
	// A failed read of the quota usage is kept for one poll only: this
	// poll reads again.
	s.forgetQuotaUnread()
	for _, target := range s.Targets {
		// The stop signal came while this poll was running. Start nothing
		// more: the requests that are going on are the ones to wait for.
		if ctx.Err() != nil {
			return errors.Join(errs...)
		}
		// A repository with no issue in work waits for the idle poll
		// interval. A stop after the runs polls every repository, as before.
		at := s.now()
		if !s.pollIsDue(target.Repository, at) && !finishing {
			continue
		}
		result, err := s.pollRepository(ctx, target, finishing)
		s.notePoll(target.Repository, at, result, err)
		all.add(result)
		if err == nil {
			s.pollSucceeded(target.Repository)
			continue
		}
		s.logger().Error("poll failed", "repository", target.Repository.String(), "error", err.Error())
		errs = append(errs, err)
		s.pollFailed(ctx, target.Repository, err)
	}
	// "tell that cumin waits" sends only after a poll that read every
	// repository; a decided
	// action ends the silence even when another repository failed. A stop request
	// holds work back, so having nothing to do is not news then.
	if !finishing {
		all.waitsForQuota = s.startWaitsForQuota()
		s.waitingCheck(ctx, all, len(errs) == 0)
	}
	s.writeMonitorFile(finishing)
	return errors.Join(errs...)
}

// lastPoll is what the poll loop keeps of the last poll of a repository.
type lastPoll struct {
	LastPoll
	// at is when the poll started.
	at time.Time
	// runEnded says that an agent run of the repository ended since the
	// poll started. The next poll reads what the run left.
	runEnded bool
	// idle says that the log already told that the repository is not in work.
	idle bool
}

// pollIsDue says whether this poll reads the repository: always when the
// repository is in work, and otherwise once in each idle poll interval.
func (s *Service) pollIsDue(repository config.Repository, now time.Time) bool {
	if s.IdlePollInterval <= 0 {
		return true
	}
	key := repository.String()
	running := len(s.runningIssues(key)) > 0
	s.lastPollMu.Lock()
	defer s.lastPollMu.Unlock()
	last := s.lastPolls[key]
	if last == nil {
		return true
	}
	inWork := RepositoryInWork(last.LastPoll, running || last.runEnded)
	if !PollIsDue(inWork, now.Sub(last.at), s.PollInterval, s.IdlePollInterval) {
		return false
	}
	// This poll reads what a run that ended left.
	last.runEnded = false
	return true
}

// notePoll keeps the result of a poll of a repository for pollIsDue, and
// logs once when the repository leaves or enters the work.
func (s *Service) notePoll(repository config.Repository, at time.Time, result pollResult, err error) {
	if s.IdlePollInterval <= 0 {
		return
	}
	key := repository.String()
	running := len(s.runningIssues(key)) > 0
	s.lastPollMu.Lock()
	defer s.lastPollMu.Unlock()
	if s.lastPolls == nil {
		s.lastPolls = map[string]*lastPoll{}
	}
	last := s.lastPolls[key]
	if last == nil {
		last = &lastPoll{}
		s.lastPolls[key] = last
	}
	// A run that ended while this poll ran stays noted: the poll may have
	// read GitHub before the end.
	last.LastPoll = LastPoll{Ran: true, Failed: err != nil, Acted: result.decided, IssueInWork: result.issueInWork}
	last.at = at
	idle := !RepositoryInWork(last.LastPoll, running || last.runEnded)
	if idle != last.idle {
		log := s.logger().With("repository", key)
		if idle {
			log.Info("no issue is in work: the next poll comes after the idle poll interval",
				"idle_poll_interval", s.IdlePollInterval.String())
		} else {
			log.Info("the repository is in work again: the next poll comes after the poll interval")
		}
	}
	last.idle = idle
}

// noteRunEnded records the end of an agent run of a repository, so that
// the next poll reads the repository, also when it had no issue in work.
func (s *Service) noteRunEnded(repository string) {
	s.lastPollMu.Lock()
	defer s.lastPollMu.Unlock()
	if last := s.lastPolls[repository]; last != nil {
		last.runEnded = true
	}
}

// readSnapshot reads one repository for a poll, in two queries, and builds
// one snapshot from the two reads. The poll query stops at the sub-issue. The
// second query reads the pull requests of the sub-issues that a rule reads
// them for; no such sub-issue means no second query (poll.md, the topic on
// the two queries). The rate limit of the result is the one of both reads.
//
// The snapshot holds only the requirement issues that both reads read in
// full. An issue over a limit of a query takes its requirement issue out,
// with all its sub-issues, and the unread list of the result names the issue
// and the limit. Every other failure of a read is an error. The snapshot
// keeps what was read of the removed requirement issues in Snapshot.Unread,
// for the count of the issues in progress only.
func (s *Service) readSnapshot(ctx context.Context, token, owner, repo string) (github.RepositorySnapshot, Snapshot, error) {
	read, err := s.GitHub.ReadSnapshot(ctx, token, owner, repo)
	if err != nil {
		return github.RepositorySnapshot{}, Snapshot{}, err
	}
	snapshot := toSnapshot(read)
	selected := snapshot.SubIssuesWithPullRequestRules()
	if len(selected) == 0 {
		return read, snapshot, nil
	}
	ids := make([]string, 0, len(selected))
	for _, sub := range selected {
		ids = append(ids, sub.NodeID)
	}
	pullRequests, err := s.GitHub.ReadPullRequests(ctx, token, owner, repo, ids)
	if err != nil {
		return github.RepositorySnapshot{}, Snapshot{}, err
	}
	byIssue := map[int][]PullRequest{}
	for number, list := range pullRequests.PullRequests {
		byIssue[number] = toPullRequests(list)
	}
	read.RateLimit.Cost += pullRequests.RateLimit.Cost
	read.RateLimit.Remaining = pullRequests.RateLimit.Remaining
	snapshot = snapshot.WithPullRequests(byIssue)
	unread := map[int]bool{}
	for _, issue := range pullRequests.Unread {
		issue.Requirement = snapshot.RequirementOf(issue.Issue)
		unread[issue.Requirement] = true
		read.Unread = append(read.Unread, issue)
	}
	return read, snapshot.WithoutRequirementIssues(unread), nil
}

func (s *Service) pollRepository(ctx context.Context, target Target, finishing bool) (pollResult, error) {
	var result pollResult
	owner, repo := target.Repository.Owner, target.Repository.Name
	// The running set comes before every read of GitHub. A run that ends
	// after the read of the snapshot has already decided its own end and
	// changed the issue. It then still counts as running for this poll, so
	// the poll does not decide the same end again from the old facts of
	// its snapshot: the next poll decides. A poll starts every run itself,
	// so no run is missing from a set that is read first.
	running := s.runningIssues(target.Repository.String())
	// This poll decides every start of the repository again, so a start
	// that waited only for the quota at an earlier poll is forgotten first.
	s.forgetQuotaWaits(target.Repository.String())
	token, err := target.Token(ctx)
	if err != nil {
		return result, err
	}
	read, snapshot, err := s.readSnapshot(ctx, token, owner, repo)
	if err != nil {
		return result, err
	}
	snapshot.Running = running
	s.noteWaitingIssues(target.Repository.String(), snapshot.WaitingIssues(target.Repository.String()))
	log := s.logger().With("repository", target.Repository.String())
	// An issue over a limit of a read is no failure of the poll. The
	// snapshot does not hold its requirement issue, so no rule and no later
	// read of this poll sees it, and no label of it changes.
	for _, issue := range read.Unread {
		log.Warn("cumin cannot read the issue in full: the poll leaves its requirement issue out and goes on",
			"issue", issue.Issue, "requirement_issue", issue.Requirement, "limit", issue.Limit)
	}

	// The settings of this repository. A wrong file skips this repository
	// and this poll; the other repositories are polled by the caller.
	settings, readAgain, err := s.settingsFor(target.Repository, read)
	if err != nil {
		return result, err
	}
	if readAgain {
		log.Info("the settings of the repository were read",
			"from_repository", settings.FromRepository, "risk_criteria", settings.RiskCriteriaSource)
	}
	// The setting of the repository decides the notification, so it comes
	// after the read of the settings.
	s.notifyUnreadIssues(ctx, log, target, settings.notificationOn(), read.Unread)
	s.ensurePriorityLabels(ctx, log, token, target, settings, readAgain)
	// The required checks are a REST call of their own, so the poll makes
	// it only when an issue of this repository waits for the checks ("request
	// the review", "request a check fix") or has an approval of a person to
	// check ("start the merge" after the approval of a Maintainer). Its budget
	// is not the one of the snapshot query.
	var required []RequiredCheck
	if snapshot.HasIssueChecking() || snapshot.HasMaintainerApprovalCandidate() {
		read, err := s.GitHub.RequiredChecks(ctx, token, owner, repo, snapshot.DefaultBranch)
		if err != nil {
			return result, err
		}
		required = toRequiredChecks(read)
	}
	log.Info("poll",
		"requirement_issues", len(snapshot.RequirementIssues),
		"required_checks", len(required),
		"issues_with_pull_requests_read", len(snapshot.SubIssuesWithPullRequestRules()),
		"settings", settingsSource(settings.FromRepository), "risk_criteria", settings.RiskCriteriaSource,
		"rate_limit_cost", read.RateLimit.Cost, "rate_limit_remaining", read.RateLimit.Remaining)

	s.readStatusActors(ctx, log, token, target, settings, &snapshot)
	s.readLabelTimes(ctx, log, token, target, &snapshot)
	if !finishing {
		s.readReadyIssueOwners(ctx, log, token, target, settings, &snapshot)
	}
	s.readAcceptanceComments(ctx, log, token, target, &snapshot)
	s.readImplementingFacts(ctx, log, token, target, settings, &snapshot)
	s.readReviewingFacts(ctx, log, token, target, settings, &snapshot)
	s.readMergingFacts(ctx, log, token, target, settings, &snapshot)
	s.writeFollowUpNotes(ctx, log, token, target, &snapshot)
	s.cleanUp(ctx, log, target, snapshot)
	var errs []error
	// A requirement issue that "mark the requirement as in work" could not
	// move keeps its sub-issues
	// waiting in this poll. A claim would take cumin/status/ready away from
	// the sub-issue, and that action would then never apply again: the requirement
	// issue would stay in awaiting-plan-review while its work goes on.
	notStarted := map[int]bool{}
	// An issue that cumin moves on without a Maintainer keeps "tell that
	// cumin waits" silent, even when this poll decides nothing for it.
	result.movesOn = snapshot.MovesWithoutMaintainer()
	result.issueInWork = snapshot.HasIssueInWork()
	actions := Decide(snapshot, s.Settings.MaxIssuesInProgress, required, settings.Settings.PriorityLabelNames(),
		s.now(), settings.Settings.ChecksWaitTime)
	// An issue that "start the merge" after the approval of a Maintainer or
	// "send back for changes" took at this poll gets no "request a conflict
	// resolution": the review of a Maintainer on the conflicting head decides
	// first. A check of one of those two actions that failed keeps the issue
	// too, so that the next poll decides it again. So does a request for
	// changes of a Maintainer that waits for the permit of its start.
	maintainerDecided := map[int]bool{}
	// The merges that this poll sent, for the wait between two of them.
	merges := 0
	for _, action := range actions {
		if a, ok := action.(ResolveConflict); ok && maintainerDecided[a.Number] {
			log.Info(string(ActionRequestAConflictResolution)+": waits for the review of a Maintainer on the conflicting head", "issue", a.Number)
			continue
		}
		// A candidate of "start the merge" after the approval of a Maintainer
		// or of "send back for changes" is only a check; it counts as progress
		// for "tell that cumin waits" when it merges, stops, or sends back the
		// issue.
		switch action.(type) {
		case MergeMaintainerApproval, FixMaintainerReview:
		default:
			result.note(action)
		}
		switch a := action.(type) {
		case StartRequirement:
			if err := s.startRequirement(ctx, token, target, snapshot, a); err != nil {
				notStarted[a.Number] = true
				errs = append(errs, err)
			}
		case ReviewRemaining:
			if err := s.reviewRemaining(ctx, token, target, snapshot, settings, a); err != nil {
				errs = append(errs, err)
			}
		case Accept:
			if err := s.accept(ctx, token, target, snapshot, settings, a); err != nil {
				errs = append(errs, err)
			}
		case CheckAcceptance:
			if err := s.checkAcceptance(ctx, token, target, snapshot, settings, a); err != nil {
				errs = append(errs, err)
			}
		case StopAcceptance:
			if err := s.stopAcceptance(ctx, token, target, snapshot, settings, a); err != nil {
				errs = append(errs, err)
			}
		case Plan:
			if err := s.plan(ctx, token, target, snapshot, settings, a); err != nil {
				errs = append(errs, err)
			}
		case ReviewPlan:
			if err := s.reviewPlan(ctx, token, target, snapshot, settings, a); err != nil {
				errs = append(errs, err)
			}
		case StopSplit:
			if err := s.stopSplit(ctx, token, target, snapshot, settings, a); err != nil {
				errs = append(errs, err)
			}
		case WaitForChecks:
			sub, _ := snapshot.SubIssue(a.Number)
			if err := s.waitForChecks(ctx, log.With("issue", a.Number), token, target, settings, sub, a); err != nil {
				errs = append(errs, err)
			}
		case StopImplementation:
			sub, _ := snapshot.SubIssue(a.Number)
			if err := s.stopImplementation(ctx, log.With("issue", a.Number), token, target, settings, sub, a); err != nil {
				errs = append(errs, err)
			}
		case RequestImplementationAgain:
			sub, _ := snapshot.SubIssue(a.Number)
			if err := s.requestImplementationAgain(ctx, token, target, settings, sub, snapshot.DefaultBranch, a); err != nil {
				errs = append(errs, err)
			}
		case CloseMergedIssue, SendMerge, ResolveMergeConflict, LeaveMerge:
			if err := s.mergeEndAtPoll(ctx, log, token, target, snapshot, settings, a, &merges); err != nil {
				errs = append(errs, err)
			}
		case RequestReviewFix, AskMaintainerToMerge, StartMerge, RequestCause, StopAtRoundLimit, BackToChecks, RequestReviewAgain, StopReview:
			if err := s.reviewEndAtPoll(ctx, log, token, target, snapshot, settings, a); err != nil {
				errs = append(errs, err)
			}
		case Claim:
			if notStarted[a.RequirementIssue] {
				log.Info(string(ActionRequestTheImplementation)+": waits for \""+string(ActionMarkTheRequirementAsInWork)+"\" of the requirement issue", "issue", a.Number, "requirement_issue", a.RequirementIssue)
				continue
			}
			if err := s.claim(ctx, token, target, snapshot, settings, a); err != nil {
				errs = append(errs, err)
			}
		case StartReview:
			if err := s.startReview(ctx, token, target, snapshot, settings, a); err != nil {
				errs = append(errs, err)
			}
		case FixChecks:
			if err := s.fixChecks(ctx, token, target, snapshot, settings, a); err != nil {
				errs = append(errs, err)
			}
		case ResolveConflict:
			if err := s.resolveConflictAtPoll(ctx, token, target, snapshot, settings, a); err != nil {
				errs = append(errs, err)
			}
		case StopForUnreportedChecks:
			if err := s.stopForUnreportedChecks(ctx, token, target, snapshot, settings, a); err != nil {
				errs = append(errs, err)
			}
		case CopyLabels:
			if err := s.copyLabels(ctx, token, target, a); err != nil {
				errs = append(errs, err)
			}
		case MergeMaintainerApproval:
			acted, err := s.mergeMaintainerApproval(ctx, token, target, snapshot, settings, required, a)
			if err != nil {
				errs = append(errs, err)
			}
			maintainerDecided[a.Number] = acted || err != nil
			if acted {
				result.note(action)
			}
		case FixMaintainerReview:
			acted, waits, err := s.fixMaintainerReview(ctx, token, target, snapshot, settings, a)
			if err != nil {
				errs = append(errs, err)
			}
			maintainerDecided[a.Number] = maintainerDecided[a.Number] || acted || waits || err != nil
			if acted {
				result.note(action)
			}
		default:
			errs = append(errs, fmt.Errorf("unknown action %T", action))
		}
	}
	return result, errors.Join(errs...)
}
