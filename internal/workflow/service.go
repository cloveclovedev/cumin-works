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
	// MergeWait is how long cumin waits between two merges of one poll of
	// a repository. Zero means DefaultMergeWait. Tests shorten it.
	MergeWait time.Duration
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
	// readyTold holds, for each issue, the time of the status label event
	// of an account that does not count (another account than the Owner
	// for cumin/status/ready; than cumin-core or an Owner for the others)
	// that cumin already logged and told the Owner about. cumin can lose
	// it: after a restart it tells the Owner once more.
	readyMu      sync.Mutex
	readyTold    map[string]time.Time
	pollFailures map[string]*repeatedFailure

	// quota keeps which notifications of "stop agent starts" the Owner already got
	// (quota.go). The polls and the ends of the runs share it.
	quotaMu sync.Mutex
	quota   quotaNotices
	// waitingTold says that the Owner heard Q4 (waiting) since cumin last
	// did something (waiting.go). quotaMu guards it.
	waitingTold bool
	// quotaUnread says that the read of the usage failed in this poll
	// (quota.go). quotaMu guards it.
	quotaUnread bool
	// quotaWaits holds the repositories where a start of an agent waits
	// only for the quota (waiting.go). quotaMu guards it.
	quotaWaits map[string]bool

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
	s.started++
	return func() { s.endRun(ctx, key) }
}

