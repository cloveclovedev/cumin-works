package workflow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/agent"
	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/core/state"
	"github.com/cloveclovedev/cumin-works/internal/notify"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
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
	// Login returns the login of the bot of cumin-core, "<slug>[bot]". I9
	// uses it to find its own follow-up notes. cumin run passes the
	// BotLogin method of the same github.TokenSource. Without it, cumin
	// writes no follow-up note.
	Login func(ctx context.Context) (string, error)
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
	// State is what cumin keeps on the Host for each implementation issue:
	// the session of the last run, and the number of check fix requests
	// (I4); and for each requirement issue in cumin/status/accepting: the
	// session of the Planner, and whether the acceptance check was requested
	// again. A nil store keeps nothing, which is the same as losing the
	// file: the next request starts a new session and counts from zero.
	State *state.Store
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
	// IdlePollInterval is the setting idle_poll_interval: how often a
	// poll reads a repository with no issue in work. Zero means that every
	// poll reads every repository.
	IdlePollInterval time.Duration
	// StopGrace is how long Run waits for the requests that are running,
	// after SIGINT or SIGTERM ended the context. Zero means
	// DefaultStopGrace. Tests shorten it.
	StopGrace time.Duration
	// CloseWait is how long the merge step waits for GitHub to close the
	// implementation issue before cumin reads it (I6, I12). Zero means
	// DefaultCloseWait. Tests shorten it.
	CloseWait time.Duration
	// Labels are the labels that Run creates in each target repository when
	// they are missing. RepositoryLabels gives the list of cumin.
	Labels []github.Label
	Logger *slog.Logger
	// AllowancePath is the allowance file that `cumin quota allow` writes
	// (Q2). Each check before a start reads it. Empty means no allowance.
	AllowancePath string
	// Now is the clock of the quota decisions (Q1). Nil means time.Now.
	// Tests set it, so that a week passes without waiting.
	Now func() time.Time
	// Location is the time zone of the time bands of the 5h quota window.
	// Nil means time.Local, the zone of the Host. Tests set it, so that
	// the zone of the machine does not change a result.
	Location *time.Location
	// StopRequestPath is the stop request file that `cumin stop
	// --after-current-runs` writes (stopafterruns.go). Each poll reads it. Empty
	// means that cumin never stops that way.
	StopRequestPath string

	// running counts the agent runs that the polls started. Each run has
	// its own goroutine, so that the poll goes on while an agent works.
	running sync.WaitGroup
	// inProgress holds the issues whose agent is running, for the log of
	// the stop.
	progressMu sync.Mutex
	inProgress map[inProgressKey]bool
	// started counts the runs that cumin started. progressMu guards it.
	started int
	// keptSteps holds the steps after an agent run that wait for their next
	// try (keptstep.go). progressMu guards it.
	keptSteps map[inProgressKey]*keptStep
	// startedAt is when Run started. A stop request from before it is
	// for an earlier process (stopafterruns.go). Only Run and its polls use it.
	startedAt time.Time
	// finishing says that a poll read a stop request. It stays true until
	// cumin exits.
	finishing atomic.Bool
	// runEnded wakes Run when a run ends, so that a stop after the runs does not wait for
	// the next tick. Run makes it; without Run, nothing is sent.
	runEnded chan struct{}

	// lastPolls keeps, for each repository, what its last poll left, so
	// that a repository with no issue in work waits for the idle poll
	// interval. It lives in memory: after a restart every repository is
	// polled at once.
	lastPollMu sync.Mutex
	lastPolls  map[string]*lastPoll

	// repositorySettings keeps what each repository's .cumin/ decided,
	// until a blob of those files changes. Poll reads and writes it, and a
	// test may poll from more than one goroutine.
	settingsMu         sync.Mutex
	repositorySettings map[string]*RepositorySettings
	// priorityLabelsDone says, for each repository, that its default
	// priority labels exist or that its settings name its own. settingsMu
	// guards it.
	priorityLabelsDone map[string]bool

	// pollFailures counts the consecutive failed polls of each repository,
	// so that a failure that repeats reaches the Owner once
	// (pollfailure.go).
	failureMu sync.Mutex
	// readyTold holds, for each issue, the time of the cumin/status/ready
	// event of another account than the Owner that cumin already logged
	// and told the Owner about. cumin can lose it: after a restart it
	// tells the Owner once more.
	readyMu      sync.Mutex
	readyTold    map[string]time.Time
	pollFailures map[string]*repeatedFailure

	// quota keeps which Q1 notifications the Owner already got
	// (quota.go). The polls and the ends of the runs share it.
	quotaMu sync.Mutex
	quota   quotaNotices
	// waitingTold says that the Owner heard Q4 (waiting) since cumin last
	// did something (waiting.go). quotaMu guards it.
	waitingTold bool

	// kept are the worktrees of closed issues that the cleanup kept,
	// because they hold work that is not on GitHub. The cleanup does not
	// check them again until cumin restarts, so the log says so once.
	keptMu sync.Mutex
	kept   map[string]bool
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
// once and after every PollInterval, until ctx ends. A poll passes over a
// repository with no issue in work until IdlePollInterval passed since its
// last poll. A failed poll is logged,
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
//
// A stop request (stopafterruns.go) ends Run in another way: the polls go on and
// ask no agent for new work, the runs that are going on end by themselves,
// and Run returns nil after a poll that began and ended with no run in
// progress and started none. That last poll carries what the last run left
// to its next state, as far as no agent is needed. A stop signal during
// that wait stops at once, as above.
func (s *Service) Run(ctx context.Context) error {
	if s.PollInterval <= 0 {
		return errors.New("workflow: the poll interval must be more than 0")
	}
	if s.Settings == nil {
		return errors.New("workflow: no Host settings are configured")
	}
	s.dropStopRequest()
	s.runEnded = make(chan struct{}, 1)
	s.ensureLabels(ctx)
	ticker := time.NewTicker(s.PollInterval)
	defer ticker.Stop()
	for {
		started, idle := s.runsStarted(), len(s.inProgressIssues()) == 0
		// Poll logs its own failures. Run keeps the loop.
		_ = s.Poll(ctx)
		finishing := s.finishing.Load()
		if finishing && ctx.Err() == nil && idle && s.runsStarted() == started && len(s.inProgressIssues()) == 0 {
			s.stoppedAfterRuns()
			return nil
		}
		// Only a stop after the runs polls at the end of a run: the usual pace of the
		// polls stays the interval.
		var runEnded <-chan struct{}
		if finishing {
			runEnded = s.runEnded
		}
		select {
		case <-ctx.Done():
			s.stop(context.Cause(ctx).Error())
			if finishing {
				// The stop request ends with the process, however it stops.
				s.removeStopRequest()
			}
			return nil
		case <-ticker.C:
		case <-runEnded:
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

// inProgressIssues returns the issues whose agent is running or whose step
// after the run is kept, as
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
// returns the function that removes it when the run ends. An issue whose
// step after the run is kept stays: the kept step removes it when it ends.
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
	s.started++
	return func() { s.endRun(ctx, key) }
}

// endRun removes an issue from the set of issues in work when its run ends.
// See markInProgress for the two cases that leave the entry.
func (s *Service) endRun(ctx context.Context, key inProgressKey) {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	if ctx.Err() != nil || s.keptSteps[key] != nil {
		return
	}
	s.endInProgress(key)
}

// endInProgress removes an issue from the set of issues in work, and wakes
// Run. The caller holds progressMu.
func (s *Service) endInProgress(key inProgressKey) {
	// The note comes first, so that a poll never sees neither the run
	// nor its end.
	s.noteRunEnded(key.repository)
	delete(s.inProgress, key)
	select {
	case s.runEnded <- struct{}{}:
	default:
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

// ensurePriorityLabels creates the default priority labels of a repository
// whose settings name none, when they are missing, as ensureLabels does for
// the other labels of cumin. The settings of the repository decide it, so
// it runs in the poll: once after each read of the settings, and again at
// the next poll after a failure. Labels that a settings file names belong
// to the organization, and cumin never creates or changes them.
func (s *Service) ensurePriorityLabels(ctx context.Context, log *slog.Logger, token string, target Target, settings *RepositorySettings, readAgain bool) {
	key := repositoryKey(target.Repository)
	s.settingsMu.Lock()
	if readAgain {
		delete(s.priorityLabelsDone, key)
	}
	done := s.priorityLabelsDone[key]
	s.settingsMu.Unlock()
	if done {
		return
	}
	if settings.Settings.PriorityLabels == nil {
		labels := DefaultPriorityLabels(config.DefaultPriorityLabels())
		created, err := s.GitHub.EnsureLabels(ctx, token, target.Repository.Owner, target.Repository.Name, labels)
		for _, name := range created {
			log.Info("created the label", "label", name)
		}
		if err != nil {
			log.Error("create the missing priority labels failed", "error", err.Error())
			return
		}
	}
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	if s.priorityLabelsDone == nil {
		s.priorityLabelsDone = map[string]bool{}
	}
	s.priorityLabelsDone[key] = true
}

// Poll does one poll of every target repository: read the snapshot, decide,
// and apply the actions. A failure in one repository does not stop the
// others, and a failure that repeats tells the Owner (pollFailed). The
// returned error joins the failures.
func (s *Service) Poll(ctx context.Context) error {
	var errs []error
	var all pollResult
	finishing := s.stopRequested()
	// The kept steps come first, so that the poll reads what they wrote.
	s.runKeptSteps(ctx)
	for _, target := range s.Targets {
		// The stop signal came while this poll was running. Start nothing
		// more: the requests that are going on are the ones to wait for.
		if ctx.Err() != nil {
			return errors.Join(errs...)
		}
		// A repository with no issue in work waits for the idle poll
		// interval. A stop after the runs polls every repository, as before.
		at := s.now()
		if !s.pollIsDue(target.Repository, at) && !finishing {
			continue
		}
		result, err := s.pollRepository(ctx, target, finishing)
		s.notePoll(target.Repository, at, result, err)
		all.add(result)
		if err == nil {
			s.pollSucceeded(target.Repository)
			continue
		}
		s.logger().Error("poll failed", "repository", target.Repository.String(), "error", err.Error())
		errs = append(errs, err)
		s.pollFailed(ctx, target.Repository, err)
	}
	// Q4 sends only after a poll that read every repository; a decided
	// action ends the silence even when another repository failed. A stop request
	// holds work back, so having nothing to do is not news then.
	if !finishing {
		s.waitingCheck(ctx, all, len(errs) == 0)
	}
	return errors.Join(errs...)
}

// lastPoll is what the poll loop keeps of the last poll of a repository.
type lastPoll struct {
	LastPoll
	// at is when the poll started.
	at time.Time
	// runEnded says that an agent run of the repository ended since the
	// poll started. The next poll reads what the run left.
	runEnded bool
	// idle says that the log already told that the repository is not in work.
	idle bool
}

// pollIsDue says whether this poll reads the repository: always when the
// repository is in work, and otherwise once in each idle poll interval.
func (s *Service) pollIsDue(repository config.Repository, now time.Time) bool {
	if s.IdlePollInterval <= 0 {
		return true
	}
	key := repository.String()
	running := len(s.runningIssues(key)) > 0
	s.lastPollMu.Lock()
	defer s.lastPollMu.Unlock()
	last := s.lastPolls[key]
	if last == nil {
		return true
	}
	inWork := RepositoryInWork(last.LastPoll, running || last.runEnded)
	if !PollIsDue(inWork, now.Sub(last.at), s.PollInterval, s.IdlePollInterval) {
		return false
	}
	// This poll reads what a run that ended left.
	last.runEnded = false
	return true
}

// notePoll keeps the result of a poll of a repository for pollIsDue, and
// logs once when the repository leaves or enters the work.
func (s *Service) notePoll(repository config.Repository, at time.Time, result pollResult, err error) {
	if s.IdlePollInterval <= 0 {
		return
	}
	key := repository.String()
	running := len(s.runningIssues(key)) > 0
	s.lastPollMu.Lock()
	defer s.lastPollMu.Unlock()
	if s.lastPolls == nil {
		s.lastPolls = map[string]*lastPoll{}
	}
	last := s.lastPolls[key]
	if last == nil {
		last = &lastPoll{}
		s.lastPolls[key] = last
	}
	// A run that ended while this poll ran stays noted: the poll may have
	// read GitHub before the end.
	last.LastPoll = LastPoll{Ran: true, Failed: err != nil, Acted: result.decided, IssueInWork: result.issueInWork}
	last.at = at
	idle := !RepositoryInWork(last.LastPoll, running || last.runEnded)
	if idle != last.idle {
		log := s.logger().With("repository", key)
		if idle {
			log.Info("no issue is in work: the next poll comes after the idle poll interval",
				"idle_poll_interval", s.IdlePollInterval.String())
		} else {
			log.Info("the repository is in work again: the next poll comes after the poll interval")
		}
	}
	last.idle = idle
}

// noteRunEnded records the end of an agent run of a repository, so that
// the next poll reads the repository, also when it had no issue in work.
func (s *Service) noteRunEnded(repository string) {
	s.lastPollMu.Lock()
	defer s.lastPollMu.Unlock()
	if last := s.lastPolls[repository]; last != nil {
		last.runEnded = true
	}
}

// readSnapshot reads one repository for a poll, in two queries, and builds
// one snapshot from the two reads. The poll query stops at the sub-issue. The
// second query reads the pull requests of the sub-issues that a rule reads
// them for; no such sub-issue means no second query (poll.md, the topic on
// the two queries). The rate limit of the result is the one of both reads.
func (s *Service) readSnapshot(ctx context.Context, token, owner, repo string) (github.RepositorySnapshot, Snapshot, error) {
	read, err := s.GitHub.ReadSnapshot(ctx, token, owner, repo)
	if err != nil {
		return github.RepositorySnapshot{}, Snapshot{}, err
	}
	snapshot := toSnapshot(read)
	selected := snapshot.SubIssuesWithPullRequestRules()
	if len(selected) == 0 {
		return read, snapshot, nil
	}
	ids := make([]string, 0, len(selected))
	for _, sub := range selected {
		ids = append(ids, sub.NodeID)
	}
	pullRequests, err := s.GitHub.ReadPullRequests(ctx, token, owner, repo, ids)
	if err != nil {
		return github.RepositorySnapshot{}, Snapshot{}, err
	}
	byIssue := map[int][]PullRequest{}
	for number, list := range pullRequests.PullRequests {
		byIssue[number] = toPullRequests(list)
	}
	read.RateLimit.Cost += pullRequests.RateLimit.Cost
	read.RateLimit.Remaining = pullRequests.RateLimit.Remaining
	return read, snapshot.WithPullRequests(byIssue), nil
}

func (s *Service) pollRepository(ctx context.Context, target Target, finishing bool) (pollResult, error) {
	var result pollResult
	err := s.pollRepositoryInto(ctx, target, finishing, &result)
	return result, err
}

func (s *Service) pollRepositoryInto(ctx context.Context, target Target, finishing bool, result *pollResult) error {
	owner, repo := target.Repository.Owner, target.Repository.Name
	token, err := target.Token(ctx)
	if err != nil {
		return err
	}
	read, snapshot, err := s.readSnapshot(ctx, token, owner, repo)
	if err != nil {
		return err
	}
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
	s.ensurePriorityLabels(ctx, log, token, target, settings, readAgain)
	// The required checks are a REST call of their own, so the poll makes
	// it only when an issue of this repository waits for the checks (I3,
	// I4) or has an approval of a person to check (I12). Its budget is not
	// the one of the snapshot query.
	var required []RequiredCheck
	if snapshot.HasIssueChecking() || snapshot.HasOwnerApprovalCandidate() {
		read, err := s.GitHub.RequiredChecks(ctx, token, owner, repo, snapshot.DefaultBranch)
		if err != nil {
			return err
		}
		required = toRequiredChecks(read)
	}
	log.Info("poll",
		"requirement_issues", len(snapshot.RequirementIssues),
		"required_checks", len(required),
		"issues_with_pull_requests_read", len(snapshot.SubIssuesWithPullRequestRules()),
		"settings", settingsSource(settings.FromRepository), "risk_criteria", settings.RiskCriteriaSource,
		"rate_limit_cost", read.RateLimit.Cost, "rate_limit_remaining", read.RateLimit.Remaining)

	// The running set comes before the comments: a Planner that writes its
	// acceptance check comment and ends between the two reads then still
	// counts as running, and R4 waits one poll instead of asking twice.
	snapshot.Running = s.runningIssues(target.Repository.String())
	s.readLabelTimes(ctx, log, token, target, &snapshot)
	if !finishing {
		s.readReadyOwners(ctx, log, token, target, settings, &snapshot)
	}
	s.readAcceptanceComments(ctx, log, token, target, &snapshot)
	s.writeFollowUpNotes(ctx, log, token, target, &snapshot)
	s.cleanUp(ctx, log, target, snapshot)
	var errs []error
	// A requirement issue that R3 could not move keeps its sub-issues
	// waiting in this poll. A claim would take cumin/status/ready away from
	// the sub-issue, and R3 would then never apply again: the requirement
	// issue would stay in awaiting-plan-review while its work goes on.
	notStarted := map[int]bool{}
	// An issue that cumin moves on without the Owner keeps Q4 silent, even
	// when this poll decides nothing for it.
	result.movesOn = snapshot.MovesWithoutOwner()
	result.issueInWork = snapshot.HasIssueInWork()
	actions := Decide(snapshot, s.Settings.MaxIssuesInProgress, required, settings.Settings.PriorityLabelNames(),
		s.now(), settings.Settings.ChecksWaitTime)
	if finishing {
		// The work that is held back waits under its label for the next
		// start of cumin.
		kept := WithoutNewWork(actions)
		if held := len(actions) - len(kept); held > 0 {
			log.Info("stop after the current runs: new work is held back", "actions", held)
		}
		actions = kept
	}
	// An issue that I12 or I13 took at this poll gets no conflict resolution
	// of I14: the review of the Owner on the conflicting head decides
	// first. A check of I12 or of I13 that failed keeps the issue too, so
	// that the next poll decides it again.
	ownerDecided := map[int]bool{}
	for _, action := range actions {
		if a, ok := action.(ResolveConflict); ok && ownerDecided[a.Number] {
			log.Info("I14: waits for the review of the Owner on the conflicting head", "issue", a.Number)
			continue
		}
		// A candidate of I12 or of I13 is only a check; it counts as
		// progress for Q4 when it merges, stops, or sends back the issue.
		switch action.(type) {
		case MergeOwnerApproval, FixOwnerReview:
		default:
			result.note(action)
		}
		switch a := action.(type) {
		case StartRequirement:
			if err := s.startRequirement(ctx, token, target, snapshot, a); err != nil {
				notStarted[a.Number] = true
				errs = append(errs, err)
			}
		case ReviewRemaining:
			if err := s.reviewRemaining(ctx, token, target, snapshot, settings, a); err != nil {
				errs = append(errs, err)
			}
		case Accept:
			if err := s.accept(ctx, token, target, snapshot, settings, a); err != nil {
				errs = append(errs, err)
			}
		case CheckAcceptance:
			if err := s.checkAcceptance(ctx, token, target, snapshot, settings, a); err != nil {
				errs = append(errs, err)
			}
		case StopAcceptance:
			if err := s.stopAcceptance(ctx, token, target, snapshot, settings, a); err != nil {
				errs = append(errs, err)
			}
		case Plan:
			if err := s.plan(ctx, token, target, snapshot, settings, a); err != nil {
				errs = append(errs, err)
			}
		case ReviewPlan:
			if err := s.reviewPlan(ctx, token, target, snapshot, settings, a); err != nil {
				errs = append(errs, err)
			}
		case StopSplit:
			if err := s.stopSplit(ctx, token, target, snapshot, settings, a); err != nil {
				errs = append(errs, err)
			}
		case Claim:
			if notStarted[a.RequirementIssue] {
				log.Info("I1: waits for R3 of the requirement issue", "issue", a.Number, "requirement_issue", a.RequirementIssue)
				continue
			}
			if err := s.claim(ctx, token, target, snapshot, settings, a); err != nil {
				errs = append(errs, err)
			}
		case StartReview:
			if err := s.startReview(ctx, token, target, snapshot, settings, a); err != nil {
				errs = append(errs, err)
			}
		case FixChecks:
			if err := s.fixChecks(ctx, token, target, snapshot, settings, a); err != nil {
				errs = append(errs, err)
			}
		case ResolveConflict:
			if err := s.resolveConflictAtPoll(ctx, token, target, snapshot, settings, a); err != nil {
				errs = append(errs, err)
			}
		case StopForUnreportedChecks:
			if err := s.stopForUnreportedChecks(ctx, token, target, snapshot, settings, a); err != nil {
				errs = append(errs, err)
			}
		case CopyLabels:
			if err := s.copyLabels(ctx, token, target, a); err != nil {
				errs = append(errs, err)
			}
		case MergeOwnerApproval:
			acted, err := s.mergeOwnerApproval(ctx, token, target, snapshot, settings, required, a)
			if err != nil {
				errs = append(errs, err)
			}
			ownerDecided[a.Number] = acted || err != nil
			if acted {
				result.note(action)
			}
		case FixOwnerReview:
			acted, err := s.fixOwnerReview(ctx, token, target, snapshot, settings, a)
			if err != nil {
				errs = append(errs, err)
			}
			ownerDecided[a.Number] = ownerDecided[a.Number] || acted || err != nil
			if acted {
				result.note(action)
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
	// Q1: the quota decides before anything changes, the state included.
	if ok, err := s.quotaAllowsStart(ctx, "I1", config.RoleImplementer, target, c.Number); err != nil || !ok {
		if err != nil {
			return fmt.Errorf("I1: issue #%d: %w", c.Number, err)
		}
		return nil
	}
	// The Owner added cumin/status/ready, so the work starts again from a
	// new session and a count of zero (issue-states.md, the section on the
	// sessions of an agent). This comes before the label change: a state
	// that cumin cannot clear would resume the old session of a request in
	// the same session (I4) after a restart, which the Owner's intervention
	// must end. The issue keeps cumin/status/ready, so the next poll
	// claims it again.
	if err := s.State.Clear(target.Repository.String(), c.Number); err != nil {
		return fmt.Errorf("I1: clear the state of issue #%d: %w", c.Number, err)
	}
	labels := LabelsAfterClaim(sub.Labels)
	if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, c.Number, labels); err != nil {
		return fmt.Errorf("I1: claim issue #%d: %w", c.Number, err)
	}
	log := s.logger().With("repository", target.Repository.String(), "issue", c.Number)
	log.Info("I1: claimed the issue",
		"requirement_issue", c.RequirementIssue, "labels", labels)
	// The poll read the Owner of the newest cumin/status/ready before the
	// decision (readReadyOwners); I1 holds only with that Owner.
	if err := s.startImplementer(ctx, target, settings, sub, sub.ReadyOwner); err != nil {
		return fmt.Errorf("I1: request the work for issue #%d: %w", c.Number, err)
	}
	return nil
}

// copyLabels applies I11: the pull request gets the cumin/status/* and
// risk/* labels of the issue that it closes. A pull request is an issue on
// the labels endpoint, so the call is the one for an issue.
func (s *Service) copyLabels(ctx context.Context, token string, target Target, a CopyLabels) error {
	owner, repo := target.Repository.Owner, target.Repository.Name
	if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, a.PullRequest, a.Labels); err != nil {
		return fmt.Errorf("I11: copy the labels of issue #%d to pull request #%d: %w", a.Issue, a.PullRequest, err)
	}
	s.logger().Info("I11: copied the labels of the issue to the pull request",
		"repository", target.Repository.String(), "issue", a.Issue,
		"pull_request", a.PullRequest, "labels", a.Labels)
	return nil
}

// implementerRequest is one request to the Implementer: its row, its kind,
// the branch of its worktree, the session that it resumes, and its text.
type implementerRequest struct {
	// row starts the log lines of the request: I1, I4, I13, or I14.
	row string
	// kind is the request kind of implementer.md, for the log.
	kind   string
	branch string
	// pullRequest is the open pull request whose work the request
	// continues, or 0 for a first request.
	pullRequest int
	// sessionID resumes that session. Empty starts a new session.
	sessionID string
	// ownerLogin is the login of the Owner for the facts of the request,
	// read before the label changed. Empty says that there is none.
	ownerLogin string
	// text builds the request text once the work directory is known.
	text func(workDir string) string
	// conflictHead is the head commit that conflicted with the default
	// branch, for a conflict resolution (I6, I12, I14); empty otherwise. After done,
	// a head that is still this commit stops the issue instead of I2, so
	// that the same conflict does not go round the review again.
	conflictHead string
}

// stopForUnreportedChecks applies I15: a required check has not reported on
// the head commit of the pull request within the wait time of the
// repository, or no open pull request closes the issue. The issue goes to
// the Owner through the stop step with the row I15. No agent starts: cumin
// writes what it sees, and the Owner finds the cause.
//
// The label changes first: until it changes, the next poll decides the same
// stop, and must not post the comment and notify again (principle 3).
func (s *Service) stopForUnreportedChecks(ctx context.Context, token string, target Target, snapshot Snapshot, settings *RepositorySettings, a StopForUnreportedChecks) error {
	owner, repo := target.Repository.Owner, target.Repository.Name
	log := s.logger().With("repository", target.Repository.String(), "issue", a.Number, "pull_request", a.PullRequest)
	sub, ok := snapshot.SubIssue(a.Number)
	if !ok {
		return fmt.Errorf("I15: issue #%d is not in the snapshot", a.Number)
	}
	if a.PullRequest == 0 {
		log.Warn("I15: no open pull request closes the issue after the wait time", "waited", a.Waited.String())
	} else {
		names := make([]string, 0, len(a.Unreported))
		for _, check := range a.Unreported {
			names = append(names, check.Name)
		}
		log.Warn("I15: the required checks did not report within the wait time",
			"head_commit", a.HeadCommit, "unreported", names, "waited", a.Waited.String())
	}
	labels := ReplaceStatusLabel(sub.Labels, LabelAwaitingDecision)
	if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, a.Number, labels); err != nil {
		return fmt.Errorf("I15: stop issue #%d for the Owner: %w", a.Number, err)
	}
	log.Info("I15: the issue waits for the Owner", "labels", labels)
	reason := UnreportedChecksReason(a)
	s.stopForOwner(ctx, log, target, settings, stop{
		row:       RowI15,
		issue:     a.Number,
		labels:    labels,
		reason:    reason,
		comment:   StopNote(RowI15, reason, a.PullRequest, false),
		labelDone: true,
	})
	return nil
}

// fixChecks applies I4: a required check failed on the head commit of the
// pull request. Below the limit of the repository, the count grows by one,
// the status label becomes cumin/status/implementing, and the Implementer
// fixes the checks in the session of its last run, on the branch of the
// pull request. At the limit, the issue goes to the Owner through the stop
// step with the row I4.
//
// The count is saved before the label changes: a count that cumin cannot
// keep would let the requests run past the limit, so the label stays and
// the next poll tries again. The label changes before the request, so that
// a later poll never requests the same fix twice (principle 3).
func (s *Service) fixChecks(ctx context.Context, token string, target Target, snapshot Snapshot, settings *RepositorySettings, a FixChecks) error {
	owner, repo := target.Repository.Owner, target.Repository.Name
	repository := target.Repository.String()
	log := s.logger().With("repository", repository, "issue", a.Number, "pull_request", a.PullRequest)
	sub, ok := snapshot.SubIssue(a.Number)
	if !ok {
		return fmt.Errorf("I4: issue #%d is not in the snapshot", a.Number)
	}
	pr, ok := sub.LatestPullRequest()
	if !ok {
		return fmt.Errorf("I4: issue #%d has no open pull request", a.Number)
	}
	names := make([]string, 0, len(a.Failed))
	for _, check := range a.Failed {
		names = append(names, check.Name)
	}

	stored := s.State.Issue(repository, a.Number)
	limit := settings.Settings.MaxCheckFixRequests
	if !CheckFixAllowed(stored.CheckFixRequests, limit) {
		reason := fmt.Sprintf("A required check failed again (%s) after %d check fix requests, the limit of this repository (max_check_fix_requests).",
			strings.Join(names, ", "), stored.CheckFixRequests)
		log.Warn("I4: the limit of check fix requests is reached", "failed", names, "check_fix_requests", stored.CheckFixRequests)
		// The label first: until it changes, the next poll decides the same
		// stop, and must not post the comment and notify again.
		labels := ReplaceStatusLabel(sub.Labels, LabelAwaitingDecision)
		if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, a.Number, labels); err != nil {
			return fmt.Errorf("I4: stop issue #%d for the Owner: %w", a.Number, err)
		}
		log.Info("I4: the issue waits for the Owner", "labels", labels)
		s.stopForOwner(ctx, log, target, settings, stop{
			row:       RowI4,
			issue:     a.Number,
			labels:    labels,
			reason:    reason,
			comment:   StopNote(RowI4, reason, pr.Number, false),
			labelDone: true,
		})
		return nil
	}

	ownerLogin, err := s.readOwnerLogin(ctx, token, target, a.Number)
	if err != nil {
		return fmt.Errorf("I4: read the login of the Owner of issue #%d: %w", a.Number, err)
	}
	counted := stored
	counted.CheckFixRequests++
	if err := s.State.Set(repository, a.Number, counted); err != nil {
		return fmt.Errorf("I4: keep the count of check fix requests of issue #%d: %w", a.Number, err)
	}
	labels := LabelsAfterCheckFix(sub.Labels)
	if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, a.Number, labels); err != nil {
		// No request starts, so the count goes back: a label that fails
		// again must not use up the limit without a single fix.
		if undo := s.State.Set(repository, a.Number, stored); undo != nil {
			log.Error("I4: the count of check fix requests was not set back", "error", undo.Error())
		}
		return fmt.Errorf("I4: move issue #%d back to the Implementer: %w", a.Number, err)
	}
	stored = counted
	log.Info("I4: a required check failed; the issue goes back to the Implementer",
		"failed", names, "check_fix_requests", stored.CheckFixRequests, "labels", labels)

	failed := make([]github.RequiredCheck, 0, len(a.Failed))
	for _, check := range a.Failed {
		failed = append(failed, github.RequiredCheck{Name: check.Name, Integration: check.Integration})
	}
	contents := s.GitHub.FailedCheckContent(ctx, token, owner, repo, pr.HeadCommit, failed, log)
	texts := make([]string, 0, len(contents))
	for _, content := range contents {
		texts = append(texts, content.Content)
	}
	branch := pr.HeadBranch
	if branch == "" {
		branch = BranchName(sub.Number, sub.Title)
	}
	return s.goImplementer(ctx, target, settings, a.Number, implementerRequest{
		row: "I4", kind: "check fix", branch: branch, pullRequest: pr.Number, sessionID: stored.SessionID,
		ownerLogin: ownerLogin,
		text: func(workDir string) string {
			return CheckFixRequestText(repository, a.Number, pr.Number, branch, workDir, texts)
		},
	})
}

// startImplementer requests the work of I1 from the Implementer. The
// request kind is "implement", or "continue" when an open pull request
// already closes the issue; the work then goes on on the branch of that
// pull request (ClaimBranch). The session is new in both cases
// (issue-states.md, the section on the sessions of an agent).
func (s *Service) startImplementer(ctx context.Context, target Target, settings *RepositorySettings, sub SubIssue, ownerLogin string) error {
	branch, pullRequest := ClaimBranch(sub)
	repository := target.Repository.String()
	req := implementerRequest{
		row: "I1", kind: "implement", branch: branch, ownerLogin: ownerLogin,
		text: func(workDir string) string {
			return ImplementRequestText(repository, sub.Number, branch, workDir)
		},
	}
	if pullRequest != 0 {
		req.kind, req.pullRequest = "continue", pullRequest
		req.text = func(workDir string) string {
			return ContinueRequestText(repository, sub.Number, pullRequest, branch, workDir)
		}
	}
	return s.goImplementer(ctx, target, settings, sub.Number, req)
}

// goImplementer runs one Implementer request in its own goroutine, so that
// the poll goes on while the agent works. What the goroutine does (the
// worktree, the start, the end of the run) is only logged and handled by
// the end of the run (I2).
func (s *Service) goImplementer(ctx context.Context, target Target, settings *RepositorySettings, number int, req implementerRequest) error {
	if s.Agents == nil {
		return errors.New("no agent service is configured")
	}
	done := s.markInProgress(ctx, target.Repository.String(), number)
	s.running.Add(1)
	go func() {
		defer s.running.Done()
		defer done()
		s.runImplementer(ctx, target, settings, number, req)
	}()
	return nil
}

// agentAttempts is how many times cumin starts one request: the first run,
// and one more after an abnormal end (issue-states.md, the section on
// abnormal ends). The count lives here, in the run, so a new claim (I1)
// always starts at zero.
const agentAttempts = 2

// readOwnerLogin reads the login of the Owner for the facts of a start
// request (docs/ja/requirements/agents/common.md, the facts of the start
// request): the account that added the newest cumin/status/ready to the
// issue of the run, or to a sub-issue when a requirement issue has no such
// event. The login is passed only when that account is the Owner (IsOwner).
// The empty login says that there is no Owner login. A failed read is an
// error: the caller reads before it changes the label, changes nothing, and
// sends no request, so the next poll tries again.
func (s *Service) readOwnerLogin(ctx context.Context, token string, target Target, number int) (string, error) {
	actor, isOwner, err := s.readReadyActor(ctx, token, target, number, true)
	if err != nil || !isOwner {
		return "", err
	}
	return actor.Login, nil
}

// readReadyActor reads the account that added the newest cumin/status/ready
// to the issue, and whether that account is the Owner (IsOwner). A GitHub
// App is never the Owner, so its permission is not read. subIssues lets an
// event of a sub-issue answer for a requirement issue with no such event;
// the check of R1 and of I1 passes false, so that only an event of the
// issue itself can start work.
func (s *Service) readReadyActor(ctx context.Context, token string, target Target, number int, subIssues bool) (github.LabelActor, bool, error) {
	owner, repo := target.Repository.Owner, target.Repository.Name
	read := s.GitHub.ReadOwnLabelActor
	if subIssues {
		read = s.GitHub.ReadLabelActor
	}
	actor, rate, err := read(ctx, token, owner, repo, number, LabelReady)
	if err != nil {
		return github.LabelActor{}, false, err
	}
	s.logger().Debug("read the actor of the newest "+LabelReady, "repository", target.Repository.String(), "issue", number,
		"actor", actor.Login, "actor_type", actor.Type, "rate_limit_cost", rate.Cost, "rate_limit_remaining", rate.Remaining)
	if actor.Login == "" || actor.Type != "User" {
		return actor, false, nil
	}
	permission, userType, err := s.GitHub.RepositoryPermission(ctx, token, owner, repo, actor.Login)
	if err != nil {
		return github.LabelActor{}, false, err
	}
	return actor, IsOwner(permission, userType), nil
}

// runImplementer prepares the worktree and runs one Implementer request to
// its end. The end of the run is the trigger of I2, whatever the row of the
// request: a done result goes to verifyDone, and a blocked result stops the
// issue for the Owner.
//
// An abnormal end starts the same request once more, in the same work
// directory and in a new session (agent-run.md, the topic on the retry): a
// session that ended abnormally is not resumed again. After the second one,
// the issue goes back to the Owner with the kind of the end. While cumin is
// stopping, the context ends the run as an abnormal end as well; nothing is
// retried then, and no label changes, because the Owner restarts the issue
// (cumin-core.md, the topic on the stop).
func (s *Service) runImplementer(ctx context.Context, target Target, settings *RepositorySettings, number int, req implementerRequest) {
	log := s.logger().With("repository", target.Repository.String(), "issue", number, "role", config.RoleImplementer)
	role := settings.Settings.Roles[config.RoleImplementer]
	checkout := agent.Checkout{
		Owner:  target.Repository.Owner,
		Repo:   target.Repository.Name,
		Issue:  number,
		Role:   config.RoleImplementer,
		Branch: req.branch,
	}
	// A request on an open pull request (a continuation of I1, a check fix
	// of I4) starts from the pull request on GitHub. A worktree of an
	// earlier round can be on another branch, or behind commits that were
	// pushed since, and Prepare reuses a worktree as it is; so it goes, and
	// Prepare creates the worktree again from origin/<branch>. A worktree
	// that holds work that is not on GitHub stays: a run that cumin stopped
	// leaves its work there, and the Owner restarts the issue with
	// cumin/status/ready.
	if req.pullRequest != 0 {
		removed, err := s.Workspace.RemoveIfPushed(ctx, checkout)
		if err != nil {
			log.Error(req.row+": the worktree of an earlier round was not checked", "error", err.Error())
			return
		}
		if !removed {
			log.Warn(req.row+": the worktree of an earlier round holds work that is not on GitHub; it is used as it is", "branch", req.branch)
		}
	}
	workDir, err := s.Workspace.Prepare(ctx, target.RemoteURL, checkout)
	if err != nil {
		log.Error(req.row+": the work directory was not prepared", "error", err.Error())
		return
	}
	log.Info(req.row+": requested the work", "kind", req.kind, "branch", req.branch,
		"pull_request", req.pullRequest, "resumed", req.sessionID != "")
	request := agent.StartRequest{
		Owner:        target.Repository.Owner,
		Repo:         target.Repository.Name,
		Role:         config.RoleImplementer,
		RiskCriteria: settings.RiskCriteria,
		Facts:        agent.Facts{IssueNumber: number, IssueKind: agent.IssueKindImplementation, OwnerLogin: req.ownerLogin, ProtectedPaths: settings.ProtectedPaths},
		Text:         req.text(workDir),
		WorkDir:      workDir,
		Settings:     &role,
		SessionID:    req.sessionID,
	}

	var firstKind agent.EndKind
	for attempt := 1; attempt <= agentAttempts; attempt++ {
		run, err := s.Agents.Start(ctx, request)
		var abnormal *agent.AbnormalEnd
		switch {
		case errors.As(err, &abnormal):
			log.Info("the agent run ended abnormally", "kind", abnormal.Kind.String(),
				"session_id", abnormal.SessionID, "detail", abnormal.Detail, "attempt", attempt)
			if ctx.Err() != nil {
				// cumin is stopping. The label stays, and the Owner
				// restarts the issue with cumin/status/ready.
				return
			}
			if attempt < agentAttempts {
				firstKind = abnormal.Kind
				request.SessionID = ""
				log.Info("I2: the same request runs again in the same work directory", "attempt", attempt+1)
				continue
			}
			s.stopAfterAbnormalEnd(ctx, log, target, settings, RowI2, "Implementer", number, firstKind, abnormal.Kind)
			return
		case err != nil:
			log.Error("the agent was not started", "error", err.Error())
			return
		default:
			log.Info("the agent run ended", "result", run.Result.Result, "session_id", run.SessionID)
			s.quotaAfterRun(ctx, log, target, number, run)
			s.keepSession(log, target, config.RoleImplementer, number, run.SessionID)
			if run.Result.Result != agent.ResultDone {
				s.stopAfterBlocked(ctx, log, target, settings, RowI2, "Implementer", number, run.Result.BlockedReason)
				return
			}
			// The first try runs here. A try of the kept step runs in a poll.
			kept := false
			step := &keptStep{name: "verify done", log: log}
			step.run = func(ctx context.Context) error {
				again := kept
				kept = true
				return s.verifyDone(ctx, log, target, settings, number, req.branch, workDir, run.BotLogin, req.conflictHead, req.row, again)
			}
			if err := step.run(ctx); err != nil && ctx.Err() == nil {
				s.keepStep(inProgressKey{repository: target.Repository.String(), issue: number}, step, err)
			}
			return
		}
	}
}

// stopAfterAbnormalEnd stops the issue after the second abnormal end of the
// same request, with the row of the request (I2 for the Implementer, I3 for
// the Reviewer): the comment names both kinds and says that cumin ran the
// request again, and the issue goes to the Owner. The two runs can end in
// different ways, and the Owner needs the kind of each one to know where
// to look.
func (s *Service) stopAfterAbnormalEnd(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, row, role string, number int, first, second agent.EndKind) {
	reason := abnormalReason(role, first, second)
	sub, _ := s.subIssueNow(ctx, log, target, number)
	pullRequest := 0
	if pr, ok := sub.LatestPullRequest(); ok {
		pullRequest = pr.Number
	}
	s.stopForOwner(ctx, log, target, settings, stop{
		row:     row,
		issue:   number,
		labels:  sub.Labels,
		reason:  reason,
		comment: StopNote(row, reason, pullRequest, true),
	})
}

// stopAfterBlocked stops the issue for a blocked result, with the row of
// the result (I2 for the Implementer, I10 for the Reviewer): the
// blocked_reason of the agent becomes the comment, because the agent
// already wrote it in the form of templates/decision-request.md, and its
// first line is the question for the Owner. Nothing is retried: a blocked
// result usually means that a requirement is missing, so the Owner answers
// first (issue-states.md, the paragraph on a blocked result).
func (s *Service) stopAfterBlocked(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, row, role string, number int, reason string) {
	s.stopBlocked(ctx, log, target, settings, row, role, number, reason, labelsNow(s.subIssueNow(ctx, log, target, number)))
}

// stopBlocked is stopAfterBlocked with the labels of the issue that the
// caller read.
func (s *Service) stopBlocked(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, row, role string, number int, reason string, labels []string) {
	question := firstLine(reason)
	log.Warn(row+": the agent returned blocked", "reason", question)
	s.stopForOwner(ctx, log, target, settings, stop{
		row:     row,
		issue:   number,
		labels:  labels,
		reason:  "the " + role + " returned blocked: " + question,
		comment: reason,
	})
}

// labelsNow is the labels of the sub-issue that subIssueNow read, or none
// when it could not be read.
func labelsNow(sub SubIssue, ok bool) []string {
	if !ok {
		return nil
	}
	return sub.Labels
}

// verifyDone applies I2 after a done result. It reads the issue of the run
// again, and only that issue, because a rule that the end of a run triggers
// judges on the facts of that moment, not on those of the last poll
// (cumin-core.md, the topic on the GitHub client). It then lists the open pull requests of
// the branch of the run, reads the head commit of the worktree, and runs
// the pure check.
//
// On a pass, when the issue has no closing link to the pull request,
// cumin-core adds it and reads the issue once more to see it; then the
// status label becomes cumin/status/checking. A failed check, a
// failed link, and a link that is still missing hand the issue back to the
// Owner through the stop step, with one sentence. Nothing of that is
// retried: the Owner decides what to do next.
//
// A temporary failure of a call to GitHub (the token, a read, the label) is
// returned, and the caller keeps the step (keptstep.go). The step then runs
// again from its start, with again set: when the issue of the new read has
// left cumin/status/implementing, the label was already changed, and the
// step changes nothing. Every other failure is logged and returns nil, as
// before.
func (s *Service) verifyDone(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, number int, branch, workDir, botLogin, conflictHead, row string, again bool) error {
	owner, repo := target.Repository.Owner, target.Repository.Name
	token, err := target.Token(ctx)
	if err != nil {
		log.Error("I2: no token", "error", err.Error())
		return temporary(err)
	}
	read, err := s.GitHub.ReadSubIssue(ctx, token, owner, repo, number)
	if err != nil {
		log.Error("I2: the issue was not read again", "error", err.Error())
		return temporary(err)
	}
	log.Debug("read the issue again", "issue", number, "rate_limit_cost", read.RateLimit.Cost, "rate_limit_remaining", read.RateLimit.Remaining)
	sub := toSubIssue(read.Issue)
	if again && !slices.Contains(sub.Labels, LabelImplementing) {
		log.Info("I2: the issue left cumin/status/implementing while verify done was kept; nothing changes", "labels", sub.Labels)
		return nil
	}
	listed, err := s.GitHub.ListOpenPullRequestsOfBranch(ctx, token, owner, repo, branch)
	if err != nil {
		log.Error("I2: the open pull requests of the branch were not read", "branch", branch, "error", err.Error())
		return temporary(err)
	}
	onBranch := make([]PullRequest, 0, len(listed))
	nodeIDs := map[int]string{}
	for _, pr := range listed {
		onBranch = append(onBranch, PullRequest{Number: pr.Number, HeadCommit: pr.HeadCommit, HeadBranch: pr.HeadBranch, Author: pr.Author})
		nodeIDs[pr.Number] = pr.NodeID
	}
	head, err := s.Workspace.Head(ctx, workDir)
	if err != nil {
		log.Error("I2: the head commit of the work directory was not read", "error", err.Error())
		return nil
	}
	verification := VerifyDone(sub, branch, onBranch, botLogin, head, github.MaxOpenClosingPullRequests)
	stopI2 := func(reason string, pullRequest int) {
		s.stopForOwner(ctx, log, target, settings, stop{
			row:     RowI2,
			issue:   number,
			labels:  sub.Labels,
			reason:  reason,
			comment: StopNote(RowI2, reason, pullRequest, false),
		})
	}
	if !verification.Passed {
		log.Warn("I2: the verification failed", "failure", verification.Failure.String(),
			"branch", branch, "pull_request", verification.PullRequest)
		stopI2(VerificationReason(verification.Failure), verification.PullRequest)
		return nil
	}
	if conflictHead != "" && head == conflictHead {
		// The pull request passed, so its head is the head of the worktree.
		// row is the row that found the conflict: I6, I12, or I14.
		log.Warn(row+": the head did not change after the conflict resolution", "pull_request", verification.PullRequest)
		s.stopForOwner(ctx, log, target, settings, stop{
			row: row, issue: number, labels: sub.Labels, reason: ConflictNotResolvedReason(verification.PullRequest),
			comment: StopNote(row, ConflictNotResolvedReason(verification.PullRequest), verification.PullRequest, false),
		})
		return nil
	}
	if verification.AddLink {
		pr := verification.PullRequest
		if err := s.GitHub.AddClosingLink(ctx, token, sub.NodeID, nodeIDs[pr]); err != nil {
			log.Warn("I2: the closing link was not added", "pull_request", pr, "error", err.Error())
			stopI2(LinkFailedReason(pr, githubAnswer(err)), pr)
			return nil
		}
		again, err := s.GitHub.ReadSubIssue(ctx, token, owner, repo, number)
		if err != nil {
			log.Error("I2: the issue was not read after the closing link", "error", err.Error())
			return temporary(err)
		}
		log.Debug("read the issue again", "issue", number, "rate_limit_cost", again.RateLimit.Cost, "rate_limit_remaining", again.RateLimit.Remaining)
		sub = toSubIssue(again.Issue)
		if !linksPullRequest(sub, pr) {
			log.Warn("I2: the closing link is missing after cumin-core added it", "pull_request", pr)
			stopI2(LinkMissingReason(pr), pr)
			return nil
		}
		log.Info("I2: added the closing link", "pull_request", pr)
	}
	labels := ReplaceStatusLabel(sub.Labels, LabelChecking)
	if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, number, labels); err != nil {
		log.Error("I2: the label was not changed", "error", err.Error())
		return temporary(err)
	}
	log.Info("I2: verified the pull request", "pull_request", verification.PullRequest, "labels", labels)
	return nil
}

// temporary returns err when it is a temporary failure of a call to GitHub,
// and nil otherwise: only a temporary failure keeps a step.
func temporary(err error) error {
	if github.IsTemporary(err) {
		return err
	}
	return nil
}

// githubAnswer is the answer of GitHub in an error of AddClosingLink: the
// request, the status, and the message of GitHub, or the messages of a
// GraphQL answer.
func githubAnswer(err error) string {
	return strings.TrimPrefix(err.Error(), "github: add the closing link: ")
}

// keepSession stores the session of the run of the role, so that a request
// in the same session can resume it (I4, I5, and the later rounds of I3).
// The Implementer and the Reviewer each keep their own. A failure is logged
// and changes nothing else: the next request then starts a new session.
func (s *Service) keepSession(log *slog.Logger, target Target, role config.Role, number int, sessionID string) {
	if sessionID == "" {
		return
	}
	repository := target.Repository.String()
	issue := s.State.Issue(repository, number)
	if role == config.RoleReviewer {
		issue.ReviewerSessionID = sessionID
	} else {
		issue.SessionID = sessionID
	}
	if err := s.State.Set(repository, number, issue); err != nil {
		log.Error("the session of the run was not kept", "error", err.Error())
	}
}

// firstLine is the first line of s, for one log field. The first line of a
// blocked_reason is the question that the Owner must answer.
func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

// toRequiredChecks converts the required checks that the REST call read.
// The types of the platform package stop here.
func toRequiredChecks(read []github.RequiredCheck) []RequiredCheck {
	checks := make([]RequiredCheck, 0, len(read))
	for _, check := range read {
		checks = append(checks, RequiredCheck{Name: check.Name, Integration: check.Integration})
	}
	return checks
}

// toChecks converts the checks of one pull request.
func toChecks(read []github.CheckResult) []CheckResult {
	checks := make([]CheckResult, 0, len(read))
	for _, check := range read {
		checks = append(checks, CheckResult{
			Name:        check.Name,
			Conclusion:  toConclusion(check.Conclusion),
			Integration: check.Integration,
		})
	}
	return checks
}

// toConclusion folds the conclusion of the client into the one of the rules.
func toReviews(read []github.Review) []Review {
	var reviews []Review
	for _, r := range read {
		reviews = append(reviews, Review{Author: r.Author, State: ReviewState(r.State), Commit: r.Commit, SubmittedAt: r.SubmittedAt, URL: r.URL})
	}
	return reviews
}

func toConclusion(c github.CheckConclusion) CheckConclusion {
	switch c {
	case github.CheckPassed:
		return CheckPassed
	case github.CheckFailed:
		return CheckFailed
	}
	return CheckPending
}

// toSnapshot converts what the GitHub client read to the snapshot of the
// rules. The types of the platform package stop here.
func toSnapshot(read github.RepositorySnapshot) Snapshot {
	snapshot := Snapshot{DefaultBranch: read.DefaultBranch}
	for _, issue := range read.RequirementIssues {
		snapshot.RequirementIssues = append(snapshot.RequirementIssues, toRequirementIssue(issue))
	}
	return snapshot
}

// toRequirementIssue converts one requirement issue of the GitHub client,
// from the poll or from the read of one issue.
func toRequirementIssue(issue github.Issue) RequirementIssue {
	requirement := RequirementIssue{Number: issue.Number, Labels: issue.Labels}
	for _, blocker := range issue.BlockedBy {
		requirement.BlockedBy = append(requirement.BlockedBy, BlockedBy{Number: blocker.Number, Closed: blocker.Closed})
	}
	for _, sub := range issue.SubIssues {
		requirement.SubIssues = append(requirement.SubIssues, toSubIssue(sub))
	}
	return requirement
}

// toSubIssue converts one sub-issue of the GitHub client, from the poll or
// from the read of one issue.
func toSubIssue(sub github.Issue) SubIssue {
	subIssue := SubIssue{Number: sub.Number, NodeID: sub.NodeID, Title: sub.Title, Closed: sub.Closed, ClosedAt: sub.ClosedAt, Labels: sub.Labels}
	for _, blocker := range sub.BlockedBy {
		subIssue.BlockedBy = append(subIssue.BlockedBy, BlockedBy{Number: blocker.Number, Closed: blocker.Closed})
	}
	subIssue.PullRequests = toPullRequests(sub.PullRequests)
	return subIssue
}

// toPullRequests converts the pull requests of one sub-issue of the GitHub
// client, from the second query of the poll or from the read of one issue.
func toPullRequests(read []github.PullRequest) []PullRequest {
	var pullRequests []PullRequest
	for _, pr := range read {
		pullRequests = append(pullRequests, PullRequest{
			Number:     pr.Number,
			HeadCommit: pr.HeadCommit,
			HeadBranch: pr.HeadBranch,
			Author:     pr.Author,
			Labels:     pr.Labels,
			Checks:     toChecks(pr.Checks),
			Reviews:    toReviews(pr.Reviews),
			// The three values of GitHub pass as they are; the
			// client refuses any other value.
			Mergeable:       MergeableState(pr.Mergeable),
			HeadCommittedAt: pr.HeadCommittedAt,
		})
	}
	return pullRequests
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
