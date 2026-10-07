package workflow

// This file applies "stop agent starts" and "resume agent starts" of
// issue-states.md: before every start of an agent, and at the end of each
// agent run, the quota usage decides whether cumin goes on starting agents;
// while agent starts are stopped, the stored usage decides when to try
// again. A stored usage that is new enough decides a start without a
// minimal run. The limits are the pure rules of internal/quota.
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

// RowQ1 is the row "stop agent starts" at a quota limit.
const RowQ1 = "Q1"

// quotaNotices keeps the Owner from getting the same notification of "stop agent starts" twice.
// It lives in memory: a restart may send one more, and the state file
// holds nothing for it (designs/quota.md, the topic on duplicate
// notifications).
type quotaNotices struct {
	// told holds the windows that the Owner heard about since agent starts last
	// resumed.
	told map[quota.Name]bool
	// unreadTold says that the Owner heard that the usage was not read,
	// since the last read that succeeded.
	unreadTold bool
	// allowanceWarning is the last reason why the allowance file was not
	// read, so that the same reason is logged once.
	allowanceWarning string
}

// noteQuotaUnread records that the read of the usage failed in this poll.
// The later requests of the poll wait without a new minimal run, which
// would fail the same way, until the next poll reads again
// (forgetQuotaUnread).
func (s *Service) noteQuotaUnread() {
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	s.quotaUnread = true
}

// forgetQuotaUnread takes back the mark at the start of a poll, once for
// all repositories.
func (s *Service) forgetQuotaUnread() {
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	s.quotaUnread = false
}

// quotaUnreadInPoll says whether the read of the usage failed in this
// poll.
func (s *Service) quotaUnreadInPoll() bool {
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	return s.quotaUnread
}

// now is the time of the quota decisions. Tests set Service.Now.
func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// location is the time zone of the time bands. Tests set Service.Location.
func (s *Service) location() *time.Location {
	if s.Location != nil {
		return s.Location
	}
	return time.Local
}

// quotaAllowsStart is the quota part of the one check before every start
// of an agent (permitStart, its only caller): the usage decides against
// the limits. A stored usage that is new enough (quota.Fresh) decides
// without a minimal run; an older or missing one costs one minimal run
// that reads the usage again. A read that fails is kept for the rest of
// the poll, so the later requests of that poll wait with no minimal run.
// It runs before the label change and before
// the count of the request, so that a stopped start leaves the issue as it
// was and a later poll decides again.
//
// It reports false when the agent must not start: the usage was not read,
// or a window is at or above its limit. The Owner hears once for each
// cause, until agent starts resume.
func (s *Service) quotaAllowsStart(ctx context.Context, log *slog.Logger, request string, role config.Role, target Target, number int) bool {
	if s.Agents == nil {
		log.Error("stop agent starts: no agent service is configured; the quota usage was not read", "request", request)
		return false
	}
	// A stored usage that is new enough decides, with no minimal run.
	if usage, fresh := s.freshUsage(); fresh {
		// Each poll decides again while the usage is new enough, so a stop
		// is a debug line here. The Owner still hears once.
		log.Debug("the stored quota usage is new enough; no minimal run", "request", request)
		return s.usageAllowsStart(ctx, log, slog.LevelDebug, request, target, number, usage)
	}
	// While the stored usage stops the agent starts, no minimal run happens
	// before the next try time. Usage only rises until a reset, so no
	// earlier read could pass.
	if next, waiting := s.nextTry(); waiting {
		log.Debug("agent starts stopped: the request waits for the next try time", "request", request, "next_try", next)
		return false
	}
	// One failed read is enough for one poll: a new minimal run would fail
	// the same way. The first failure already told the Owner.
	if s.quotaUnreadInPoll() {
		log.Debug("agent starts stopped: the quota usage was not read in this poll; no minimal run", "request", request)
		return false
	}
	read, err := s.Agents.ReadQuota(ctx, role)
	if err != nil {
		s.noteQuotaUnread()
		var notRead *agent.QuotaNotRead
		reason := err.Error()
		if errors.As(err, &notRead) {
			reason = notRead.Reason
		}
		log.Info("stop agent starts: the quota usage was not read", "request", request, "reason", reason)
		s.quotaMu.Lock()
		tell := !s.quota.unreadTold
		s.quota.unreadTold = true
		s.quotaMu.Unlock()
		if tell && !s.notifyQuota(ctx, log, target, number,
			"The quota usage was not read before the start of an agent ("+reason+"). cumin starts no agent until a read succeeds.") {
			// The channel failed: the next poll tries again.
			s.quotaMu.Lock()
			s.quota.unreadTold = false
			s.quotaMu.Unlock()
		}
		return false
	}
	return s.usageAllowsStart(ctx, log, slog.LevelInfo, request, target, number, s.keepUsage(log, read))
}

