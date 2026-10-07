package workflow

// This file applies Q4 of issue-states.md: when nothing moves on without
// the Owner, the Owner hears it once. That means that no R1 (split) and no
// I1 (implement) holds in any target repository, that no agent runs, and
// that no issue exists that cumin itself moves on later (an issue that
// waits for the required checks, or a ready issue that waits for room
// under the limit). A start that only the quota stops (Q1) is work left:
// Q1 already named that cause (the Owner's decision on #234). That holds
// for every start of an agent, in whatever state the issue waits.

import (
	"context"

	"github.com/cloveclovedev/cumin-works/internal/notify"
)

// pollResult is what one poll of a repository tells Q4.
type pollResult struct {
	// decided says that the poll decided an action. Either cumin did
	// something, or R1 or I1 holds: a start that only the quota stops is
	// still a decided Plan or Claim. Both mean that cumin is not waiting.
	decided bool
	// movesOn says that an issue exists that cumin moves on without the
	// Owner (Snapshot.MovesWithoutOwner). cumin did nothing in this poll,
	// and still it is not waiting for the Owner.
	movesOn bool
	// waitsForQuota says that a start of an agent waits only for the quota
	// in any repository. The one check before a start notes it
	// (permitStart), and Poll reads it for every repository at once, so
	// add does not join it.
	waitsForQuota bool
	// issueInWork says that an issue of the repository is in work
	// (Snapshot.HasIssueInWork). The waiting notification to the Owner (Q4)
	// does not read it: the poll loop does, to pick the interval of the
	// next poll of the repository.
	issueInWork bool
}

// add joins the result of one more repository: one repository with work
// that goes on means that cumin is not waiting.
func (r *pollResult) add(other pollResult) {
	r.decided = r.decided || other.decided
	r.movesOn = r.movesOn || other.movesOn
}

// note records one decided action.
func (r *pollResult) note(Action) { r.decided = true }

// noteQuotaWait records that a start of an agent of the repository waits
// only for the quota. The quota notification already named the cause, so
// the waiting notification stays back until a poll of the repository
// decides its starts again (forgetQuotaWaits).
func (s *Service) noteQuotaWait(repository string) {
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	if s.quotaWaits == nil {
		s.quotaWaits = map[string]bool{}
	}
	s.quotaWaits[repository] = true
}

// forgetQuotaWaits takes back the note of the repository at the start of
// its poll: the poll asks for the permit of every start that still waits.
func (s *Service) forgetQuotaWaits(repository string) {
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	delete(s.quotaWaits, repository)
}

// startWaitsForQuota says whether a start of an agent waits only for the
// quota in any repository.
func (s *Service) startWaitsForQuota() bool {
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	return len(s.quotaWaits) > 0
}

// waitingCheck applies Q4 after a poll. A decided action or a running agent
// ends the silence whatever else happened in the poll. The notification
// goes out only after a poll that read every repository (complete), since
// a repository that was not read may hold work. A start that waits only
// for the quota, and an issue that cumin moves on without the Owner, keep
// the notification back and leave the mark as it is: cumin did nothing.
// The mark lives in memory: a restart may send the notification once more.
func (s *Service) waitingCheck(ctx context.Context, result pollResult, complete bool) {
	running := len(s.inProgressIssues()) > 0
	s.quotaMu.Lock()
	if result.decided || running {
		// cumin did something, or has something to do: a later time with
		// nothing to do is new.
		s.waitingTold = false
		s.quotaMu.Unlock()
		return
	}
	if !complete || result.waitsForQuota || result.movesOn {
		s.quotaMu.Unlock()
		return
	}
	tell := !s.waitingTold
	s.waitingTold = true
	s.quotaMu.Unlock()
	if !tell {
		return
	}
	log := s.logger()
	log.Info(string(ActionTellThatCuminWaits) + ": no issue can go on and no agent runs; the Owner is told once")
	sent := s.notifyOwner(ctx, log, s.Settings != nil && s.Settings.Notify.DiscordEnabled, notify.Notification{
		Action: string(ActionTellThatCuminWaits),
		Reason: "No issue can go on and no agent runs. cumin waits for a new ready issue or a decision.",
	})
	if !sent {
		// The channel failed: the next poll with nothing to do tries again.
		s.quotaMu.Lock()
		s.waitingTold = false
		s.quotaMu.Unlock()
	}
}
