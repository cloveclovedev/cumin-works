package workflow

// This file applies Q1 and Q3 of issue-states.md: before a new start (R1,
// I1), and at the end of each agent run, the quota usage decides whether
// cumin starts new work; while it stops, the stored usage decides when to
// try again. The limits are the pure rules of internal/quota.
// docs/ja/designs/quota.md records the design; the minimal run that reads
// the usage is in internal/agent.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/agent"
	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/core/state"
	"github.com/cloveclovedev/cumin-works/internal/notify"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/quota"
)

// RowQ1 is the row that stops new starts at a quota limit.
const RowQ1 = "Q1"

// quotaNotices keeps the Owner from getting the same Q1 notification twice.
// It lives in memory: a restart may send one more, and the state file
// holds nothing for it (designs/quota.md, the topic on duplicate
// notifications).
type quotaNotices struct {
	// told holds the windows that the Owner heard about since starts last
	// went on.
	told map[quota.Name]bool
	// unreadTold says that the Owner heard that the usage was not read,
	// since the last read that succeeded.
	unreadTold bool
}

// now is the time of the quota decisions. Tests set Service.Now.
func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// quotaAllowsStart applies Q1 before a new start: one minimal run reads the
// usage, and the limits decide. It runs before the label changes, so that
// a stopped start leaves every label as it was and the next poll decides
// again. Each start reads the usage again, also a second start in the same
// poll, so that parallel starts never pass on the same old value.
//
// It reports false when the start must not go on: the usage was not read,
// or a window is at or above its limit. The Owner hears once for each
// cause, until starts go on again.
func (s *Service) quotaAllowsStart(ctx context.Context, row string, role config.Role, target Target, number int) (bool, error) {
	if s.Agents == nil {
		return false, errors.New("no agent service is configured")
	}
	log := s.logger().With("repository", target.Repository.String(), "issue", number)
	// Q3: while the stored usage stops the starts, no minimal run happens
	// before the next try time. Usage only rises until a reset, so no
	// earlier read could pass.
	if next, waiting := s.nextTry(); waiting {
		log.Debug("Q3: waits for the next try time", "row", row, "next_try", next)
		return false, nil
	}
	read, err := s.Agents.ReadQuota(ctx, role)
	if err != nil {
		var notRead *agent.QuotaNotRead
		reason := err.Error()
		if errors.As(err, &notRead) {
			reason = notRead.Reason
		}
		log.Info("Q1: no start; the quota usage was not read", "row", row, "reason", reason)
		s.quotaMu.Lock()
		tell := !s.quota.unreadTold
		s.quota.unreadTold = true
		s.quotaMu.Unlock()
		if tell && !s.notifyQuota(ctx, log, target, number,
			"The quota usage was not read before a start ("+reason+"). cumin starts no new work until a read succeeds.") {
			// The channel failed: the next poll tries again.
			s.quotaMu.Lock()
			s.quota.unreadTold = false
			s.quotaMu.Unlock()
		}
		return false, nil
	}
	s.keepUsage(log, read)
	decision := s.decideQuota(log, read)
	if decision.Allows() {
		return true, nil
	}
	next, _ := quota.NextTry(toUsage(read), s.quotaSettings(), s.now())
	log.Info("Q1: no start; the quota limit is reached", "row", row, "windows", decision.Stopped, "next_try", next)
	s.tellQuotaLimit(ctx, log, target, number, decision)
	return false, nil
}

// quotaAfterRun applies Q1 at the end of an agent run: the usage that the
// run reported decides whether new starts stop now, before the next start
// reads the usage again. The run itself is done; nothing else changes.
func (s *Service) quotaAfterRun(ctx context.Context, log *slog.Logger, target Target, number int, run *agent.Run) {
	if run == nil || !run.QuotaRead {
		return
	}
	s.keepUsage(log, run.Quota)
	decision := s.decideQuota(log, run.Quota)
	if decision.Allows() {
		return
	}
	log.Info("Q1: the quota limit is reached at the end of a run; no new start until it resumes", "windows", decision.Stopped)
	s.tellQuotaLimit(ctx, log, target, number, decision)
}

// keepUsage stores a usage that was read, and ends the silence after an
// unread usage: a later read that fails is a new failure. A state that
// cannot be saved costs only a minimal run after a restart.
func (s *Service) keepUsage(log *slog.Logger, read agent.QuotaUsage) {
	s.quotaMu.Lock()
	s.quota.unreadTold = false
	s.quotaMu.Unlock()
	err := s.State.SetQuota(state.Quota{
		FiveHour: state.QuotaWindow{Utilization: read.FiveHour.Utilization, ResetsAt: read.FiveHour.ResetsAt},
		Weekly:   state.QuotaWindow{Utilization: read.Weekly.Utilization, ResetsAt: read.Weekly.ResetsAt},
		ReadAt:   s.now(),
	})
	if err != nil {
		log.Error("Q3: the quota usage was not kept", "error", err.Error())
	}
}