// endRun removes an issue from the set of issues in work when its run ends,
// and wakes Run. See markInProgress for the case that leaves the entry.
func (s *Service) endRun(ctx context.Context, key inProgressKey) {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	if ctx.Err() != nil {
		return
	}
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
	// A failed read of the quota usage is kept for one poll only: this
	// poll reads again.
	s.forgetQuotaUnread()
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
		all.waitsForQuota = s.startWaitsForQuota()
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
	// The running set comes before every read of GitHub. A run that ends
	// after the read of the snapshot has already decided its own end and
	// changed the issue. It then still counts as running for this poll, so
	// the poll does not decide the same end again from the old facts of
	// its snapshot: the next poll decides. A poll starts every run itself,
	// so no run is missing from a set that is read first.
	running := s.runningIssues(target.Repository.String())
	// This poll decides every start of the repository again, so a start
	// that waited only for the quota at an earlier poll is forgotten first.
	s.forgetQuotaWaits(target.Repository.String())
	token, err := target.Token(ctx)
	if err != nil {
		return err
	}
	read, snapshot, err := s.readSnapshot(ctx, token, owner, repo)
	if err != nil {
		return err
	}
	snapshot.Running = running
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

	s.readStatusActors(ctx, log, token, target, settings, &snapshot)
	s.readLabelTimes(ctx, log, token, target, &snapshot)
	if !finishing {
		s.readReadyOwners(ctx, log, token, target, settings, &snapshot)
	}
	s.readAcceptanceComments(ctx, log, token, target, &snapshot)
	s.readImplementingFacts(ctx, log, token, target, settings, &snapshot)
	s.readReviewingFacts(ctx, log, token, target, settings, &snapshot)
	s.readMergingFacts(ctx, log, token, target, settings, &snapshot)
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
	// An issue that I12 or I13 took at this poll gets no conflict resolution
	// of I14: the review of the Owner on the conflicting head decides
	// first. A check of I12 or of I13 that failed keeps the issue too, so
	// that the next poll decides it again. So does a request for changes of
	// the Owner that waits for the permit of its start.
	ownerDecided := map[int]bool{}
	// The merges that this poll sent, for the wait between two of them.
	merges := 0
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
		case WaitForChecks:
			sub, _ := snapshot.SubIssue(a.Number)
			if err := s.waitForChecks(ctx, log.With("issue", a.Number), token, target, settings, sub, a); err != nil {
				errs = append(errs, err)
			}
		case StopImplementation:
			sub, _ := snapshot.SubIssue(a.Number)
			if err := s.stopImplementation(ctx, log.With("issue", a.Number), token, target, settings, sub, a); err != nil {
				errs = append(errs, err)
			}
		case RequestImplementationAgain:
			sub, _ := snapshot.SubIssue(a.Number)
			if err := s.requestImplementationAgain(ctx, token, target, settings, sub, snapshot.DefaultBranch, a); err != nil {
				errs = append(errs, err)
			}
		case CloseMergedIssue, SendMerge, ResolveMergeConflict, LeaveMerge:
			if err := s.mergeEndAtPoll(ctx, log, token, target, snapshot, settings, a, &merges); err != nil {
				errs = append(errs, err)
			}
		case RequestReviewFix, AskOwnerToMerge, StartMerge, RequestCause, StopAtRoundLimit, BackToChecks, RequestReviewAgain, StopReview:
			if err := s.reviewEndAtPoll(ctx, log, token, target, snapshot, settings, a); err != nil {
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
			acted, waits, err := s.fixOwnerReview(ctx, token, target, snapshot, settings, a)
			if err != nil {
				errs = append(errs, err)
			}
			ownerDecided[a.Number] = ownerDecided[a.Number] || acted || waits || err != nil
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
	permit, ok := s.permitStart(ctx, s.logger().With("repository", target.Repository.String(), "issue", c.Number), "claim", config.RoleImplementer, target, c.Number)
	if !ok {
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
	if err := s.startImplementer(ctx, permit, target, settings, sub, sub.ReadyOwner); err != nil {
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
	// again says that the implementation was already requested again
	// during this stay in cumin/status/implementing.
	again bool
	// count says that the state file does not hold this second request
	// yet. runImplementer counts it before it prepares the work directory,
	// and takes the count back when the agent did not start.
	count bool
	// permit is the permit of the start of this request (permitStart).
	permit StartPermit
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
// the next poll tries again. The same write starts the new stay in
// cumin/status/implementing. The label changes before the request, so that
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

	// The stop at the limit above starts no agent, so it needs no permit.
	permit, ok := s.permitStart(ctx, log, "check fix", config.RoleImplementer, target, a.Number)
	if !ok {
		return nil
	}
	ownerLogin, err := s.readOwnerLogin(ctx, token, target, a.Number)
	if err != nil {
		return fmt.Errorf("I4: read the login of the Owner of issue #%d: %w", a.Number, err)
	}
	counted := stored
	counted.CheckFixRequests++
	counted.ImplementationRequests, counted.ConflictResolution = 0, false
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
		ownerLogin: ownerLogin, permit: permit,
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
func (s *Service) startImplementer(ctx context.Context, permit StartPermit, target Target, settings *RepositorySettings, sub SubIssue, ownerLogin string) error {
	branch, pullRequest := ClaimBranch(sub)
	repository := target.Repository.String()
	req := implementerRequest{
		row: "I1", kind: "implement", branch: branch, ownerLogin: ownerLogin, permit: permit,
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

// permitStart gets the permit of one start of an agent from the one check
// (PermitStart). Every request calls it before its label change and before
// its count. Without a permit the request does nothing more: the issue
// keeps its state, and a later poll decides the same step again. request
// names the request for the log, and role is the role whose agent would
// read the quota usage in a minimal run.
//
// While cumin stops after the current runs, no usage is read: the request
// waits for the next start of cumin in any case.
//
// A start that only the quota stops is noted for the waiting notification
// (noteQuotaWait), at a poll and at the end of a run alike.
func (s *Service) permitStart(ctx context.Context, log *slog.Logger, request string, role config.Role, target Target, number int) (StartPermit, bool) {
	stopsAfterRuns := s.finishing.Load()
	if stopsAfterRuns {
		log.Info("stop after the current runs: the request waits for the next start of cumin", "request", request)
	}
	quotaAllows := !stopsAfterRuns && s.quotaAllowsStart(ctx, log, request, role, target, number)
	if !stopsAfterRuns && !quotaAllows {
		s.noteQuotaWait(target.Repository.String())
	}
	return PermitStart(stopsAfterRuns, quotaAllows)
}

// startAgent starts an agent and waits for the end of its run. It is the
// only caller of Agents.Start in this package, and it takes the permit of
// the start, so no request reaches an agent without the one check.
func (s *Service) startAgent(ctx context.Context, permit StartPermit, request agent.StartRequest) (*agent.Run, error) {
	if !permit.granted {
		return nil, errors.New("the start of the agent has no permit")
	}
	return s.Agents.Start(ctx, request)
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

// startStay writes, in the state file, the start of a new stay of the
// implementation issue in cumin/status/implementing: no second request
// yet, and whether the request is a conflict resolution (I6, I12, I14). A
// resolution that leaves the head commit stops the issue, so that the same
// conflict does not go round the review again (ImplementationEnd).
//
// Every step that changes the label to cumin/status/implementing calls it
// before the label changes, as the claim clears the state file: a restart
// of cumin right after the label change then finds the new stay, not the
// count of the earlier one.
func (s *Service) startStay(repository string, number int, conflict bool) error {
	stored := s.State.Issue(repository, number)
	if stored.ImplementationRequests == 0 && stored.ConflictResolution == conflict {
		return nil
	}
	stored.ImplementationRequests, stored.ConflictResolution = 0, conflict
	return s.State.Set(repository, number, stored)
}

// countImplementationRequest changes, in the state file, how many times the
// implementation was requested again during this stay in
// cumin/status/implementing. delta is 1 before the second request starts,
// and -1 when that run did not start.
func (s *Service) countImplementationRequest(repository string, number, delta int) error {
	stored := s.State.Issue(repository, number)
	stored.ImplementationRequests = max(0, stored.ImplementationRequests+delta)
	return s.State.Set(repository, number, stored)
}

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
// to the issue, and whether that account is the Owner (IsOwner). subIssues
// lets an event of a sub-issue answer for a requirement issue with no such
// event; the check of R1 and of I1 passes false, so that only an event of
// the issue itself can start work.
func (s *Service) readReadyActor(ctx context.Context, token string, target Target, number int, subIssues bool) (github.LabelActor, bool, error) {
	return s.readStatusActor(ctx, token, target, number, LabelReady, subIssues)
}

// readStatusActor reads the account that added the newest status label to
// the issue, and whether the label counts as a state (StatusLabelCounts):
// the account is an Owner or, for every label but cumin/status/ready, the
// cumin-core App. The permission is read only for a person: a GitHub App is
// never the Owner. A failed read of the login of cumin-core is an error, so
// that the caller decides nothing.
func (s *Service) readStatusActor(ctx context.Context, token string, target Target, number int, label string, subIssues bool) (github.LabelActor, bool, error) {
	owner, repo := target.Repository.Owner, target.Repository.Name
	read := s.GitHub.ReadOwnLabelActor
	if subIssues {
		read = s.GitHub.ReadLabelActor
	}
	actor, rate, err := read(ctx, token, owner, repo, number, label)
	if err != nil {
		return github.LabelActor{}, false, err
	}
	s.logger().Debug("read the actor of the newest "+label, "repository", target.Repository.String(), "issue", number,
		"actor", actor.Login, "actor_type", actor.Type, "rate_limit_cost", rate.Cost, "rate_limit_remaining", rate.Remaining)
	status := StatusActor{Login: actor.Login, Type: actor.Type}
	core := ""
	switch {
	case actor.Login == "":
	case actor.Type == "User":
		status.Permission, status.UserType, err = s.GitHub.RepositoryPermission(ctx, token, owner, repo, actor.Login)
	case label != LabelReady && target.Login != nil:
		core, err = target.Login(ctx)
	}
	if err != nil {
		return github.LabelActor{}, false, err
	}
	return actor, StatusLabelCounts(label, status, core), nil
}

// runImplementer prepares the worktree and runs one Implementer request,
// and decides its end as the poll does: it reads the issue again with the
// facts of the way out of cumin/status/implementing, and ImplementationEnd
// decides from the facts on GitHub. So every end is the same case: a done
// result, an abnormal end, and a run that a restart of cumin cut off, which
// the next poll finds. Only a blocked result is not read from GitHub: cumin
// posts the blocked_reason and stops the issue for the Owner at once.
//
// When the facts ask for the second request, it runs here in the same work
// directory, in the kept session when the state file holds one. The quota
// decides before the request is counted, so a run that hit the quota limit
// does not use up the one second request. While cumin is stopping, nothing
// is requested and no label changes. A failed read changes nothing: the
// issue keeps cumin/status/implementing, and the next poll decides.
//
// A second request of a poll is counted before the work directory is
// prepared. A work directory that is not prepared sends no request: the
// next poll requests the implementation again, and the second failure stops
// the implementation for the Owner (stopForWorkDirectory).
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
	repository := target.Repository.String()
	if req.count {
		if err := s.countImplementationRequest(repository, number, 1); err != nil {
			log.Error("I2: the request was not counted; the next poll decides again", "error", err.Error())
			return
		}
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
			s.stopForWorkDirectory(ctx, log, target, settings, number, req)
			return
		}
		if !removed {
			log.Warn(req.row+": the worktree of an earlier round holds work that is not on GitHub; it is used as it is", "branch", req.branch)
		}
	}
	workDir, err := s.Workspace.Prepare(ctx, target.RemoteURL, checkout)
	if err != nil {
		log.Error(req.row+": the work directory was not prepared", "error", err.Error())
		s.stopForWorkDirectory(ctx, log, target, settings, number, req)
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

	again, counted, permit := req.again, req.count, req.permit
	for {
		run, err := s.startAgent(ctx, permit, request)
		var abnormal *agent.AbnormalEnd
		switch {
		case errors.As(err, &abnormal):
			log.Info("the agent run ended abnormally", "kind", abnormal.Kind.String(),
				"session_id", abnormal.SessionID, "detail", abnormal.Detail)
			if ctx.Err() != nil {
				// cumin is stopping. The label stays, and the next start
				// of cumin decides from the facts on GitHub.
				return
			}
		case err != nil:
			log.Error("the agent was not started", "error", err.Error())
			if counted {
				if err := s.countImplementationRequest(repository, number, -1); err != nil {
					log.Error("I2: the count of the request that did not start was not taken back", "error", err.Error())
				}
			}
			return
		default:
			log.Info("the agent run ended", "result", run.Result.Result, "session_id", run.SessionID)
			s.quotaAfterRun(ctx, log, target, number, run)
			s.keepSession(log, target, config.RoleImplementer, number, run.SessionID)
			if run.Result.Result != agent.ResultDone {
				s.stopAfterBlocked(ctx, log, target, settings, RowI2, "Implementer", number, run.Result.BlockedReason)
				return
			}
		}

		token, err := target.Token(ctx)
		if err != nil {
			log.Error("I2: no token; the next poll decides the end of the implementation", "error", err.Error())
			return
		}
		sub, ok := s.implementingNow(ctx, log, token, target, settings, number, req.branch)
		if !ok {
			return
		}
		sub.Implementing.RequestedAgain = sub.Implementing.RequestedAgain || again
		switch a := ImplementationEnd(sub, false).(type) {
		case WaitForChecks:
			if err := s.waitForChecks(ctx, log, token, target, settings, sub, a); err != nil {
				log.Error("the issue was not moved; the next poll decides again", "error", err.Error())
			}
			return
		case StopImplementation:
			// The Owner needs the kind of the end to know where to look.
			if abnormal != nil && !a.Question {
				a.Reason = AfterAbnormalEndReason(a.Reason, "Implementer", abnormal.Kind)
			}
			if err := s.stopImplementation(ctx, log, token, target, settings, sub, a); err != nil {
				log.Error("the issue was not moved; the next poll decides again", "error", err.Error())
			}
			return
		case RequestImplementationAgain:
			if permit, ok = s.permitStart(ctx, log, "implementation again", config.RoleImplementer, target, number); !ok {
				return
			}
			if err := s.countImplementationRequest(repository, number, 1); err != nil {
				log.Error("I2: the request was not counted; the next poll decides again", "error", err.Error())
				return
			}
			again, counted = true, true
			request.SessionID = s.State.Issue(repository, number).SessionID
			log.Info("I2: the pull request does not pass the check; the same request runs again in the same work directory",
				"resumed", request.SessionID != "")
		default:
			log.Info("I2: the end of the implementation was not decided; the next poll decides", "labels", sub.Labels)
			return
		}
	}
}

// stopForWorkDirectory stops the implementation for the Owner when the work
// directory of the second request of this stay was not prepared: the first
// request and the second one both sent nothing to the Implementer, and a
// third one would fail the same way. After the first failure nothing
// changes here, and the next poll requests the implementation again. While
// cumin is stopping, and when the issue left cumin/status/implementing,
// nothing changes either.
func (s *Service) stopForWorkDirectory(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, number int, req implementerRequest) {
	if !req.again || ctx.Err() != nil {
		return
	}
	token, err := target.Token(ctx)
	if err != nil {
		log.Error("I2: no token; the next poll decides the end of the implementation", "error", err.Error())
		return
	}
	sub, ok := s.subIssueNow(ctx, log, target, number)
	if !ok || !ImplementationNeedsFacts(sub, false) {
		return
	}
	a := StopImplementation{Number: number, Reason: WorkDirectoryReason(), PullRequest: req.pullRequest, Retried: true}
	if err := s.stopImplementation(ctx, log, token, target, settings, sub, a); err != nil {
		log.Error("the issue was not moved; the next poll decides again", "error", err.Error())
	}
}

// stopAfterBlocked stops the issue for a blocked result, with the row of
// the result (I2 for the Implementer, I10 for the Reviewer): the
// blocked_reason of the agent becomes the comment, because the agent
// already wrote it in the form of templates/decision-request.md, and its
// first line is the question for the Owner. Nothing is retried: a blocked
// result usually means that a requirement is missing, so the Owner answers
// first (issue-states.md, the paragraph on a blocked result).
//
// The label changes first, then the comment is written. A comment that
// GitHub refuses then leaves the issue in cumin/status/awaiting-decision,
// so no poll requests the same work again; its whole text goes to the log.
func (s *Service) stopAfterBlocked(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, row, role string, number int, reason string) {
	s.stopBlocked(ctx, log, target, settings, row, role, number, reason, labelsNow(s.subIssueNow(ctx, log, target, number)))
}

// stopBlocked is stopAfterBlocked with the labels of the issue that the
// caller read.
func (s *Service) stopBlocked(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, row, role string, number int, reason string, labels []string) {
	question := firstLine(reason)
	log.Warn(row+": the agent returned blocked", "reason", question)
	s.stopForOwner(ctx, log, target, settings, stop{
		row:        row,
		issue:      number,
		labels:     labels,
		labelFirst: true,
		reason:     "the " + role + " returned blocked: " + question,
		comment:    reason,
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

// readImplementingFacts adds the facts of the way out of
// cumin/status/implementing to each sub-issue of the snapshot that needs
// them (ImplementationNeedsFacts): it reads that issue again, so that the
// decision judges on the pull requests and the labels of this moment. A
// failed read leaves the facts out, so nothing is decided for that issue in
// this poll.
func (s *Service) readImplementingFacts(ctx context.Context, log *slog.Logger, token string, target Target, settings *RepositorySettings, snapshot *Snapshot) {
	for i := range snapshot.RequirementIssues {
		for j := range snapshot.RequirementIssues[i].SubIssues {
			sub := &snapshot.RequirementIssues[i].SubIssues[j]
			if !ImplementationNeedsFacts(*sub, snapshot.Running[sub.Number]) {
				continue
			}
			if read, ok := s.implementingNow(ctx, log.With("issue", sub.Number), token, target, settings, sub.Number, ""); ok {
				*sub = read
			}
		}
	}
}

// implementingNow reads one implementation issue again, and only that
// issue, with the facts that ImplementationEnd decides from: the account
// and the time of the newest cumin/status/implementing, the decision
// requests after it, the open pull requests of the branch, the head commit
// of the worktree, and what the state file holds for this stay. branch is the branch of
// the run; the poll passes none and takes the branch that a claim would
// choose (ClaimBranch).
//
// The second value is false when a read failed, and when the issue is not
// an open issue in cumin/status/implementing any more: nothing is decided
// then. A label that does not count ends the read, and the Owner is told
// once. A worktree that the Host does not hold gives an empty head commit,
// so the pull request does not pass the check.
func (s *Service) implementingNow(ctx context.Context, log *slog.Logger, token string, target Target, settings *RepositorySettings, number int, branch string) (SubIssue, bool) {
	owner, repo := target.Repository.Owner, target.Repository.Name
	read, err := s.GitHub.ReadSubIssue(ctx, token, owner, repo, number)
	if err != nil {
		log.Error("I2: the issue was not read again; the next poll decides", "error", err.Error())
		return SubIssue{}, false
	}
	log.Debug("read the issue again", "issue", number, "rate_limit_cost", read.RateLimit.Cost, "rate_limit_remaining", read.RateLimit.Remaining)
	sub := toSubIssue(read.Issue)
	if !ImplementationNeedsFacts(sub, false) {
		log.Info("I2: the issue is not in cumin/status/implementing; nothing changes", "labels", sub.Labels)
		return sub, false
	}
	actor, counts, err := s.readStatusActor(ctx, token, target, number, LabelImplementing, false)
	if err != nil {
		log.Error("the actor of the newest "+LabelImplementing+" was not read; the next poll decides", "error", err.Error())
		return sub, false
	}
	facts := &ImplementingFacts{StatusCounts: counts, ImplementingAt: actor.At, MaxLinks: github.MaxOpenClosingPullRequests}
	sub.Implementing = facts
	if !counts {
		s.tellStatusOfAnother(ctx, log, target, settings, number, LabelImplementing, actor)
		return sub, true
	}
	if s.Agents == nil {
		return sub, false
	}
	if facts.Implementer, err = s.Agents.BotLogin(ctx, owner, config.RoleImplementer); err != nil {
		log.Error("I2: the login of the Implementer App was not read; the next poll decides", "error", err.Error())
		return sub, false
	}
	// cumin-core posts the blocked_reason of the Implementer, so its
	// decision request is a question too.
	askers := []string{facts.Implementer}
	if target.Login != nil {
		core, err := target.Login(ctx)
		if err != nil {
			log.Error("I2: the login of cumin-core was not read; the next poll decides", "error", err.Error())
			return sub, false
		}
		askers = append(askers, core)
	}
	comments, rate, err := s.GitHub.ReadIssueComments(ctx, token, owner, repo, number, facts.ImplementingAt)
	if err != nil {
		log.Error("I2: the comments were not read; the next poll decides", "error", err.Error())
		return sub, false
	}
	log.Debug("I2: read the comments", "rate_limit_cost", rate.Cost, "rate_limit_remaining", rate.Remaining)
	asked := make([]Comment, 0, len(comments))
	for _, c := range comments {
		asked = append(asked, Comment{Author: c.Author, CreatedAt: c.CreatedAt, Body: c.Body, URL: c.URL})
	}
	facts.QuestionAt = QuestionAt(asked, askers...)
	if facts.Branch = branch; branch == "" {
		facts.Branch, _ = ClaimBranch(sub)
	}
	listed, err := s.GitHub.ListOpenPullRequestsOfBranch(ctx, token, owner, repo, facts.Branch)
	if err != nil {
		log.Error("I2: the open pull requests of the branch were not read; the next poll decides", "branch", facts.Branch, "error", err.Error())
		return sub, false
	}
	for _, pr := range listed {
		facts.OnBranch = append(facts.OnBranch, PullRequest{Number: pr.Number, NodeID: pr.NodeID, HeadCommit: pr.HeadCommit, HeadBranch: pr.HeadBranch, Author: pr.Author})
	}
	workDir := s.Workspace.Dir(agent.Checkout{Owner: owner, Repo: repo, Issue: number, Role: config.RoleImplementer, Branch: facts.Branch})
	if facts.LocalHead, err = s.Workspace.Head(ctx, workDir); err != nil {
		log.Info("I2: the head commit of the work directory was not read", "error", err.Error())
		facts.LocalHead = ""
	}
	stored := s.State.Issue(target.Repository.String(), number)
	facts.RequestedAgain, facts.ConflictRequested = stored.ImplementationRequests > 0, stored.ConflictResolution
	return sub, true
}

// waitForChecks applies "wait for the checks" (I2): the pull request passes
// the check. When the issue has no closing link to the pull request,
// cumin-core adds it and reads the issue once more to see it; then the
// status label becomes cumin/status/checking. A link that GitHub refuses,
// and a link that is still missing, hand the issue back to the Owner
// through the stop step, with one sentence.
//
// A failed read or label change is returned and changes nothing more: the
// issue keeps cumin/status/implementing, and the next poll decides again
// from the same facts.
func (s *Service) waitForChecks(ctx context.Context, log *slog.Logger, token string, target Target, settings *RepositorySettings, sub SubIssue, a WaitForChecks) error {
	owner, repo := target.Repository.Owner, target.Repository.Name
	stopI2 := func(reason string) {
		s.stopForOwner(ctx, log, target, settings, stop{
			row:     RowI2,
			issue:   a.Number,
			labels:  sub.Labels,
			reason:  reason,
			comment: StopNote(RowI2, reason, a.PullRequest, false),
		})
	}
	if a.AddLink {
		pr := a.PullRequest
		if err := s.GitHub.AddClosingLink(ctx, token, sub.NodeID, a.PullRequestNodeID); err != nil {
			if temporary(err) != nil {
				return fmt.Errorf("I2: add the closing link of issue #%d: %w", a.Number, err)
			}
			log.Warn("I2: the closing link was not added", "pull_request", pr, "error", err.Error())
			stopI2(LinkFailedReason(pr, githubAnswer(err)))
			return nil
		}
		again, err := s.GitHub.ReadSubIssue(ctx, token, owner, repo, a.Number)
		if err != nil {
			return fmt.Errorf("I2: read issue #%d after the closing link: %w", a.Number, err)
		}
		log.Debug("read the issue again", "issue", a.Number, "rate_limit_cost", again.RateLimit.Cost, "rate_limit_remaining", again.RateLimit.Remaining)
		sub = toSubIssue(again.Issue)
		if !linksPullRequest(sub, pr) {
			log.Warn("I2: the closing link is missing after cumin-core added it", "pull_request", pr)
			stopI2(LinkMissingReason(pr))
			return nil
		}
		log.Info("I2: added the closing link", "pull_request", pr)
	}
	labels := ReplaceStatusLabel(sub.Labels, LabelChecking)
	if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, a.Number, labels); err != nil {
		return fmt.Errorf("I2: move issue #%d to checking: %w", a.Number, err)
	}
	log.Info("I2: verified the pull request", "pull_request", a.PullRequest, "labels", labels)
	return nil
}

// stopImplementation applies "stop the implementation for the Owner": the
// implementation issue moves from cumin/status/implementing to
// cumin/status/awaiting-decision, and the Owner is notified. After a
// question of the Implementer, its comment holds the reason, and cumin
// writes none. Otherwise cumin writes the reason on the issue.
//
// The label moves first. When that fails, nothing else happens: the state
// file keeps the count of the second request, so the next poll decides the
// same stop and requests nothing.
func (s *Service) stopImplementation(ctx context.Context, log *slog.Logger, token string, target Target, settings *RepositorySettings, sub SubIssue, a StopImplementation) error {
	owner, repo := target.Repository.Owner, target.Repository.Name
	labels := ReplaceStatusLabel(sub.Labels, LabelAwaitingDecision)
	if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, a.Number, labels); err != nil {
		return fmt.Errorf("I2: move issue #%d to awaiting-decision: %w", a.Number, err)
	}
	if !a.Question {
		log.Warn("I2: the implementation stops for the Owner", "reason", a.Reason, "pull_request", a.PullRequest, "retried", a.Retried, "labels", labels)
		s.stopForOwner(ctx, log, target, settings, stop{
			row:       RowI2,
			issue:     a.Number,
			labelDone: true,
			reason:    a.Reason,
			comment:   StopNote(RowI2, a.Reason, a.PullRequest, a.Retried),
		})
		return nil
	}
	log = log.With("row", RowI2)
	log.Info("I2: the Implementer asked a question; the issue waits for the Owner", "labels", labels)
	s.notifyOwner(ctx, log, settings.Settings.Notify.DiscordEnabled, notify.Notification{
		Row:        RowI2,
		Reason:     "The Implementer asked a question during the implementation.",
		Repository: target.Repository.String(),
		Subject:    fmt.Sprintf("issue #%d", a.Number),
		Link:       github.IssueURL(owner, repo, a.Number),
	})
	return nil
}

// requestImplementationAgain applies "request the implementation again" at
// a poll: the issue is in cumin/status/implementing, no Implementer runs,
// and the pull request does not pass the check. The work continues on the
// branch of the issue, in the kept session when the state file holds one.
// When the state file says that the stay is a conflict resolution, the
// request is the conflict resolution again, as at the end of a run.
// The count of the state file is raised when the run is about to start
// (runImplementer), so that the request is sent once for each stay in
// cumin/status/implementing, and a start that failed does not use it up.
func (s *Service) requestImplementationAgain(ctx context.Context, token string, target Target, settings *RepositorySettings, sub SubIssue, defaultBranch string, a RequestImplementationAgain) error {
	permit, ok := s.permitStart(ctx, s.logger().With("repository", target.Repository.String(), "issue", a.Number), "implementation again", config.RoleImplementer, target, a.Number)
	if !ok {
		return nil
	}
	ownerLogin, err := s.readOwnerLogin(ctx, token, target, a.Number)
	if err != nil {
		return fmt.Errorf("I2: read the login of the Owner of issue #%d: %w", a.Number, err)
	}
	repository := target.Repository.String()
	branch := sub.Implementing.Branch
	req := implementerRequest{
		row: RowI2, kind: "implement", branch: branch, ownerLogin: ownerLogin, again: true, count: true, permit: permit,
		sessionID: s.State.Issue(repository, a.Number).SessionID,
		text: func(workDir string) string {
			return ImplementRequestText(repository, a.Number, branch, workDir)
		},
	}
	if _, pullRequest := ClaimBranch(sub); pullRequest != 0 {
		req.kind, req.pullRequest = "continue", pullRequest
		req.text = func(workDir string) string {
			return ContinueRequestText(repository, a.Number, pullRequest, branch, workDir)
		}
		if sub.Implementing.ConflictRequested {
			req.kind = "conflict resolution"
			req.text = func(workDir string) string {
				return ConflictResolutionRequestText(repository, a.Number, pullRequest, branch, workDir, defaultBranch)
			}
		}
	}
	s.logger().Info("I2: the pull request does not pass the check; the implementation is requested again",
		"repository", repository, "issue", a.Number, "kind", req.kind)
	return s.goImplementer(ctx, target, settings, a.Number, req)
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
