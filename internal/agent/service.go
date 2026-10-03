package agent

// This file puts the pieces of one agent start in order: create a fresh
// token of the role limited to the repository, read the bot identity of the
// role, then run the CLI in the work directory. It also reads the quota
// usage, which the caller asks for before a new start (R1, I1) only.
// docs/ja/designs/agent-run.md (the topic on the steps of one request)
// records the order and the reasons. The rest of cumin calls only Start.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
)

// Service starts agents. One Service serves every role and repository.
type Service struct {
	// Roles are the settings of each role: the CLI, its path, the model,
	// and the time limit.
	Roles map[config.Role]config.RoleSettings
	// Apps are the credentials of the GitHub App of each role, for each
	// owner of a target repository: the setting github_apps.<owner>.<role>.
	// The outer key is the owner (an organization or a user).
	Apps map[string]map[config.Role]github.AppCredentials
	// GitHub creates the tokens and reads the bot users.
	GitHub *github.AppClient
	// SkillsDir is the directory that holds one directory of skills for
	// each role, as cumin run wrote them at its start (WriteSkills). A run
	// gets the directory of its own role.
	SkillsDir string
	// Logger may be nil. Then the default logger is used.
	Logger *slog.Logger
	// Grace and QuotaTimeLimit are passed to the CLI adapter. Zero means
	// its defaults. Tests shorten them.
	Grace          time.Duration
	QuotaTimeLimit time.Duration
	// Now is the clock that gives the start of a run, for the end time
	// that the agent receives. Nil means time.Now. Tests set it.
	Now func() time.Time

	mu         sync.Mutex
	identities map[appKey]identity
	botLogins  map[appKey]string
}

// BotLogin returns the login of the bot of the App of a role for an owner,
// "<slug>[bot]", as the REST API shows the author of a comment. It needs no
// installation token: the slug comes from GET /app with the JWT of the
// App. The slug never changes while cumin runs, so it is read once.
func (s *Service) BotLogin(ctx context.Context, owner string, role config.Role) (string, error) {
	key := appKey{owner: strings.ToLower(owner), role: role}
	s.mu.Lock()
	login, ok := s.botLogins[key]
	s.mu.Unlock()
	if ok {
		return login, nil
	}
	cred, err := s.app(owner, role)
	if err != nil {
		return "", err
	}
	app, err := s.GitHub.GetApp(ctx, cred)
	if err != nil {
		return "", err
	}
	login = app.Slug + "[bot]"
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.botLogins == nil {
		s.botLogins = map[appKey]string{}
	}
	s.botLogins[key] = login
	return login, nil
}

// appKey names one App: the owner of the repository (in lower case, as
// GitHub account names are case-insensitive) and the role.
type appKey struct {
	owner string
	role  config.Role
}

// identity is the bot user of the App of a role, as the author of its
// commits. It never changes, so it is read once and kept in memory.
type identity struct {
	name  string
	email string
}

// StartRequest is one request to start an agent.
type StartRequest struct {
	Owner string
	Repo  string
	Role  config.Role
	// RiskCriteria is the risk criteria text of the target repository,
	// resolved over its three levels by the caller. It becomes the last
	// part of the instruction of the role (instruction.go). An empty text
	// leaves the instruction without that part.
	RiskCriteria string
	// Text is the request text: what the agent must do this time.
	Text string
	// WorkDir is the worktree that the agent runs in.
	WorkDir string
	// Settings replace the settings of the role for this request. The poll
	// passes the settings of the target repository, because its
	// .cumin/config.toml may set the CLI and the model of a role
	// (cumin-core.md, the topic on settings). Nil uses the settings of the
	// Service, which are the ones of the Host.
	Settings *config.RoleSettings
	// SessionID continues an earlier session. Empty starts a new session.
	SessionID string
}

