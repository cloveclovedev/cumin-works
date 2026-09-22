package workflow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/agent"
	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/notify"
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
	// Notify tells the Owner that an issue needs an answer. cmd/cumin
	// builds it from the webhook URL in the Keychain. A nil notifier
	// reports that no channel is configured, which is logged.
	Notify *notify.Notifier
	// Settings are the Host settings. Each poll applies the
	// .cumin/config.toml of a repository over them, for the keys that a
	// repository may set.
	Settings *config.Settings
	// SettingsDir is the directory of the Host settings file. The optional
	// risk-criteria.md of the Host stands next to it. An empty value skips
	// that level.
	SettingsDir string
	// PollInterval is the setting poll_interval.
	PollInterval time.Duration
	// StopGrace is how long Run waits for the requests that are running,
	// after SIGINT or SIGTERM ended the context. Zero means
	// DefaultStopGrace. Tests shorten it.
	StopGrace time.Duration
	// Labels are the labels that Run creates in each target repository when
	// they are missing. RepositoryLabels gives the list of cumin.
	Labels []github.Label
	Logger *slog.Logger

	// running counts the agent runs that the polls started. Each run has
	// its own goroutine, so that the poll goes on while an agent works.
	running sync.WaitGroup
	// inProgress holds the issues whose agent is running, for the log of
	// the stop.
	progressMu sync.Mutex
	inProgress map[inProgressKey]bool

	// repositorySettings keeps what each repository's .cumin/ decided,
	// until a blob of those files changes. Poll reads and writes it, and a
	// test may poll from more than one goroutine.
	settingsMu         sync.Mutex
	repositorySettings map[string]*RepositorySettings
}

// DefaultStopGrace is how long Run waits for the requests that are running
// after the stop signal, when there is no agent service to ask. It must be
// longer than the grace of the agent adapter, because that grace is only
// the time between SIGTERM and SIGKILL; the adapter still has to end the
// CLI and to kill its process group afterwards. Ending the wait too early
// would leave a CLI alive with the token of its role, and launchd does not
// reach it: the CLI runs in its own process group.
//
// The ExitTimeOut of the LaunchAgent is longer than this
// (docs/ja/designs/cumin-core.md, the topic on launchd).
const DefaultStopGrace = 15 * time.Second

// inProgressKey is one issue whose agent is running.
type inProgressKey struct {
	repository string
	issue      int
}

// Run creates the missing labels in each target repository, then polls at
// once and after every PollInterval, until ctx ends. A failed poll is logged,
// and the loop continues.
//
// When ctx ends (SIGINT or SIGTERM), Run starts no new work, and the runs
// that are going on end because they use ctx: the adapter sends SIGTERM to
// the process group of the CLI and SIGKILL after the grace. Run waits for
// them for StopGrace, logs the issues that were in progress, and returns
// nil, so that `cumin run` exits with 0 and launchd leaves it stopped.
//
// No label is changed on the way out. An issue that was in progress keeps
// cumin/status/implementing, and the Owner restarts it with
// cumin/status/ready (issue-states.md, the section on what v0.1 does not
// build).
func (s *Service) Run(ctx context.Context) error {
	if s.PollInterval <= 0 {
		return errors.New("workflow: the poll interval must be more than 0")
	}
	if s.Settings == nil {
		return errors.New("workflow: no Host settings are configured")
	}
	s.ensureLabels(ctx)
	ticker := time.NewTicker(s.PollInterval)
	defer ticker.Stop()
	for {
		// Poll logs its own failures. Run keeps the loop.
		_ = s.Poll(ctx)
		select {
		case <-ctx.Done():
			s.stop(context.Cause(ctx).Error())
			return nil
		case <-ticker.C:
		}
	}
}

// Wait waits for the agent runs that the polls started. A test calls it
// after a poll, to read what the run did.
func (s *Service) Wait() { s.running.Wait() }

// stop ends the run: it waits for the requests that are going on, for at
// most the grace, and logs one line with the issues that were in progress.
func (s *Service) stop(reason string) {
	issues := s.inProgressIssues()
	grace := s.stopGrace()
	ended := make(chan struct{})
	// The goroutine outlives stop when a request does not end inside the
	// grace. The process is on its way out, so nothing waits for it.
	go func() {
		s.running.Wait()
		close(ended)
	}()
	timer := time.NewTimer(grace)
	defer timer.Stop()
	endedInGrace := true
	select {
	case <-ended:
	case <-timer.C:
		endedInGrace = false
	}
	s.logger().Info("stopped", "reason", reason,
		"in_progress", issues, "ended_within_grace", endedInGrace, "grace", grace.String())
}

// stopGrace is how long the stop waits for the requests that are running.
// The value of the agent service is the one to use, because it knows the
// grace of its adapter and what it needs after it.
func (s *Service) stopGrace() time.Duration {
	switch {
	case s.StopGrace > 0:
		return s.StopGrace
	case s.Agents != nil:
		return s.Agents.StopBudget()
	default:
		return DefaultStopGrace
	}
}

