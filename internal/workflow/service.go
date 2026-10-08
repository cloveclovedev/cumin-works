package workflow

// This file holds the Service type, the loop of the polls (Run), the stop of
// cumin, and the set of issues whose agent is running. It also holds the
// steps that every role uses: the label move, the permit and the start of
// an agent, the run of one request, the kept session, and the reads of an
// issue that more than one step file needs. poll.go holds one poll, and
// each step file holds the actions of one role.
// docs/ja/designs/poll.md and docs/ja/designs/cumin-core.md, the topic on
// the stop.

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
	// Login returns the login of the bot of cumin-core, "<slug>[bot]".
	// "write the follow-up note" uses it to find its own follow-up notes.
	// cumin run passes the BotLogin method of the same github.TokenSource.
	// Without it, cumin writes no follow-up note.
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
	// Notify sends the notification that an issue needs an answer. cmd/cumin
	// builds it from the webhook URL in the Keychain. A nil notifier
	// reports that no channel is configured, which is logged.
	Notify *notify.Notifier
	// State is what cumin keeps on the Host for each implementation issue:
	// the session of the last run, and the number of check fix requests
	// ("request a check fix"); and for each requirement issue in
	// cumin/status/accepting: the
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
	// ("resume agent starts", when the Operator allows to use up the 5-hour
	// window). Each check before a start reads it. Empty means no allowance.
	AllowancePath string
	// Now is the clock of the quota decisions ("stop agent starts"). Nil
	// means time.Now.
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
	// MonitorPath is the monitor file that a tool outside cumin reads
	// (monitorfile.go). Each poll writes it at its end, and nothing reads
	// it. Empty means that cumin writes no monitor file.
	MonitorPath string

	// running counts the agent runs that the polls started. Each run has
	// its own goroutine, so that the poll goes on while an agent works.
	running sync.WaitGroup
	// inProgress holds the issues whose agent is running, each with the run
	// that the monitor file shows, for the log of the stop and for the
	// monitor file.
	progressMu sync.Mutex
	inProgress map[inProgressKey]agentRun
	// monitorFacts are the facts of the last poll in the monitor file, for
	// the write at the end of a run. monitorMu guards them and every write
	// of the file; it is taken before progressMu.
	monitorMu    sync.Mutex
	monitorFacts *MonitorFacts
	// monitorWaiting keeps, for each repository, the issues that wait for a
	// Maintainer in the snapshot of its last read, for the monitor file.
	// monitorMu guards it.
	monitorWaiting map[string][]state.MonitorWaiting
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
	// so that a failure that repeats is notified once
	// (pollfailure.go).
	failureMu sync.Mutex
	// readyTold holds, for each issue, the time of the status label event
	// of an account that does not count (another account than a Maintainer
	// for cumin/status/ready; than cumin-core or a Maintainer for the others)
	// that cumin already logged and notified about. cumin can lose
	// it: after a restart it notifies once more.
	readyMu      sync.Mutex
	readyTold    map[string]time.Time
	pollFailures map[string]*repeatedFailure
	// unreadTold holds, for each repository, the issues over a read limit
	// that cumin already notified about, each with its requirement issue
	// (unreadissue.go). failureMu guards it.
	unreadTold map[string]map[toldUnreadIssue]int

	// quota keeps which notifications of "stop agent starts" cumin already sent
	// (quota.go). The polls and the ends of the runs share it.
	quotaMu sync.Mutex
	quota   quotaNotices
	// waitingTold says that cumin sent "tell that cumin waits" since it last
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

