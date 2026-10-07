package workflow

// This file is the stop after the current runs: the Operator asked cumin to
// finish the agent runs that are going on, to start no new work, and then to
// exit (`cumin stop --after-current-runs`). The request is a file in the
// state directory of the Host. docs/ja/designs/cumin-core.md, the topic on
// the stop.

import (
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/state"
)

// dropStopRequest removes a stop request that was written before this
// start of cumin run. A request is for the process that ran when the Operator
// asked; it never reaches the next start.
//
// It also keeps the time of the start. A request that could not be removed
// here, for example in a state directory that cannot be written, is older
// than that time, and stopRequested passes over it. Without that, every
// start would read the same request and exit at once.
func (s *Service) dropStopRequest() {
	s.startedAt = time.Now()
	if s.StopRequestPath == "" {
		return
	}
	// A request that is not older than this start is for this process: the
	// Operator asked while cumin run was starting. It stays. A file that
	// cannot be read is no request, and goes.
	if request, found, err := state.ReadStopRequest(s.StopRequestPath); err == nil && (!found || !request.RequestedAt.Before(s.startedAt)) {
		return
	}
	removed, err := state.RemoveStopRequest(s.StopRequestPath)
	switch {
	case err != nil:
		s.logger().Warn("the stop request of an earlier start was not removed", "error", err.Error())
	case removed:
		s.logger().Info("removed the stop request of an earlier start; cumin runs as usual")
	}
}

// stopRequested reads the stop request at the start of a poll, and says
// whether cumin stops after its runs. Once a request was read, cumin goes on that way until it
// exits. A file that cannot be read is no request, and is logged.
func (s *Service) stopRequested() bool {
	if s.finishing.Load() {
		return true
	}
	if s.StopRequestPath == "" {
		return false
	}
	request, found, err := state.ReadStopRequest(s.StopRequestPath)
	if err != nil {
		s.logger().Warn("the stop request was not read; cumin goes on as usual", "error", err.Error())
		return false
	}
	if !found || request.RequestedAt.Before(s.startedAt) {
		return false
	}
	s.finishing.Store(true)
	s.logger().Info("stop after the current runs: no new work starts; cumin exits when the agent runs have ended",
		"requested_at", request.RequestedAt, "in_progress", s.inProgressIssues())
	return true
}

// stoppedAfterRuns ends the run after a stop request: it removes the request and logs the
// line of the stop. Nothing is in progress, so no issue is named.
func (s *Service) stoppedAfterRuns() {
	s.removeStopRequest()
	s.logger().Info("stopped", "reason", "the agent runs have ended after a stop request",
		"in_progress", []string{}, "ended_within_grace", true, "grace", s.stopGrace().String())
}

// removeStopRequest removes the request when cumin run exits. A failure is
// logged: the next start removes the request, so it still ends here.
func (s *Service) removeStopRequest() {
	if _, err := state.RemoveStopRequest(s.StopRequestPath); err != nil {
		s.logger().Error("the stop request was not removed", "error", err.Error())
	}
}

// runsStarted is how many runs cumin has started since it started itself:
// agent runs and merge steps, each in its own goroutine.
func (s *Service) runsStarted() int {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	return s.started
}