// usageAllowsStart decides one start from a usage, stored or just read. At
// a limit, the Owner hears once for each window, and the stop is logged at
// level.
func (s *Service) usageAllowsStart(ctx context.Context, log *slog.Logger, level slog.Level, request string, target Target, number int, usage quota.Usage) bool {
	decision := s.decideQuota(log, usage)
	if decision.Allows() {
		return true
	}
	next, _ := quota.NextTry(usage, s.quotaSettings(), s.allowance(log), s.now(), s.location())
	log.Log(ctx, level, "stop agent starts: the quota limit is reached", "request", request, "windows", decision.Stopped, "next_try", next)
	s.tellQuotaLimit(ctx, log, target, number, decision)
	return false
}

// freshUsage returns the stored usage when cumin read it a short time ago
// (quota.Fresh), so that a start decides from it without a minimal run.
func (s *Service) freshUsage() (quota.Usage, bool) {
	stored, ok := s.State.Quota()
	if !ok || !quota.Fresh(stored.ReadAt, s.now()) {
		return quota.Usage{}, false
	}
	return storedUsage(stored), true
}

// quotaAfterRun applies "stop agent starts" at the end of an agent run: the
// usage that the run reported decides whether agent starts stop now, before
// the next start decides. The run itself is done; nothing else changes.
func (s *Service) quotaAfterRun(ctx context.Context, log *slog.Logger, target Target, number int, run *agent.Run) {
	if run == nil || !run.QuotaRead {
		return
	}
	decision := s.decideQuota(log, s.keepUsage(log, run.Quota))
	if decision.Allows() {
		return
	}
	log.Info("stop agent starts: the quota limit is reached at the end of a run", "windows", decision.Stopped)
	s.tellQuotaLimit(ctx, log, target, number, decision)
}

// keepUsage stores a usage that was read, ends the silence after an unread
// usage, and returns the usage that the state now holds, which the caller
// decides from. The lock covers the read and the write of the state, so
// that two runs that end together do not lose a reading: a later read that fails is a new failure. A state that
// cannot be saved costs only a minimal run after a restart.
func (s *Service) keepUsage(log *slog.Logger, read agent.QuotaUsage) quota.Usage {
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	s.quota.unreadTold = false
	// Runs that overlap can end in any order, so each window keeps the
	// newer of the stored reading and this one. An older reading never
	// makes the next try time earlier.
	usage := toUsage(read)
	readAt := read.ReadAt
	if readAt.IsZero() {
		readAt = s.now()
	}
	if stored, ok := s.State.Quota(); ok {
		// A reading that came in late keeps the later time: the kept
		// values are never older than the stored time says.
		if stored.ReadAt.After(readAt) {
			readAt = stored.ReadAt
		}
		kept := storedUsage(stored)
		usage.FiveHour = quota.Newer(usage.FiveHour, kept.FiveHour)
		usage.Weekly = quota.Newer(usage.Weekly, kept.Weekly)
	}
	err := s.State.SetQuota(state.Quota{
		FiveHour: state.QuotaWindow{Utilization: usage.FiveHour.Utilization, ResetsAt: usage.FiveHour.ResetsAt},
		Weekly:   state.QuotaWindow{Utilization: usage.Weekly.Utilization, ResetsAt: usage.Weekly.ResetsAt},
		ReadAt:   readAt,
	})
	if err != nil {
		log.Error("the quota usage was not kept", "error", err.Error())
	}
	return usage
}