// agentRun is what cumin keeps of the run of an issue in work, for the
// monitor file (monitorfile.go).
type agentRun struct {
	role config.Role
	// request is the request kind, as the role file names it.
	request string
	// title is the title of the issue in the snapshot of the request.
	title string
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
// cumin/status/implementing, and a Maintainer restarts it with
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
// cumin/status/implementing in that case, so a Maintainer has to restart it,
// and the line of the stop must name it. Nothing removes entries after the
// signal; the process is on its way out.
func (s *Service) markInProgress(ctx context.Context, repository string, issue int, run agentRun) func() {
	key := inProgressKey{repository: repository, issue: issue}
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	if s.inProgress == nil {
		s.inProgress = map[inProgressKey]agentRun{}
	}
	s.inProgress[key] = run
	s.started++
	return func() { s.endRun(ctx, key) }
}

// noteRequest keeps the role and the request kind of the run of an issue in
// work. One step in work can go on with another request: the cause or the
// review fix after a review.
func (s *Service) noteRequest(repository string, issue int, role config.Role, request string) {
	key := inProgressKey{repository: repository, issue: issue}
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	if run, ok := s.inProgress[key]; ok {
		run.role, run.request = role, request
		s.inProgress[key] = run
	}
}

// endRun removes an issue from the set of issues in work when its run ends,
// writes the monitor file without the run, and wakes Run. See
// markInProgress for the case that leaves the entry.
func (s *Service) endRun(ctx context.Context, key inProgressKey) {
	if !s.removeInProgress(ctx, key) {
		return
	}
	// The file comes before the wake, so that the poll that the wake starts
	// writes after it.
	s.writeMonitorFileAfterRun()
	select {
	case s.runEnded <- struct{}{}:
	default:
	}
}

// removeInProgress removes an issue from the set of issues in work, and
// says whether it did.
func (s *Service) removeInProgress(ctx context.Context, key inProgressKey) bool {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	if ctx.Err() != nil {
		return false
	}
	// The note comes first, so that a poll never sees neither the run
	// nor its end.
	s.noteRunEnded(key.repository)
	delete(s.inProgress, key)
	return true
}

// moveIssue replaces the status label of an issue: it sets the labels of the
// issue with the new status label, and returns them. The caller puts the
// name of its action before the error.
func (s *Service) moveIssue(ctx context.Context, token string, target Target, number int, labels []string, status string) ([]string, error) {
	moved := ReplaceStatusLabel(labels, status)
	if err := s.GitHub.SetIssueLabels(ctx, token, target.Repository.Owner, target.Repository.Name, number, moved); err != nil {
		return nil, fmt.Errorf("move issue #%d to %s: %w", number, status, err)
	}
	return moved, nil
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

// startRequest builds the start request of a role for an issue, with the
// facts of the run. The Planner works on a requirement issue, and the
// Implementer and the Reviewer on an implementation issue. The caller sets
// the session to resume.
func startRequest(target Target, settings *RepositorySettings, role config.Role, number int, issueOwnerLogin, text, workDir string) agent.StartRequest {
	kind := agent.IssueKindImplementation
	if role == config.RolePlanner {
		kind = agent.IssueKindRequirement
	}
	roleSettings := settings.Settings.Roles[role]
	return agent.StartRequest{
		Owner:        target.Repository.Owner,
		Repo:         target.Repository.Name,
		Role:         role,
		RiskCriteria: settings.RiskCriteria,
		Facts:        agent.Facts{IssueNumber: number, IssueKind: kind, IssueOwnerLogin: issueOwnerLogin, ProtectedPaths: settings.ProtectedPaths},
		Text:         text,
		WorkDir:      workDir,
		Settings:     &roleSettings,
	}
}

// runEnd is the kind of the end of one request to an agent.
type runEnd int

const (
	// runDone: the agent returned done.
	runDone runEnd = iota
	// runBlocked: the agent returned blocked.
	runBlocked
	// runAbnormal: the run ended abnormally, and cumin goes on.
	runAbnormal
	// runNotStarted: the agent was not started.
	runNotStarted
	// runStopping: the run ended abnormally while cumin is stopping. The
	// label stays, and the next start of cumin decides from the facts on
	// GitHub.
	runStopping
)

// requestEnd is the end of one request to an agent: its kind, the run of a
// done or a blocked result, and the abnormal end.
type requestEnd struct {
	kind     runEnd
	run      *agent.Run
	abnormal *agent.AbnormalEnd
}

// keptSession says which ends of a run keep the session of the run.
type keptSession int

const (
	// keepNoSession: every request starts a new session (the split).
	keepNoSession keptSession = iota
	// keepEndedSession: a run that ended, done or blocked (the Implementer
	// and the Reviewer).
	keepEndedSession
	// keepResumableSession: a done result and an abnormal end, which the
	// second request resumes (the acceptance check).
	keepResumableSession
)

// runRequest runs one request to an agent and returns its end. It is the
// only caller of startAgent. It logs the end, applies "stop agent starts"
// from the usage of the run, and keeps the session as keep says. What
// follows the end (the counts, the second request, the stop) belongs to the
// caller.
func (s *Service) runRequest(ctx context.Context, log *slog.Logger, target Target, number int, permit StartPermit, request agent.StartRequest, keep keptSession) requestEnd {
	run, err := s.startAgent(ctx, permit, request)
	var abnormal *agent.AbnormalEnd
	switch {
	case errors.As(err, &abnormal):
		log.Info("the agent run ended abnormally", "kind", abnormal.Kind.String(),
			"session_id", abnormal.SessionID, "detail", abnormal.Detail)
		if ctx.Err() != nil {
			return requestEnd{kind: runStopping, abnormal: abnormal}
		}
		if keep == keepResumableSession {
			s.keepSession(log, target, request.Role, number, abnormal.SessionID)
		}
		return requestEnd{kind: runAbnormal, abnormal: abnormal}
	case err != nil:
		log.Error("the agent was not started", "error", err.Error())
		return requestEnd{kind: runNotStarted}
	}
	log.Info("the agent run ended", "result", run.Result.Result, "session_id", run.SessionID)
	s.quotaAfterRun(ctx, log, target, number, run)
	if run.Result.Result != agent.ResultDone {
		if keep == keepEndedSession {
			s.keepSession(log, target, request.Role, number, run.SessionID)
		}
		return requestEnd{kind: runBlocked, run: run}
	}
	if keep != keepNoSession {
		s.keepSession(log, target, request.Role, number, run.SessionID)
	}
	return requestEnd{kind: runDone, run: run}
}

// startStay writes, in the state file, the start of a new stay of the
// implementation issue in cumin/status/implementing: no second request
// yet, and whether the request is a conflict resolution (of "start the
// merge" or of "request a conflict resolution"). A
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

// readIssueOwnerLogin reads the login of the Issue Owner for the facts of a
// start request (docs/ja/requirements/agents/common.md, the facts of the
// start request): the account that added the newest cumin/status/ready to the
// issue of the run, or to a sub-issue when a requirement issue has no such
// event. The login is passed only when that account is a Maintainer
// (IsMaintainer). The empty login says that there is no Issue Owner login. A
// failed read is an error: the caller reads before it changes the label,
// changes nothing, and sends no request, so the next poll tries again.
func (s *Service) readIssueOwnerLogin(ctx context.Context, token string, target Target, number int) (string, error) {
	actor, isMaintainer, err := s.readReadyActor(ctx, token, target, number, true)
	if err != nil || !isMaintainer {
		return "", err
	}
	return actor.Login, nil
}

// readReadyActor reads the account that added the newest cumin/status/ready
// to the issue, and whether that account is a Maintainer (IsMaintainer).
// subIssues lets an event of a sub-issue answer for a requirement issue with
// no such event; the check of "request the split" and of "request the
// implementation" passes false, so that only an event of the issue itself can
// start work.
func (s *Service) readReadyActor(ctx context.Context, token string, target Target, number int, subIssues bool) (github.LabelActor, bool, error) {
	return s.readStatusActor(ctx, token, target, number, LabelReady, subIssues)
}

// readStatusActor reads the account that added the newest status label to
// the issue, and whether the label counts as a state (StatusLabelCounts):
// the account is a Maintainer or, for every label but cumin/status/ready, the
// cumin-core App. The permission is read only for a person: a GitHub App is
// never a Maintainer. A failed read of the login of cumin-core is an error, so
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

// stopAfterBlocked stops the issue for a blocked result, with the action of
// the result ("stop the implementation" for the Implementer, "stop the
// review" for the Reviewer): the
// blocked_reason of the agent becomes the comment, because the agent
// already wrote it in the form of templates/decision-request.md, and its
// first line is the question for a Maintainer. Nothing is retried: a blocked
// result usually means that a requirement is missing, so a Maintainer answers
// first (issue-states.md, the paragraph on a blocked result).
//
// The label changes first, then the comment is written. A comment that
// GitHub refuses then leaves the issue in cumin/status/awaiting-decision,
// so no poll requests the same work again; its whole text goes to the log.
func (s *Service) stopAfterBlocked(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, action ActionName, role string, number int, reason string) {
	s.stopBlocked(ctx, log, target, settings, action, role, number, reason, labelsNow(s.subIssueNow(ctx, log, target, number)))
}

// stopBlocked is stopAfterBlocked with the labels of the issue that the
// caller read.
func (s *Service) stopBlocked(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, action ActionName, role string, number int, reason string, labels []string) {
	question := firstLine(strings.TrimSpace(reason))
	log.Warn(string(action)+": the agent returned blocked", "reason", question)
	s.stopForMaintainer(ctx, log, target, settings, stop{
		action:     action,
		issue:      number,
		labels:     labels,
		labelFirst: true,
		reason:     "the " + role + " returned blocked: " + question,
		comment:    reason,
	})
}

// readFacts is the walk of the three read...Facts functions: for each
// sub-issue of the snapshot that needs the facts of a state (needs), it
// reads that issue again (read) and replaces it. A read that returns false
// leaves the sub-issue as the poll read it.
func readFacts(log *slog.Logger, snapshot *Snapshot, needs func(sub SubIssue, running bool) bool, read func(log *slog.Logger, number int) (SubIssue, bool)) {
	for i := range snapshot.RequirementIssues {
		for j := range snapshot.RequirementIssues[i].SubIssues {
			sub := &snapshot.RequirementIssues[i].SubIssues[j]
			if !needs(*sub, snapshot.Running[sub.Number]) {
				continue
			}
			if again, ok := read(log.With("issue", sub.Number), sub.Number); ok {
				*sub = again
			}
		}
	}
}

// temporary returns err when it is a temporary failure of a call to GitHub,
// and nil otherwise: only a temporary failure keeps a step.
func temporary(err error) error {
	if github.IsTemporary(err) {
		return err
	}
	return nil
}

// keepSession stores the session of the run of the role, so that a request
// in the same session can resume it ("request a check fix", "request a review
// fix", and the later rounds of "request the review"). The Implementer and
// the Reviewer each keep their own. A failure is logged and changes nothing
// else: the next request then starts a new session.
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

// runningIssues returns the issues of the repository whose agent runs now.
func (s *Service) runningIssues(repository string) map[int]bool {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	running := map[int]bool{}
	for key := range s.inProgress {
		if key.repository == repository {
			running[key.issue] = true
		}
	}
	return running
}

func (s *Service) logger() *slog.Logger {
	if s.Logger == nil {
		return slog.Default()
	}
	return s.Logger
}
