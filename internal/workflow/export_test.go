package workflow

import (
	"time"

	"github.com/cloveclovedev/cumin-works/internal/agent"
	"github.com/cloveclovedev/cumin-works/internal/quota"
)

// StopGraceOf is how long the stop of the service waits for the requests
// that are running. A test outside the package reads it through this
// function.
func StopGraceOf(s *Service) time.Duration { return s.stopGrace() }

// KeepUsage stores one reading as the end of a run or a minimal run does,
// and returns the usage that the decision uses.
func KeepUsage(s *Service, read agent.QuotaUsage) quota.Usage { return s.keepUsage(s.logger(), read) }
