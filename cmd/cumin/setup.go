package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"

	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/keychain"
	"github.com/cloveclovedev/cumin-works/internal/setup"
)

// runSetup is `cumin setup`. It has one subcommand: github-apps.
func runSetup(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "github-apps" {
		fmt.Fprintln(stderr, "usage: cumin setup github-apps --org <organization> [--name-prefix <prefix>]")
		return exitBadUsage
	}

	fs := flag.NewFlagSet("cumin setup github-apps", flag.ContinueOnError)
	fs.SetOutput(stderr)
	org := fs.String("org", "", "the organization that owns the GitHub Apps and the repositories (required)")
	prefix := fs.String("name-prefix", "", "text before each App name. App names are unique on all of GitHub")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitBadUsage
	}
	if *org == "" || fs.NArg() > 0 {
		fmt.Fprintln(stderr, "usage: cumin setup github-apps --org <organization> [--name-prefix <prefix>]")
		return exitBadUsage
	}
	// Check the names before anything else, so that a wrong name opens no page.
	if err := setup.CheckNames(*org, *prefix); err != nil {
		fmt.Fprintf(stderr, "cumin setup github-apps: %v\n", err)
		return exitFailure
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	secrets, err := keychain.Default(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "cumin setup github-apps: %v\n", err)
		return exitFailure
	}
	service := &setup.Service{
		GitHubURL:   setup.DefaultGitHubURL,
		Converter:   github.NewAppClient(github.DefaultBaseURL, nil),
		Secrets:     secrets,
		OpenBrowser: openBrowser,
		Out:         stdout,
	}
	if _, err := service.RegisterApps(ctx, *org, *prefix); err != nil {
		fmt.Fprintf(stderr, "cumin setup github-apps: %v\n", err)
		return exitFailure
	}
	return exitOK
}

// openBrowser opens the address in the default browser of macOS.
func openBrowser(url string) error {
	if runtime.GOOS != "darwin" {
		return errors.New("cumin opens a browser on macOS only")
	}
	return exec.Command("/usr/bin/open", url).Run()
}