// ReadQuota reads the quota usage with one minimal run of the CLI of the
// role. The caller decides the limits (Q1) before a new start; Start itself
// reads nothing, so that a request that is not a new start (I4, I5, the
// Reviewer) goes on over a limit (designs/quota.md, the topic on the check
// before a start). An error is a *QuotaNotRead.
func (s *Service) ReadQuota(ctx context.Context, role config.Role) (QuotaUsage, error) {
	settings, ok := s.Roles[role]
	if !ok {
		return QuotaUsage{}, &QuotaNotRead{Reason: "no settings for the role " + string(role)}
	}
	log := s.logger().With("role", role)
	cli := ClaudeCode{Path: settings.CLIPath, Logger: log, Grace: s.Grace, QuotaTimeLimit: s.QuotaTimeLimit}
	return cli.ReadQuota(ctx)
}

// Start runs one request. The steps, in order: the instruction of the role
// is composed; a token of the App of the role is created, limited to the
// repository;
// the bot identity of the role is read, once; then the CLI runs with the
// token and the identity. The token lives only in the request of the run.
// The request text of the run starts with the facts of the run (facts.go):
// the time limit of the settings that the run uses, and the end time.
// An error from the run is an *AbnormalEnd.
func (s *Service) Start(ctx context.Context, req StartRequest) (*Run, error) {
	settings, ok := s.Roles[req.Role]
	if !ok {
		return nil, fmt.Errorf("start %s on %s/%s: no settings for the role", req.Role, req.Owner, req.Repo)
	}
	if req.Settings != nil {
		settings = *req.Settings
	}
	cred, err := s.app(req.Owner, req.Role)
	if err != nil {
		return nil, fmt.Errorf("start %s on %s/%s: %w", req.Role, req.Owner, req.Repo, err)
	}
	// The instruction of the role, with the risk criteria of the
	// repository at its end. It is composed before anything is created on
	// GitHub, so that a wrong role costs no token.
	roleInstruction, err := instruction(req.Role, req.RiskCriteria)
	if err != nil {
		return nil, fmt.Errorf("start %s on %s/%s: %w", req.Role, req.Owner, req.Repo, err)
	}
	// The logger names the role and the repository. The CLI adapter adds
	// nothing of its own to it, so that no line carries the role twice
	// and every line of the adapter names it.
	log := s.logger().With("role", req.Role, "repository", req.Owner+"/"+req.Repo)
	cli := ClaudeCode{Path: settings.CLIPath, Logger: log, Grace: s.Grace, QuotaTimeLimit: s.QuotaTimeLimit}

	// 1. A fresh token for this request. A run lasts up to 55 minutes and
	// a token lives one hour, so no token is reused.
	token, err := s.GitHub.CreateInstallationToken(ctx, cred, string(req.Role), req.Owner, req.Repo)
	if err != nil {
		return nil, fmt.Errorf("start %s on %s/%s: %w", req.Role, req.Owner, req.Repo, err)
	}
	log.Info("agent token created", "expires_at", token.ExpiresAt)

	// 2. The identity of the commits of the agent.
	id, err := s.identity(ctx, appKey{strings.ToLower(req.Owner), req.Role}, cred, token.Token)
	if err != nil {
		return nil, fmt.Errorf("start %s on %s/%s: %w", req.Role, req.Owner, req.Repo, err)
	}

	// 3. The run. The login of the bot goes with the result, so that the
	// caller can compare it with the author of a pull request (I2). The
	// clock is read right before the run, so that the end time that the
	// agent receives is not later than the time at which the run is cut.
	facts := runFacts{TimeLimit: settings.TimeLimit, End: s.now().Add(settings.TimeLimit)}
	run, err := cli.Run(ctx, Request{
		Role:            req.Role,
		RoleInstruction: roleInstruction,
		Text:            requestWithFacts(facts, req.Text),
		WorkDir:         req.WorkDir,
		SessionID:       req.SessionID,
		SkillsDir:       s.skillsDir(req.Role),
		Model:           settings.Model,
		TimeLimit:       settings.TimeLimit,
		Credentials:     Credentials{Token: token.Token, AuthorName: id.name, AuthorEmail: id.email},
	})
	if err != nil {
		return nil, err
	}
	run.BotLogin = id.name
	return run, nil
}

