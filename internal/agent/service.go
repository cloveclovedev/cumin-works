package agent

// This file puts the pieces of one agent start in order: read the quota
// usage, create a fresh token of the role limited to the repository, read
// the bot identity of the role, then run the CLI in the work directory.
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
	// SkillsDir is the directory whose .claude/skills/ holds the skills
	// that cumin run wrote at start (roles.WriteSkills). Every run gets it.
	SkillsDir string
	// Logger may be nil. Then the default logger is used.
	Logger *slog.Logger
	// Grace and QuotaTimeLimit are passed to the CLI adapter. Zero means
	// its defaults. Tests shorten them.
	Grace          time.Duration
	QuotaTimeLimit time.Duration

	mu         sync.Mutex
	identities map[appKey]identity
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
	// RoleInstruction is the instruction of the role, from roles/.
	RoleInstruction string
	// Text is the request text: what the agent must do this time.
	Text string
	// WorkDir is the worktree that the agent runs in.
	WorkDir string
	// SessionID continues an earlier session. Empty starts a new session.
	SessionID string
}

// Start runs one request. The steps, in order: the quota usage is read
// with a minimal run (an error of kind *QuotaNotRead stops the start);
// a token of the App of the role is created, limited to the repository;
// the bot identity of the role is read, once; then the CLI runs with the
// token and the identity. The token lives only in the request of the run.
// An error from the run is an *AbnormalEnd.
func (s *Service) Start(ctx context.Context, req StartRequest) (*Run, error) {
	settings, ok := s.Roles[req.Role]
	if !ok {
		return nil, fmt.Errorf("start %s on %s/%s: no settings for the role", req.Role, req.Owner, req.Repo)
	}
	cred, err := s.app(req.Owner, req.Role)
	if err != nil {
		return nil, fmt.Errorf("start %s on %s/%s: %w", req.Role, req.Owner, req.Repo, err)
	}
	log := s.logger().With("role", req.Role, "repository", req.Owner+"/"+req.Repo)
	cli := ClaudeCode{Path: settings.CLIPath, Logger: log, Grace: s.Grace, QuotaTimeLimit: s.QuotaTimeLimit}

	// 1. The quota usage. The decision on thresholds (Q1) is a later
	// requirement; here an unreadable usage stops the start.
	if _, err := cli.ReadQuota(ctx); err != nil {
		return nil, fmt.Errorf("start %s on %s/%s: %w", req.Role, req.Owner, req.Repo, err)
	}

	// 2. A fresh token for this request. A run lasts up to 55 minutes and
	// a token lives one hour, so no token is reused.
	token, err := s.GitHub.CreateInstallationToken(ctx, cred, string(req.Role), req.Owner, req.Repo)
	if err != nil {
		return nil, fmt.Errorf("start %s on %s/%s: %w", req.Role, req.Owner, req.Repo, err)
	}
	log.Info("agent token created", "expires_at", token.ExpiresAt)

	// 3. The identity of the commits of the agent.
	id, err := s.identity(ctx, appKey{strings.ToLower(req.Owner), req.Role}, cred, token.Token)
	if err != nil {
		return nil, fmt.Errorf("start %s on %s/%s: %w", req.Role, req.Owner, req.Repo, err)
	}

	// 4. The run.
	return cli.Run(ctx, Request{
		Role:            req.Role,
		RoleInstruction: req.RoleInstruction,
		Text:            req.Text,
		WorkDir:         req.WorkDir,
		SessionID:       req.SessionID,
		SkillsDir:       s.SkillsDir,
		Model:           settings.Model,
		TimeLimit:       settings.TimeLimit,
		Credentials:     Credentials{Token: token.Token, AuthorName: id.name, AuthorEmail: id.email},
	})
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

// HostWarnings returns one warning for each global instruction file that
// the CLI of a role reads and cannot ignore. cumin run logs them at its
// start. For Claude Code the list is empty: --setting-sources project
// ignores the user-level files (measured-constraints.md row 6e), and auto
// memory is off through the environment (row 28).
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
