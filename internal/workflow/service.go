package workflow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/agent"
	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/roles"
)

// Target is one target repository with the token of cumin-core for it.
type Target struct {
	Repository config.Repository
	// RemoteURL is the address that the clone of the work directory uses.
	// cumin run passes https://github.com/<owner>/<repo>.git; a test passes
	// a local bare repository.
	RemoteURL string
	// Token returns an installation token for the repository. cumin run
	// passes the Token method of a github.TokenSource. Tests pass a function
	// that returns the token of the fake.
	Token func(ctx context.Context) (string, error)
}

// Service polls the target repositories and applies the rules.
type Service struct {
	GitHub  *github.AppClient
	Targets []Target
	// Agents starts the agents. A claim without it is an error.
	Agents *agent.Service
	// Workspace holds the clone and the worktrees of each repository, under
	// the setting work_dir.
	Workspace agent.Workspace
	// MaxIssuesInProgress is the setting max_issues_in_progress.
	MaxIssuesInProgress int
	// PollInterval is the setting poll_interval.
	PollInterval time.Duration
	// Labels are the labels that Run creates in each target repository when
	// they are missing. RepositoryLabels gives the list of cumin.
	Labels []github.Label
	Logger *slog.Logger

	// running counts the agent runs that the polls started. Each run has
	// its own goroutine, so that the poll goes on while an agent works.
	running sync.WaitGroup
}

// Run creates the missing labels in each target repository, then polls at
// once and after every PollInterval, until ctx ends. A failed poll is logged,
// and the loop continues. When ctx ends, Run waits for the running agents
// before it returns nil. The runs use ctx, so the end of ctx ends them as an
// abnormal end of the kind "time limit".
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
			reason := context.Cause(ctx).Error()
			s.Wait()
			s.logger().Info("stopped", "reason", reason)
			return nil
		case <-ticker.C:
		}
	}
}

// Wait waits for the agent runs that the polls started. Run calls it when
// ctx ends; a test calls it after a poll, to read what the run did.
func (s *Service) Wait() { s.running.Wait() }

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
	if err := s.startImplementer(ctx, target, sub); err != nil {
		return fmt.Errorf("I1: request the work for issue #%d: %w", c.Number, err)
	}
	return nil
}

// startImplementer requests the work of I1 from the Implementer. The
// request kind is always "implement": the continuation request, which uses
// the branch of an existing pull request, is a later requirement.
//
// The work runs in its own goroutine, so that the poll goes on while the
// agent works. What the goroutine does (the worktree, the start, the end of
// the run) is only logged: the label stays cumin/status/implementing,
// because no rule of v0.1 takes it back (issue-states.md, the section on
// what v0.1 does not build).
func (s *Service) startImplementer(ctx context.Context, target Target, sub SubIssue) error {
	if s.Agents == nil {
		return errors.New("no agent service is configured")
	}
	instruction, err := roles.Instruction(config.RoleImplementer)
	if err != nil {
		return err
	}
	branch := BranchName(sub.Number, sub.Title)
	s.running.Add(1)
	go func() {
		defer s.running.Done()
		s.runImplementer(ctx, target, sub.Number, branch, instruction)
	}()
	return nil
}