// app returns the credentials of the App of the role for the owner. GitHub
// account names are case-insensitive, so the owner matches the settings
// key without regard to case; two keys that differ only by case are an
// error, as in the settings of cumin-core.
func (s *Service) app(owner string, role config.Role) (github.AppCredentials, error) {
	var found []string
	for key := range s.Apps {
		if strings.EqualFold(key, owner) {
			found = append(found, key)
		}
	}
	if len(found) > 1 {
		return github.AppCredentials{}, fmt.Errorf("the settings have github_apps for %q more than once, with different cases", owner)
	}
	if len(found) == 1 {
		if cred, ok := s.Apps[found[0]][role]; ok {
			return cred, nil
		}
	}
	return github.AppCredentials{}, fmt.Errorf("no GitHub App for the role and the owner (the setting github_apps.%s.%s)", owner, role)
}

// identity returns the bot identity of the role: the slug of the App from
// GET /app, and the id of the bot user from GET /users/<slug>[bot]. The
// name is "<slug>[bot]" and the email is
// "<id>+<slug>[bot]@users.noreply.github.com", the form that GitHub links
// to the bot (measured-constraints.md row 49). One App serves one owner
// and one role, so the cache is keyed by both.
func (s *Service) identity(ctx context.Context, key appKey, cred github.AppCredentials, token string) (identity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.identities[key]; ok {
		return id, nil
	}
	app, err := s.GitHub.GetApp(ctx, cred)
	if err != nil {
		return identity{}, err
	}
	login := app.Slug + "[bot]"
	user, err := s.GitHub.GetUser(ctx, token, login)
	if err != nil {
		return identity{}, err
	}
	id := identity{name: user.Login, email: fmt.Sprintf("%d+%s@users.noreply.github.com", user.ID, user.Login)}
	if s.identities == nil {
		s.identities = map[appKey]identity{}
	}
	s.identities[key] = id
	s.logger().Info("agent identity read", "role", key.role, "owner", key.owner, "login", user.Login)
	return id, nil
}

// stopMargin is the time that a run needs after the grace, to let os/exec
// end the CLI, to read what is left of its output, and to send SIGKILL to
// the process group. The steps take milliseconds; the value is generous,
// because a caller uses it to decide when to give up waiting.
const stopMargin = 5 * time.Second

// StopBudget is the longest time that one run takes to end after its
// context is cancelled: SIGTERM to the process group of the CLI, SIGKILL
// after the grace of the adapter, and the clean-up that follows
// (docs/ja/designs/agent-run.md, the topic on the time limit of a run).
//
// A caller that waits for the runs of a stop must wait at least this long.
// A shorter wait can end cumin while a CLI of an agent is still alive with
// the token of its role in its environment.
func (s *Service) StopBudget() time.Duration {
	grace := s.Grace
	if grace <= 0 {
		grace = defaultGrace
	}
	return grace + stopMargin
}

// HostWarnings returns one warning for each global instruction file that
// the CLI of a role reads and cannot ignore. cumin run logs them at its
// start. For Claude Code the list is empty: --setting-sources project
// ignores the user-level files (measured-constraints.md row 6e), and auto
// memory is off through the environment (row 86).
func (s *Service) HostWarnings() []string {
	roles := make([]config.Role, 0, len(s.Roles))
	for role := range s.Roles {
		roles = append(roles, role)
	}
	sort.Slice(roles, func(i, j int) bool { return roles[i] < roles[j] })
	var warnings []string
	for _, role := range roles {
		cli := s.Roles[role].CLI
		for _, file := range globalInstructionFiles(cli) {
			if _, err := os.Stat(file); err == nil {
				warnings = append(warnings, fmt.Sprintf("role %s: the CLI %s reads %s and cannot ignore it", role, cli, file))
			}
		}
	}
	return warnings
}

// globalInstructionFiles names the user-level instruction files that a
// CLI cannot be told to ignore. A CLI that is added later lists its files
// here.
func globalInstructionFiles(cli string) []string {
	switch cli {
	case config.CLIClaudeCode:
		return nil
	}
	return nil
}

func (s *Service) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// now returns the time of the clock of the Service.
func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// skillsDir is the directory of skills that a run of the role passes with
// --add-dir, or an empty string when no skills were written.
func (s *Service) skillsDir(role config.Role) string {
	if s.SkillsDir == "" {
		return ""
	}
	return SkillDir(s.SkillsDir, role)
}
