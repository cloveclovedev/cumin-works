package workflow

// This file is the drain: the Owner asked cumin to finish the agent runs
// that are going on, to start no new work, and then to exit (`cumin stop
// --after-current-runs`). The request is a file in the state directory of
// the Host. docs/ja/designs/cumin-core.md, the topic on the stop.

import (
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/state"
)

// dropDrainRequest removes a drain request that was written before this
// start of cumin run. A request is for the process that ran when the Owner
// asked; it never reaches the next start.
//
// It also keeps the time of the start. A request that could not be removed
// here, for example in a state directory that cannot be written, is older
// than that time, and drainRequested passes over it. Without that, every
// start would read the same request and exit at once.
func (s *Service) dropDrainRequest() {
	s.startedAt = time.Now()
	if s.DrainPath == "" {
		return
	}
	// A request that is not older than this start is for this process: the
	// Owner asked while cumin run was starting. It stays. A file that
	// cannot be read is no request, and goes.
	if request, found, err := state.ReadDrain(s.DrainPath); err == nil && (!found || !request.RequestedAt.Before(s.startedAt)) {
		return
	}
	removed, err := state.RemoveDrain(s.DrainPath)
	switch {
	case err != nil:
		s.logger().Warn("the drain request of an earlier start was not removed", "error", err.Error())
	case removed:
		s.logger().Info("removed the drain request of an earlier start; cumin runs as usual")
	}
}

// drainRequested reads the drain request at the start of a poll, and says
// whether cumin drains. Once a request was read, cumin drains until it
// exits. A file that cannot be read is no request, and is logged.
func (s *Service) drainRequested() bool {
	if s.draining.Load() {
		return true
	}
	if s.DrainPath == "" {
		return false
	}
	request, found, err := state.ReadDrain(s.DrainPath)
	if err != nil {
		s.logger().Warn("the drain request was not read; cumin goes on as usual", "error", err.Error())
		return false
	}
	if !found || request.RequestedAt.Before(s.startedAt) {
		return false
	}
	s.draining.Store(true)
	s.logger().Info("drain: no new work starts; cumin exits when the agent runs have ended",
		"requested_at", request.RequestedAt, "in_progress", s.inProgressIssues())
	return true
}

// drained ends the run after a drain: it removes the request and logs the
// line of the stop. Nothing is in progress, so no issue is named.
func (s *Service) drained() {
	if _, err := state.RemoveDrain(s.DrainPath); err != nil {
		// The next start removes it, so the request still ends here.
		s.logger().Error("the drain request was not removed", "error", err.Error())
	}
	s.logger().Info("stopped", "reason", "drain: the agent runs have ended",
		"in_progress", []string{}, "ended_within_grace", true, "grace", s.stopGrace().String())
}

// runsStarted is how many runs cumin has started since it started itself:
// agent runs and merge steps, each in its own goroutine.
func (s *Service) runsStarted() int {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	return s.started
}
