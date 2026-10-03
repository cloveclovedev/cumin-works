package workflow

// This file applies Q4 of issue-states.md: when nothing moves on without
// the Owner, the Owner hears it once. That means that no R1 (split) and no
// I1 (implement) holds in any target repository, that no agent runs, and
// that no issue exists that cumin itself moves on later (an issue that
// waits for the required checks, or a ready issue that waits for room
// under the limit). A start that only the quota stops (Q1) is work left:
// Q1 already named that cause (the Owner's decision on #234).

import (
	"context"

	"github.com/cloveclovedev/cumin-works/internal/notify"
)

// RowQ4 is the row of the notification "waiting".
const RowQ4 = "Q4"

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
	// issueInWork says that an issue of the repository is in work
	// (Snapshot.HasIssueInWork). Q4 does not read it: the poll loop does,
	// to pick the interval of the next poll of the repository.
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

// waitingCheck applies Q4 after a poll. A decided action or a running agent
// ends the silence whatever else happened in the poll. The notification
// goes out only after a poll that read every repository (complete), since
// a repository that was not read may hold work. An issue that cumin moves
// on without the Owner keeps the notification back and leaves the mark as
// it is: cumin did nothing. The mark lives in memory: a restart may send
// the notification once more.
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
	if !complete || result.movesOn {
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
	log.Info("Q4: no issue can go on and no agent runs; the Owner is told once")
	sent := s.notifyOwner(ctx, log, s.Settings != nil && s.Settings.Notify.DiscordEnabled, notify.Notification{
		Row:    RowQ4,
		Reason: "No issue can go on and no agent runs. cumin waits for a new ready issue or a decision of the Owner.",
	})
	if !sent {
		// The channel failed: the next poll with nothing to do tries again.
		s.quotaMu.Lock()
		s.waitingTold = false
		s.quotaMu.Unlock()
	}
}
