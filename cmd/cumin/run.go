package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/cloveclovedev/cumin-works/internal/agent"
	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/keychain"
	"github.com/cloveclovedev/cumin-works/internal/workflow"
	"github.com/cloveclovedev/cumin-works/roles"
)

// runRun is `cumin run`: the resident program. It loads the Host settings,
// reads the private key of every GitHub App of each target repository owner
// from the Keychain, creates the missing labels, and polls until SIGINT or
// SIGTERM. launchd starts and restarts it.
func runRun(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cumin run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path of the Host settings file (default ~/.config/cumin/config.toml)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitBadUsage
	}
	// The flag package stops at the first argument that is not a flag. An
	// extra argument is a mistake; do not start a resident process on it.
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "cumin run: unexpected argument %q\nusage: cumin run [--config <path>]\n", fs.Arg(0))
		return exitBadUsage
	}

	path := *configPath
	if path == "" {
		var err error
		if path, err = config.DefaultPath(); err != nil {
			fmt.Fprintf(stderr, "cumin run: %v\n", err)
			return exitFailure
		}
	}
	settings, err := config.Load(path)
	if err != nil {
		fmt.Fprintf(stderr, "cumin run: %v\n", err)
		return exitFailure
	}
	// Every owner needs a Client ID for every App before anything touches
	// the Keychain or GitHub, so that a settings problem stops the run with
	// the key name.
	clientIDs, err := appClientIDs(settings)
	if err != nil {
		fmt.Fprintf(stderr, "cumin run: %v\n", err)
		return exitFailure
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	credentials, err := readCredentials(ctx, clientIDs)
	if err != nil {
		fmt.Fprintf(stderr, "cumin run: %v\n", err)
		return exitFailure
	}

	// Logs are JSON on stdout (cumin-core.md, "How cumin runs").
	logger := slog.New(slog.NewJSONHandler(stdout, nil))

	// The skills of the agents, written once so that they match this
	// binary. The agent start passes the directory with --add-dir.
	skillsDir, err := writeSkills()
	if err != nil {
		fmt.Fprintf(stderr, "cumin run: %v\n", err)
		return exitFailure
	}
	logger.Info("skills written", "dir", skillsDir)
	client := github.NewAppClient(github.DefaultBaseURL, nil)
	agents := &agent.Service{
		Roles:     settings.Roles,
		Apps:      roleCredentials(credentials),
		GitHub:    client,
		SkillsDir: skillsDir,
		Logger:    logger,
	}
	for _, warning := range agents.HostWarnings() {
		logger.Warn("global instruction file on the Host", "warning", warning)
	}
	service := &workflow.Service{
		GitHub:       client,
		Agents:       agents,
		Workspace:    agent.Workspace{Root: settings.WorkDir, Logger: logger},
		Settings:     settings,
		SettingsDir:  filepath.Dir(path),
		PollInterval: settings.PollInterval,
		Labels:       workflow.RepositoryLabels(),
		Logger:       logger,
	}
	var names []string
	for _, repo := range settings.Repositories {
		owner := strings.ToLower(repo.Owner)
		source := github.NewTokenSource(client, credentials[owner][config.AppCuminCore], config.AppCuminCore, repo.Owner, repo.Name)
		service.Targets = append(service.Targets, workflow.Target{
			Repository: repo,
			RemoteURL:  remoteURL(repo),
			Token:      source.Token,
		})
		names = append(names, repo.String())
	}
	logger.Info("cumin run starts", "repositories", names, "poll_interval", settings.PollInterval.String(),
		"work_dir", settings.WorkDir)
	if err := service.Run(ctx); err != nil {
		fmt.Fprintf(stderr, "cumin run: %v\n", err)
		return exitFailure
	}
	return exitOK
}

// remoteURL is the address that the clone of a target repository uses. v0.1
// clones over HTTPS without credentials, so only public repositories are
// supported (agent-run.md, the section on deferred work).
func remoteURL(repo config.Repository) string {
	return "https://github.com/" + repo.Owner + "/" + repo.Name + ".git"
}

// writeSkills writes the skills of the agents under the state directory
// and returns the directory to pass with --add-dir.
func writeSkills() (string, error) {
	state, err := config.DefaultStateDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(state, "skills")
	if err := roles.WriteSkills(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// appClientIDs returns the Client ID of every GitHub App of cumin
// (config.AllApps: cumin-core and the three roles) for the owner of each
// target repository, keyed by the owner in lower case and then by the App.
// GitHub account names ignore case, and so does the lookup in github_apps.
func appClientIDs(settings *config.Settings) (map[string]map[string]string, error) {
	ids := map[string]map[string]string{}
	var errs []error
	for _, repo := range settings.Repositories {
		owner := strings.ToLower(repo.Owner)
		if _, done := ids[owner]; done {
			continue
		}
		var apps map[string]string
		var matches []string
		for org, table := range settings.GitHubApps {
			if strings.EqualFold(org, repo.Owner) {
				matches = append(matches, org)
				apps = table
			}
		}
		if len(matches) > 1 {
			slices.Sort(matches)
			errs = append(errs, fmt.Errorf("github_apps: the tables %s name the same organization in different cases. Keep one", strings.Join(matches, " and ")))
			continue
		}
		found := map[string]string{}
		for _, app := range config.AllApps() {
			clientID := apps[app]
			if clientID == "" {
				errs = append(errs, fmt.Errorf("github_apps.%s.%s: no Client ID for the owner of %s. Run \"cumin setup github-apps --org %s\" first", repo.Owner, app, repo, repo.Owner))
				continue
			}
			found[app] = clientID
		}
		if len(found) == len(config.AllApps()) {
			ids[owner] = found
		}
	}
	return ids, errors.Join(errs...)
}

// readCredentials reads the private key of each App from the Keychain. The
// key stays in memory; it never reaches a log or an error.
func readCredentials(ctx context.Context, clientIDs map[string]map[string]string) (map[string]map[string]github.AppCredentials, error) {
	store, err := keychain.Default(ctx)
	if err != nil {
		return nil, err
	}
	credentials := map[string]map[string]github.AppCredentials{}
	for _, owner := range slices.Sorted(maps.Keys(clientIDs)) {
		credentials[owner] = map[string]github.AppCredentials{}
		for _, app := range config.AllApps() {
			clientID := clientIDs[owner][app]
			pemBytes, err := store.GetBase64(ctx, keychain.Service, keychain.PrivateKeyAccount(clientID))
			if err != nil {
				return nil, fmt.Errorf("read the private key of the %s App of %s from the Keychain: %w", app, owner, err)
			}
			key, err := github.ParsePrivateKey(pemBytes)
			if err != nil {
				return nil, fmt.Errorf("the private key of the %s App of %s: %w", app, owner, err)
			}
			credentials[owner][app] = github.AppCredentials{ClientID: clientID, PrivateKey: key}
		}
	}
	return credentials, nil
}

// roleCredentials keeps only the Apps of the agent roles, in the shape that
// agent.Service takes. The App of cumin-core is not an agent.
func roleCredentials(credentials map[string]map[string]github.AppCredentials) map[string]map[config.Role]github.AppCredentials {
	apps := map[string]map[config.Role]github.AppCredentials{}
	for owner, table := range credentials {
		byRole := map[config.Role]github.AppCredentials{}
		for _, role := range config.AllRoles() {
			byRole[role] = table[string(role)]
		}
		apps[owner] = byRole
	}
	return apps
}