// nextTry reports the next try time while the stored usage stops the
// agent starts, and false when a start may read the usage now.
func (s *Service) nextTry() (time.Time, bool) {
	stored, ok := s.State.Quota()
	if !ok {
		return time.Time{}, false
	}
	now := s.now()
	next, stopped := quota.NextTry(storedUsage(stored), s.quotaSettings(), s.allowance(s.logger()), now, s.location())
	if !stopped || !now.Before(next) {
		return time.Time{}, false
	}
	return next, true
}

// allowance reads the allowance file that `cumin quota allow` writes ("resume
// agent starts").
// cumin reads it at every check, so that a new allowance counts at the next
// poll. A file that cannot be read is no allowance, with one warning that
// names the path, until the reason changes.
func (s *Service) allowance(log *slog.Logger) quota.Allowance {
	if s.AllowancePath == "" {
		return quota.Allowance{}
	}
	read, err := state.ReadAllowance(s.AllowancePath)
	s.quotaMu.Lock()
	warn := err != nil && err.Error() != s.quota.allowanceWarning
	if err == nil {
		s.quota.allowanceWarning = ""
	} else {
		s.quota.allowanceWarning = err.Error()
	}
	s.quotaMu.Unlock()
	if warn {
		log.Warn("the allowance file was not read; cumin goes on without an allowance", "path", s.AllowancePath, "error", err.Error())
	}
	return quota.Allowance{FiveHourUntil: read.FiveHourUntil}
}

func (s *Service) quotaSettings() config.QuotaSettings {
	if s.Settings == nil {
		return config.QuotaSettings{}
	}
	return s.Settings.Quota
}

// decideQuota applies the limits of the Host settings to one usage. The
// numbers go to the debug log only (designs/quota.md).
func (s *Service) decideQuota(log *slog.Logger, usage quota.Usage) quota.Decision {
	decision := quota.Decide(usage, s.quotaSettings(), s.allowance(log), s.now(), s.location())
	s.forgetResumedWindows(decision)
	log.Debug("quota usage",
		"five_hour_utilization", usage.FiveHour.Utilization, "five_hour_limit", decision.FiveHourLimit,
		"weekly_utilization", usage.Weekly.Utilization, "weekly_limit", decision.WeeklyLimit)
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
// agent starts, until agent starts resume. The mark is set before the send, so
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
		return "The weekly quota window reached its pace limit. cumin starts no agent until the pace limit rises above the usage or the window resets. The agent runs that are going on end as usual."
	}
	return fmt.Sprintf("The %s quota window reached its limit. cumin starts no agent until the window resets, a time band with a higher limit starts, or the Owner runs cumin quota allow. The agent runs that are going on end as usual.", window)
}

// notifyQuota sends one notification of "stop agent starts". The quota belongs to the account
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

func storedUsage(stored state.Quota) quota.Usage {
	return quota.Usage{
		FiveHour: quota.Window{Utilization: stored.FiveHour.Utilization, ResetsAt: stored.FiveHour.ResetsAt},
		Weekly:   quota.Window{Utilization: stored.Weekly.Utilization, ResetsAt: stored.Weekly.ResetsAt},
	}
}

func toUsage(read agent.QuotaUsage) quota.Usage {
	return quota.Usage{
		FiveHour: quota.Window{Utilization: read.FiveHour.Utilization, ResetsAt: read.FiveHour.ResetsAt},
		Weekly:   quota.Window{Utilization: read.Weekly.Utilization, ResetsAt: read.Weekly.ResetsAt},
	}
}
