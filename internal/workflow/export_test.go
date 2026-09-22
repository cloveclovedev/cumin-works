package workflow

import "time"

// StopGraceOf is how long the stop of the service waits for the requests
// that are running. A test outside the package reads it through this
// function.
func StopGraceOf(s *Service) time.Duration { return s.stopGrace() }