// runImplementer prepares the worktree and runs one Implementer request to
// its end. The end of the run is the trigger of I2 (verifyDone) for a done
// result. A blocked result and an abnormal end are logged only; #81 builds
// the label change, the comment, and the notification for them.
func (s *Service) runImplementer(ctx context.Context, target Target, number int, branch, instruction string) {
	log := s.logger().With("repository", target.Repository.String(), "issue", number, "role", config.RoleImplementer)
	workDir, err := s.Workspace.Prepare(ctx, target.RemoteURL, agent.Checkout{
		Owner:  target.Repository.Owner,
		Repo:   target.Repository.Name,
		Issue:  number,
		Role:   config.RoleImplementer,
		Branch: branch,
	})
	if err != nil {
		log.Error("I1: the work directory was not prepared", "error", err.Error())
		return
	}
	log.Info("I1: requested the work", "branch", branch)
	run, err := s.Agents.Start(ctx, agent.StartRequest{
		Owner:           target.Repository.Owner,
		Repo:            target.Repository.Name,
		Role:            config.RoleImplementer,
		RoleInstruction: instruction,
		Text:            ImplementRequestText(target.Repository.String(), number, branch, workDir),
		WorkDir:         workDir,
	})
	var abnormal *agent.AbnormalEnd
	switch {
	case errors.As(err, &abnormal):
		log.Info("the agent run ended abnormally", "kind", abnormal.Kind.String(),
			"session_id", abnormal.SessionID, "detail", abnormal.Detail)
	case err != nil:
		log.Error("the agent was not started", "error", err.Error())
	default:
		log.Info("the agent run ended", "result", run.Result.Result, "session_id", run.SessionID)
		if run.Result.Result != agent.ResultDone {
			// blocked. The first line of blocked_reason is the question
			// (decision-request.md). The label stays.
			log.Warn("the agent returned blocked", "reason", firstLine(run.Result.BlockedReason))
			return
		}
		s.verifyDone(ctx, log, target, number, workDir, run.BotLogin)
	}
}

// verifyDone applies I2 after a done result. It reads the snapshot of the
// repository again, because a rule that the end of a run triggers judges on
// the facts of that moment, not on those of the last poll (cumin-core.md,
// the topic on the GitHub client). It then reads the head commit of the
// worktree and runs the pure check.
//
// On a pass the status label becomes cumin/status/awaiting-checks. A failed
// check is logged with its kind and the label stays; #81 reads the same
// value to change the label, comment, and notify. Nothing here is retried:
// the next poll reads the facts again.
func (s *Service) verifyDone(ctx context.Context, log *slog.Logger, target Target, number int, workDir, botLogin string) {
	owner, repo := target.Repository.Owner, target.Repository.Name
	token, err := target.Token(ctx)
	if err != nil {
		log.Error("I2: no token", "error", err.Error())
		return
	}
	read, err := s.GitHub.ReadSnapshot(ctx, token, owner, repo)
	if err != nil {
		log.Error("I2: the snapshot was not read again", "error", err.Error())
		return
	}
	sub, ok := toSnapshot(read).SubIssue(number)
	if !ok {
		log.Error("I2: the issue is not in the snapshot")
		return
	}
	head, err := s.Workspace.Head(ctx, workDir)
	if err != nil {
		log.Error("I2: the head commit of the work directory was not read", "error", err.Error())
		return
	}
	verification := VerifyDone(sub, botLogin, head)
	if !verification.Passed {
		log.Warn("I2: the verification failed", "failure", verification.Failure.String(),
			"pull_request", verification.PullRequest)
		return
	}
	labels := ReplaceStatusLabel(sub.Labels, LabelAwaitingChecks)
	if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, number, labels); err != nil {
		log.Error("I2: the label was not changed", "error", err.Error())
		return
	}
	log.Info("I2: verified the pull request", "pull_request", verification.PullRequest, "labels", labels)
}

// firstLine is the first line of s, for one log field. The first line of a
// blocked_reason is the question that the Owner must answer.
func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

// toSnapshot converts what the GitHub client read to the snapshot of the
// rules. The types of the platform package stop here.
func toSnapshot(read github.RepositorySnapshot) Snapshot {
	var snapshot Snapshot
	for _, issue := range read.RequirementIssues {
		requirement := RequirementIssue{Number: issue.Number, Labels: issue.Labels}
		for _, sub := range issue.SubIssues {
			subIssue := SubIssue{Number: sub.Number, Title: sub.Title, Closed: sub.Closed, Labels: sub.Labels}
			for _, blocker := range sub.BlockedBy {
				subIssue.BlockedBy = append(subIssue.BlockedBy, BlockedBy{Number: blocker.Number, Closed: blocker.Closed})
			}
			for _, pr := range sub.PullRequests {
				subIssue.PullRequests = append(subIssue.PullRequests, PullRequest{Number: pr.Number, HeadCommit: pr.HeadCommit, Author: pr.Author})
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
