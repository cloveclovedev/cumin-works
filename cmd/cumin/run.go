package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/keychain"
	"github.com/cloveclovedev/cumin-works/internal/workflow"
	"github.com/cloveclovedev/cumin-works/roles"
)

// runRun is `cumin run`: the resident program. It loads the Host settings,
// reads the private key of the cumin-core App of each target repository
// owner from the Keychain, creates the missing labels, and polls until
// SIGINT or SIGTERM. launchd starts and restarts it.
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
	// Every owner needs a Client ID before anything touches the Keychain or
	// GitHub, so that a settings problem stops the run with the key name.
	clientIDs, err := cuminCoreClientIDs(settings)
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
	service := &workflow.Service{
		GitHub:              client,
		MaxIssuesInProgress: settings.MaxIssuesInProgress,
		RequestCommand:      settings.RequestCommand,
		PollInterval:        settings.PollInterval,
		Labels:              workflow.RepositoryLabels(),
		Logger:              logger,
	}
	var names []string
	for _, repo := range settings.Repositories {
		source := github.NewTokenSource(client, credentials[strings.ToLower(repo.Owner)], config.AppCuminCore, repo.Owner, repo.Name)
		service.Targets = append(service.Targets, workflow.Target{Repository: repo, Token: source.Token})
		names = append(names, repo.String())
	}
	logger.Info("cumin run starts", "repositories", names, "poll_interval", settings.PollInterval.String())
	if err := service.Run(ctx); err != nil {
		fmt.Fprintf(stderr, "cumin run: %v\n", err)
		return exitFailure
	}
	return exitOK
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

// cuminCoreClientIDs returns the Client ID of the cumin-core App for the
// owner of each target repository, keyed by the owner in lower case. GitHub
// account names ignore case, and so does the lookup in github_apps.
func cuminCoreClientIDs(settings *config.Settings) (map[string]string, error) {
	ids := map[string]string{}
	var errs []error
	for _, repo := range settings.Repositories {
		owner := strings.ToLower(repo.Owner)
		if _, done := ids[owner]; done {
			continue
		}
		var clientID string
		var matches []string
		for org, apps := range settings.GitHubApps {
			if strings.EqualFold(org, repo.Owner) {
				matches = append(matches, org)
				clientID = apps[config.AppCuminCore]
			}
		}
		if len(matches) > 1 {
			slices.Sort(matches)
			errs = append(errs, fmt.Errorf("github_apps: the tables %s name the same organization in different cases. Keep one", strings.Join(matches, " and ")))
			continue
		}
		if clientID == "" {
			errs = append(errs, fmt.Errorf("github_apps.%s.%s: no Client ID for the owner of %s. Run \"cumin setup github-apps --org %s\" first", repo.Owner, config.AppCuminCore, repo, repo.Owner))
			continue
		}
		ids[owner] = clientID
	}
	return ids, errors.Join(errs...)
}

// readCredentials reads the private key of each App from the Keychain. The
// key stays in memory; it never reaches a log or an error.
func readCredentials(ctx context.Context, clientIDs map[string]string) (map[string]github.AppCredentials, error) {
	store, err := keychain.Default(ctx)
	if err != nil {
		return nil, err
	}
	credentials := map[string]github.AppCredentials{}
	for owner, clientID := range clientIDs {
		pemBytes, err := store.GetBase64(ctx, keychain.Service, keychain.PrivateKeyAccount(clientID))
		if err != nil {
			return nil, fmt.Errorf("read the private key of the %s App of %s from the Keychain: %w", config.AppCuminCore, owner, err)
		}
		key, err := github.ParsePrivateKey(pemBytes)
		if err != nil {
			return nil, fmt.Errorf("the private key of the %s App of %s: %w", config.AppCuminCore, owner, err)
		}
		credentials[owner] = github.AppCredentials{ClientID: clientID, PrivateKey: key}
	}
	return credentials, nil
}
