package workflow

// This file builds the monitor file and writes it at the end of every poll.
// A tool that shows cumin from outside reads the file; cumin never reads it
// and decides nothing from it. The content comes from facts that the poll
// already has: no query and no minimal run is added for it.
// docs/ja/designs/status-menu-bar.md, the topic on the monitor file.

import (
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/core/state"
	"github.com/cloveclovedev/cumin-works/internal/quota"
)

// MonitorFacts are the facts of one poll that the monitor file shows.
type MonitorFacts struct {
	// PollEnded is when the poll of all target repositories ended.
	PollEnded time.Time
	// PollErrors are the repositories whose last poll failed, in the order
	// of the settings.
	PollErrors []state.MonitorPollError
	// StopRequested says that cumin stops after the current runs.
	StopRequested bool
	// QuotaUnread says that the read of the usage failed in this poll.
	QuotaUnread bool
	// Usage is the latest usage that cumin kept, and UsageKept says that
	// there is one.
	Usage     quota.Usage
	UsageKept bool
	// QuotaSettings, Allowance, and Location decide the limits at
	// PollEnded, as the check before a start does.
	QuotaSettings config.QuotaSettings
	Allowance     quota.Allowance
	Location      *time.Location
}

// BuildMonitorFile builds the content of the monitor file from the facts of
// a poll. It is pure. The quota part holds the name of the state, the names
// of the windows, and the next try time only: no utilization, no limit, and
// no reset time.
func BuildMonitorFile(facts MonitorFacts) state.MonitorFile {
	return state.MonitorFile{
		Version:       state.MonitorFileVersion,
		LastPoll:      state.MonitorLastPoll{At: facts.PollEnded, Errors: facts.PollErrors},
		StopRequested: facts.StopRequested,
		Quota:         monitorQuota(facts),
	}
}

// monitorQuota is the quota state of the monitor file. A read that failed
// in this poll is "unread". Without it, the kept usage decides against the
// limits of now; no kept usage stops nothing, because cumin reads the usage
// before its first start.
func monitorQuota(facts MonitorFacts) state.MonitorQuota {
	if facts.QuotaUnread {
		return state.MonitorQuota{State: state.MonitorQuotaUnread}
	}
	if !facts.UsageKept {
		return state.MonitorQuota{State: state.MonitorQuotaOpen}
	}
	decision := quota.Decide(facts.Usage, facts.QuotaSettings, facts.Allowance, facts.PollEnded, facts.Location)
	if decision.Allows() {
		return state.MonitorQuota{State: state.MonitorQuotaOpen}
	}
	stopped := state.MonitorQuota{State: state.MonitorQuotaStopped}
	for _, name := range decision.Stopped {
		stopped.StoppedWindows = append(stopped.StoppedWindows, string(name))
	}
	if next, ok := quota.NextTry(facts.Usage, facts.QuotaSettings, facts.Allowance, facts.PollEnded, facts.Location); ok {
		stopped.NextTryAt = &next
	}
	return stopped
}

// writeMonitorFile writes the monitor file at the end of a poll, also after
// a poll that failed or that passed over every repository. A write that
// fails is logged and changes nothing else: the file is for a display only.
func (s *Service) writeMonitorFile(stopRequested bool) {
	if s.MonitorPath == "" {
		return
	}
	facts := MonitorFacts{
		PollEnded:     s.now(),
		PollErrors:    s.pollErrors(),
		StopRequested: stopRequested,
		QuotaUnread:   s.quotaUnreadInPoll(),
		QuotaSettings: s.quotaSettings(),
		Location:      s.location(),
	}
	if stored, ok := s.State.Quota(); ok {
		facts.Usage, facts.UsageKept = storedUsage(stored), true
		facts.Allowance = s.allowance(s.logger())
	}
	if err := state.WriteMonitorFile(s.MonitorPath, BuildMonitorFile(facts)); err != nil {
		s.logger().Warn("the monitor file was not written; cumin goes on as usual", "path", s.MonitorPath, "error", err.Error())
	}
}

// pollErrors returns the repositories whose last poll failed, in the order
// of the targets, with the reason on one line as the notification of a poll
// that keeps failing has it (pollfailure.go). A repository that this poll
// passed over keeps the result of its last poll.
func (s *Service) pollErrors() []state.MonitorPollError {
	s.failureMu.Lock()
	defer s.failureMu.Unlock()
	var failed []state.MonitorPollError
	for _, target := range s.Targets {
		if failure, ok := s.pollFailures[repositoryKey(target.Repository)]; ok {
			failed = append(failed, state.MonitorPollError{Repository: target.Repository.String(), Message: oneLine(failure.reason)})
		}
	}
	return failed
}