// inProgressIssues returns the issues whose agent is running, as
// "<owner>/<repo>#<number>", in a fixed order.
func (s *Service) inProgressIssues() []string {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	issues := make([]string, 0, len(s.inProgress))
	for key := range s.inProgress {
		issues = append(issues, fmt.Sprintf("%s#%d", key.repository, key.issue))
	}
	slices.Sort(issues)
	return issues
}

// markInProgress records that the agent of an issue is running, and
// returns the function that removes it when the run ends.
//
// After the stop signal the entry stays, even when the run ends at once
// because its CLI follows SIGTERM. The issue keeps
// cumin/status/implementing in that case, so the Owner has to restart it,
// and the line of the stop must name it. Nothing removes entries after the
// signal; the process is on its way out.
func (s *Service) markInProgress(ctx context.Context, repository string, issue int) func() {
	key := inProgressKey{repository: repository, issue: issue}
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	if s.inProgress == nil {
		s.inProgress = map[inProgressKey]bool{}
	}
	s.inProgress[key] = true
	return func() {
		s.progressMu.Lock()
		defer s.progressMu.Unlock()
		if ctx.Err() != nil {
			return
		}
		delete(s.inProgress, key)
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
		// The stop signal came while this poll was running. Start nothing
		// more: the requests that are going on are the ones to wait for.
		if ctx.Err() != nil {
			break
		}
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
	log := s.logger().With("repository", target.Repository.String())

	// The settings of this repository. A wrong file skips this repository
	// and this poll; the other repositories are polled by the caller.
	settings, readAgain, err := s.settingsFor(target.Repository, read)
	if err != nil {
		return err
	}
	if readAgain {
		log.Info("the settings of the repository were read",
			"from_repository", settings.FromRepository, "risk_criteria", settings.RiskCriteriaSource)
	}
	log.Info("poll",
		"requirement_issues", len(snapshot.RequirementIssues),
		"settings", settingsSource(settings.FromRepository), "risk_criteria", settings.RiskCriteriaSource,
		"rate_limit_cost", read.RateLimit.Cost, "rate_limit_remaining", read.RateLimit.Remaining)

	var errs []error
	for _, action := range Decide(snapshot, s.Settings.MaxIssuesInProgress) {
		switch a := action.(type) {
		case Claim:
			if err := s.claim(ctx, token, target, snapshot, settings, a); err != nil {
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
func (s *Service) claim(ctx context.Context, token string, target Target, snapshot Snapshot, settings *RepositorySettings, c Claim) error {
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
	if err := s.startImplementer(ctx, target, settings, sub); err != nil {
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
func (s *Service) startImplementer(ctx context.Context, target Target, settings *RepositorySettings, sub SubIssue) error {
	if s.Agents == nil {
		return errors.New("no agent service is configured")
	}
	instruction, err := roles.Instruction(config.RoleImplementer)
	if err != nil {
		return err
	}
	branch := BranchName(sub.Number, sub.Title)
	done := s.markInProgress(ctx, target.Repository.String(), sub.Number)
	s.running.Add(1)
	go func() {
		defer s.running.Done()
		defer done()
		s.runImplementer(ctx, target, settings, sub.Number, branch, instruction)
	}()
	return nil
}

// runImplementer prepares the worktree and runs one Implementer request to
// its end. The end of the run is the trigger of I2: a done result goes to
// verifyDone, and a blocked result stops the issue for the Owner. An
// abnormal end is logged only; the retry and the stop after it are a later
// requirement.
func (s *Service) runImplementer(ctx context.Context, target Target, settings *RepositorySettings, number int, branch, instruction string) {
	log := s.logger().With("repository", target.Repository.String(), "issue", number, "role", config.RoleImplementer)
	role := settings.Settings.Roles[config.RoleImplementer]
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
		Settings:        &role,
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
			s.stopAfterBlocked(ctx, log, target, settings, number, run.Result.BlockedReason)
			return
		}
		s.verifyDone(ctx, log, target, number, workDir, run.BotLogin)
	}
}

// stopAfterBlocked applies I2 for a blocked result: the blocked_reason of
// the agent becomes the comment, because the agent already wrote it in the
// form of templates/decision-request.md, and its first line is the
// question for the Owner. Nothing is retried: a blocked result usually
// means that a requirement is missing, so the Owner answers first
// (issue-states.md, the paragraph on a blocked result of the Implementer).
func (s *Service) stopAfterBlocked(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, number int, reason string) {
	question := firstLine(reason)
	log.Warn("the agent returned blocked", "reason", question)
	s.stopForOwner(ctx, log, target, settings, stop{
		row:     RowI2,
		issue:   number,
		labels:  s.issueLabels(ctx, log, target, number),
		reason:  "the Implementer returned blocked: " + question,
		comment: reason,
	})
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

// settingsSource names where the settings of a poll came from, for the log.
func settingsSource(fromRepository bool) string {
	if fromRepository {
		return "repository"
	}
	return "host"
}

func (s *Service) logger() *slog.Logger {
	if s.Logger == nil {
		return slog.Default()
	}
	return s.Logger
}