// nextTry reports the next try time while the stored usage stops the
// starts, and false when a start may read the usage now.
func (s *Service) nextTry() (time.Time, bool) {
	stored, ok := s.State.Quota()
	if !ok {
		return time.Time{}, false
	}
	usage := quota.Usage{
		FiveHour: quota.Window{Utilization: stored.FiveHour.Utilization, ResetsAt: stored.FiveHour.ResetsAt},
		Weekly:   quota.Window{Utilization: stored.Weekly.Utilization, ResetsAt: stored.Weekly.ResetsAt},
	}
	now := s.now()
	next, stopped := quota.NextTry(usage, s.quotaSettings(), now)
	if !stopped || !now.Before(next) {
		return time.Time{}, false
	}
	return next, true
}

func (s *Service) quotaSettings() config.QuotaSettings {
	if s.Settings == nil {
		return config.QuotaSettings{}
	}
	return s.Settings.Quota
}

// decideQuota applies the limits of the Host settings to one usage. The
// numbers go to the debug log only (designs/quota.md).
func (s *Service) decideQuota(log *slog.Logger, read agent.QuotaUsage) quota.Decision {
	decision := quota.Decide(toUsage(read), s.quotaSettings(), s.now())
	s.forgetResumedWindows(decision)
	log.Debug("Q1: quota usage",
		"five_hour_utilization", read.FiveHour.Utilization, "five_hour_limit", decision.FiveHourLimit,
		"weekly_utilization", read.Weekly.Utilization, "weekly_limit", decision.WeeklyLimit)
	return decision
}

// forgetResumedWindows takes back the mark of each window that is below its
// limit in this read. A window that stops again later is a new stop, and
// the Owner hears about it again, whatever the other window does.
func (s *Service) forgetResumedWindows(decision quota.Decision) {
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	for window := range s.quota.told {
		if !slices.Contains(decision.Stopped, window) {
			delete(s.quota.told, window)
		}
	}
}

// tellQuotaLimit notifies the Owner once for each window that stops the
// starts, until starts go on again. The mark is set before the send, so
// that a poll and the end of a run never send the same notification twice,
// and taken back when the channel fails, so that a later check tries again.
func (s *Service) tellQuotaLimit(ctx context.Context, log *slog.Logger, target Target, number int, decision quota.Decision) {
	for _, window := range decision.Stopped {
		s.quotaMu.Lock()
		tell := !s.quota.told[window]
		if s.quota.told == nil {
			s.quota.told = map[quota.Name]bool{}
		}
		s.quota.told[window] = true
		s.quotaMu.Unlock()
		if tell && !s.notifyQuota(ctx, log, target, number, limitReason(window)) {
			s.quotaMu.Lock()
			delete(s.quota.told, window)
			s.quotaMu.Unlock()
		}
	}
}

func limitReason(window quota.Name) string {
	if window == quota.Weekly {
		return "The weekly quota window reached its pace limit. cumin starts no new work until the pace limit rises above the usage or the window resets. Running issues go on."
	}
	return fmt.Sprintf("The %s quota window reached its limit. cumin starts no new work until the window resets or a time band with a higher limit starts. Running issues go on.", window)
}

// notifyQuota sends one Q1 notification. The quota belongs to the account
// of the Host, and the marks against a second notification are shared by
// every repository, so the Host setting decides, as for a poll that keeps
// failing. A repository that turns its notifications off would otherwise
// silence the stop for every repository. The issue whose start or run met the limit is the link. It
// reports false when the channel failed.
func (s *Service) notifyQuota(ctx context.Context, log *slog.Logger, target Target, number int, reason string) bool {
	return s.notifyOwner(ctx, log, s.Settings != nil && s.Settings.Notify.DiscordEnabled, notify.Notification{
		Row:        RowQ1,
		Reason:     reason,
		Repository: target.Repository.String(),
		Subject:    fmt.Sprintf("issue #%d", number),
		Link:       github.IssueURL(target.Repository.Owner, target.Repository.Name, number),
	})
}

func toUsage(read agent.QuotaUsage) quota.Usage {
	return quota.Usage{
		FiveHour: quota.Window{Utilization: read.FiveHour.Utilization, ResetsAt: read.FiveHour.ResetsAt},
		Weekly:   quota.Window{Utilization: read.Weekly.Utilization, ResetsAt: read.Weekly.ResetsAt},
	}
}
