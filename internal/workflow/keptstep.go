package workflow

import (
	"cmp"
	"context"
	"log/slog"
	"slices"
	"time"
)

// keptStepDelay is how long a kept step waits for its next try
// (cumin-core.md, the topic on a failed call to GitHub). It is a fixed
// value, not a setting.
const keptStepDelay = 5 * time.Minute

// keptStep is a step after an agent run that ended with a temporary failure
// of GitHub. cumin keeps it, and a later poll runs it again from its start.
// It lives in memory: a restart of cumin loses it.
type keptStep struct {
	// name is the action in plain words, for the log.
	name string
	log  *slog.Logger
	// run runs the step again. It reads GitHub first, so a write that
	// already happened is not made twice. It returns an error only for a
	// temporary failure.
	run func(ctx context.Context) error
	// rest is what follows a try that ended without a failure: work that
	// can take long, such as an agent run. run sets it at every
	// try, or leaves it nil. It runs once, and it is never tried again.
	rest func(ctx context.Context)
	// next is the time of the next try. A poll before it leaves the step.
	next time.Time
}

// keepStep keeps a step after a temporary failure, and logs one line with
// the reason and the time of the next try. The issue stays in the set of
// issues in work: it holds its slot and its label, and no agent starts for
// it.
func (s *Service) keepStep(key inProgressKey, step *keptStep, reason error) {
	step.next = s.now().Add(keptStepDelay)
	s.progressMu.Lock()
	if s.keptSteps == nil {
		s.keptSteps = map[inProgressKey]*keptStep{}
	}
	s.keptSteps[key] = step
	s.progressMu.Unlock()
	step.log.Warn("kept "+step.name+" after a temporary failure: a later poll runs it again",
		"reason", reason.Error(), "next_try", step.next.UTC().Format(time.RFC3339))
}

// tryStep runs the first try of a step, at the end of an agent run: a
// temporary failure keeps the step, and a try that ended goes on with the
// rest of the step. A try of the kept step runs in a poll (runKeptSteps).
func (s *Service) tryStep(ctx context.Context, key inProgressKey, step *keptStep) {
	if err := step.run(ctx); err != nil {
		if ctx.Err() == nil {
			s.keepStep(key, step, err)
		}
		return
	}
	if step.rest != nil {
		step.rest(ctx)
	}
}

// dueKeptSteps takes the kept steps whose time has come, in a fixed order.
// It moves their next try, so that a second poll does not run them too.
func (s *Service) dueKeptSteps(now time.Time) []inProgressKey {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	var due []inProgressKey
	for key, step := range s.keptSteps {
		if now.Before(step.next) {
			continue
		}
		step.next = now.Add(keptStepDelay)
		due = append(due, key)
	}
	slices.SortFunc(due, func(a, b inProgressKey) int {
		return cmp.Or(cmp.Compare(a.repository, b.repository), cmp.Compare(a.issue, b.issue))
	})
	return due
}

// runKeptSteps runs each kept step that is due. A step that ends leaves the
// set of issues in work, as the end of an agent run does. A step that fails
// again with a temporary failure waits for the delay once more. Before the
// reset of a full rate limit the client sends nothing, so the step fails
// again and waits. The rest of a step that ended runs in its own goroutine,
// so that the poll does not wait for an agent run; the issue stays in the
// set of issues in work until the rest ends.
func (s *Service) runKeptSteps(ctx context.Context) {
	for _, key := range s.dueKeptSteps(s.now()) {
		if ctx.Err() != nil {
			return
		}
		s.progressMu.Lock()
		step := s.keptSteps[key]
		s.progressMu.Unlock()
		err := step.run(ctx)
		if ctx.Err() != nil {
			// cumin is stopping. The entry stays, as after an agent run.
			return
		}
		if err != nil {
			s.keepStep(key, step, err)
			continue
		}
		rest := step.rest
		s.progressMu.Lock()
		delete(s.keptSteps, key)
		if rest == nil {
			s.endInProgress(key)
			s.progressMu.Unlock()
			continue
		}
		s.progressMu.Unlock()
		s.running.Add(1)
		go func() {
			defer s.running.Done()
			defer s.endRun(ctx, key)
			rest(ctx)
		}()
	}
}
