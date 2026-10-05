package workflow

import (
	"slices"
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

// decideReadyOfOwner is Decide on a snapshot where the Owner added every
// cumin/status/ready, as the poll stores it after its read
// (readReadyOwners). A test of another condition of R1 or of I1 uses it.
func decideReadyOfOwner(snapshot Snapshot, maxInProgress int, required []RequiredCheck, priority []string, now time.Time, checksWait time.Duration) []Action {
	copied := Snapshot{RequirementIssues: slices.Clone(snapshot.RequirementIssues), Running: snapshot.Running}
	for i := range copied.RequirementIssues {
		requirement := &copied.RequirementIssues[i]
		requirement.ReadyRead, requirement.ReadyOwner = true, "the-owner"
		requirement.SubIssues = slices.Clone(requirement.SubIssues)
		for j := range requirement.SubIssues {
			requirement.SubIssues[j].ReadyRead, requirement.SubIssues[j].ReadyOwner = true, "the-owner"
		}
	}
	return Decide(copied, maxInProgress, required, priority, now, checksWait)
}

// InProgressIssues returns the issues in work of the service: the issues
// whose agent runs.
func InProgressIssues(s *Service) []string { return s.inProgressIssues() }
