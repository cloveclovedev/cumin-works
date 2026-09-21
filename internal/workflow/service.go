package workflow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
)

// Target is one target repository with the token of cumin-core for it.
type Target struct {
	Repository config.Repository
	// Token returns an installation token for the repository. cumin run
	// passes the Token method of a github.TokenSource. Tests pass a function
	// that returns the token of the fake.
	Token func(ctx context.Context) (string, error)
}

// Service polls the target repositories and applies the rules.
type Service struct {
	GitHub  *github.AppClient
	Targets []Target
	// MaxIssuesInProgress is the setting max_issues_in_progress.
	MaxIssuesInProgress int
	// RequestCommand is the setting request_command: the executable that a
	// request runs. Empty runs nothing.
	RequestCommand string
	// PollInterval is the setting poll_interval.
	PollInterval time.Duration
	// Labels are the labels that Run creates in each target repository when
	// they are missing. RepositoryLabels gives the list of cumin.
	Labels []github.Label
	Logger *slog.Logger
}

// Run creates the missing labels in each target repository, then polls at
// once and after every PollInterval, until ctx ends. A failed poll is logged,
// and the loop continues. Run returns nil when ctx ends.
func (s *Service) Run(ctx context.Context) error {
	if s.PollInterval <= 0 {
		return errors.New("workflow: the poll interval must be more than 0")
	}
	s.ensureLabels(ctx)
	ticker := time.NewTicker(s.PollInterval)
	defer ticker.Stop()
	for {
		// Poll logs its own failures. Run keeps the loop.
		_ = s.Poll(ctx)
		select {
		case <-ctx.Done():
			s.logger().Info("stopped", "reason", context.Cause(ctx).Error())
			return nil
		case <-ticker.C:
		}
	}
}

// ensureLabels creates the missing labels of each target repository. A
// failure is logged; the poll still runs, so that a repository without the
// labels does not stop the others.
func (s *Service) ensureLabels(ctx context.Context) {
	for _, target := range s.Targets {
		log := s.logger().With("repository", target.Repository.String())
		token, err := target.Token(ctx)
		if err != nil {
			log.Error("create the missing labels: no token", "error", err.Error())
			continue
		}
		created, err := s.GitHub.EnsureLabels(ctx, token, target.Repository.Owner, target.Repository.Name, s.Labels)
		for _, name := range created {
			log.Info("created the label", "label", name)
		}
		if err != nil {
			log.Error("create the missing labels failed", "error", err.Error())
		}
	}
}

// Poll does one poll of every target repository: read the snapshot, decide,
// and apply the actions. A failure in one repository does not stop the
// others. The returned error joins the failures.
func (s *Service) Poll(ctx context.Context) error {
	var errs []error
	for _, target := range s.Targets {
		if err := s.pollRepository(ctx, target); err != nil {
			s.logger().Error("poll failed", "repository", target.Repository.String(), "error", err.Error())
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Service) pollRepository(ctx context.Context, target Target) error {
	owner, repo := target.Repository.Owner, target.Repository.Name
	token, err := target.Token(ctx)
	if err != nil {
		return err
	}
	read, err := s.GitHub.ReadSnapshot(ctx, token, owner, repo)
	if err != nil {
		return err
	}
	snapshot := toSnapshot(read)
	s.logger().Info("poll", "repository", target.Repository.String(),
		"requirement_issues", len(snapshot.RequirementIssues),
		"rate_limit_cost", read.RateLimit.Cost, "rate_limit_remaining", read.RateLimit.Remaining)

	var errs []error
	for _, action := range Decide(snapshot, s.MaxIssuesInProgress) {
		switch a := action.(type) {
		case Claim:
			if err := s.claim(ctx, token, target, snapshot, a); err != nil {
				errs = append(errs, err)
			}
		default:
			errs = append(errs, fmt.Errorf("unknown action %T", action))
		}
	}
	return errors.Join(errs...)
}

// claim applies I1: replace the status label of the sub-issue with
// cumin/status/implementing, and only then request the work. When the label
// change fails, nothing is requested; the next poll decides again.
func (s *Service) claim(ctx context.Context, token string, target Target, snapshot Snapshot, c Claim) error {
	owner, repo := target.Repository.Owner, target.Repository.Name
	sub, ok := snapshot.SubIssue(c.Number)
	if !ok {
		return fmt.Errorf("I1: issue #%d is not in the snapshot", c.Number)
	}
	labels := LabelsAfterClaim(sub.Labels)
	if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, c.Number, labels); err != nil {
		return fmt.Errorf("I1: claim issue #%d: %w", c.Number, err)
	}
	s.logger().Info("I1: claimed the issue", "repository", target.Repository.String(),
		"issue", c.Number, "requirement_issue", c.RequirementIssue, "labels", labels)
	if err := s.request(ctx, target.Repository, c.Number); err != nil {
		return fmt.Errorf("I1: request the work for issue #%d: %w", c.Number, err)
	}
	return nil
}

// toSnapshot converts what the GitHub client read to the snapshot of the
// rules. The types of the platform package stop here.
func toSnapshot(read github.RepositorySnapshot) Snapshot {
	var snapshot Snapshot
	for _, issue := range read.RequirementIssues {
		requirement := RequirementIssue{Number: issue.Number, Labels: issue.Labels}
		for _, sub := range issue.SubIssues {
			subIssue := SubIssue{Number: sub.Number, Closed: sub.Closed, Labels: sub.Labels}
			for _, blocker := range sub.BlockedBy {
				subIssue.BlockedBy = append(subIssue.BlockedBy, BlockedBy{Number: blocker.Number, Closed: blocker.Closed})
			}
			requirement.SubIssues = append(requirement.SubIssues, subIssue)
		}
		snapshot.RequirementIssues = append(snapshot.RequirementIssues, requirement)
	}
	return snapshot
}

func (s *Service) logger() *slog.Logger {
	if s.Logger == nil {
		return slog.Default()
	}
	return s.Logger
}
