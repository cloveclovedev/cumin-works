package workflow

// This file builds the monitor file and writes it at the end of every poll
// and at the end of every agent run.
// A tool that shows cumin from outside reads the file; cumin never reads it
// and decides nothing from it. The content comes from facts that the poll
// already has: no query and no minimal run is added for it.
// docs/ja/designs/status-menu-bar.md, the topic on the monitor file.

import (
	"cmp"
	"fmt"
	"slices"
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
	// Running are the agent runs that cumin holds as running, in any order.
	Running []state.MonitorAgent
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
		Agents:        monitorAgents(facts.Running),
	}
}

// monitorAgents is the list of the running agents in its fixed order: by
// the repository, then by the number of the issue.
func monitorAgents(agents []state.MonitorAgent) []state.MonitorAgent {
	sorted := slices.Clone(agents)
	slices.SortFunc(sorted, func(a, b state.MonitorAgent) int {
		return cmp.Or(cmp.Compare(a.Repository, b.Repository), cmp.Compare(a.Issue, b.Issue))
	})
	return sorted
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
// It keeps the facts of the poll for the write at the end of a run.
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
	s.monitorMu.Lock()
	defer s.monitorMu.Unlock()
	facts.Running = s.runningAgents()
	s.monitorFacts = &facts
	s.writeMonitor(facts)
}

// writeMonitorFileAfterRun writes the monitor file at the end of an agent
// run, so that the file does not list the run until the next poll. The
// other facts stay the ones of the last poll: last_poll.at moves only with
// a poll. Before the end of the first poll there is no such fact, and that
// poll writes the file.
func (s *Service) writeMonitorFileAfterRun() {
	if s.MonitorPath == "" {
		return
	}
	s.monitorMu.Lock()
	defer s.monitorMu.Unlock()
	if s.monitorFacts == nil {
		return
	}
	s.monitorFacts.Running = s.runningAgents()
	s.writeMonitor(*s.monitorFacts)
}

// writeMonitor builds and writes the monitor file. The caller holds
// monitorMu, so that an older content never replaces a newer one.
func (s *Service) writeMonitor(facts MonitorFacts) {
	if err := state.WriteMonitorFile(s.MonitorPath, BuildMonitorFile(facts)); err != nil {
		s.logger().Warn("the monitor file was not written; cumin goes on as usual", "path", s.MonitorPath, "error", err.Error())
	}
}

// runningAgents returns the agent runs of the issues in work, in any
// order. The URL is the URL of the issue on GitHub.
func (s *Service) runningAgents() []state.MonitorAgent {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	agents := make([]state.MonitorAgent, 0, len(s.inProgress))
	for key, run := range s.inProgress {
		agents = append(agents, state.MonitorAgent{
			Repository: key.repository,
			Issue:      key.issue,
			Role:       string(run.role),
			Request:    run.request,
			Title:      run.title,
			URL:        fmt.Sprintf("https://github.com/%s/issues/%d", key.repository, key.issue),
		})
	}
	return agents
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
