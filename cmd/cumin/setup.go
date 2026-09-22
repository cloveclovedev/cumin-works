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
	"path/filepath"
	"runtime"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/keychain"
	"github.com/cloveclovedev/cumin-works/internal/setup"
)

// runSetup is `cumin setup`. It has two subcommands: github-apps and
// launchd.
func runSetup(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, setupUsage)
		return exitBadUsage
	}
	switch args[0] {
	case "github-apps":
		return runSetupGitHubApps(args, stdout, stderr)
	case "launchd":
		return runSetupLaunchd(args[1:], stdout, stderr)
	}
	fmt.Fprintln(stderr, setupUsage)
	return exitBadUsage
}

const setupUsage = `usage: cumin setup github-apps --org <organization> [--name-prefix <prefix>] [--config <path>]
       cumin setup launchd [--config <path>] [--dry-run] [--force]`

// runSetupGitHubApps registers the GitHub App of each role.
func runSetupGitHubApps(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cumin setup github-apps", flag.ContinueOnError)
	fs.SetOutput(stderr)
	org := fs.String("org", "", "the organization that owns the GitHub Apps and the repositories (required)")
	prefix := fs.String("name-prefix", "", "text before each App name. App names are unique on all of GitHub")
	configPath := fs.String("config", "", "path of the Host settings file (default ~/.config/cumin/config.toml)")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitBadUsage
	}
	if *org == "" || fs.NArg() > 0 {
		fmt.Fprintln(stderr, setupUsage)
		return exitBadUsage
	}
	// Check the names before anything else, so that a wrong name opens no page.
	if err := setup.CheckNames(*org, *prefix); err != nil {
		fmt.Fprintf(stderr, "cumin setup github-apps: %v\n", err)
		return exitFailure
	}

	path := *configPath
	if path == "" {
		var err error
		if path, err = config.DefaultPath(); err != nil {
			fmt.Fprintf(stderr, "cumin setup github-apps: %v\n", err)
			return exitFailure
		}
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
		GitHub:      github.NewAppClient(github.DefaultBaseURL, nil),
		Secrets:     secrets,
		ConfigPath:  path,
		OpenBrowser: openBrowser,
		Out:         stdout,
	}
	if err := service.Run(ctx, *org, *prefix); err != nil {
		fmt.Fprintf(stderr, "cumin setup github-apps: %v\n", err)
		return exitFailure
	}
	return exitOK
}

// runSetupLaunchd writes the LaunchAgent of the current user, so that
// launchd starts `cumin run` at login and starts it again when it ends
// with an error. Every value comes from this process: the path of the
// running binary, the settings file, the home directory, and PATH.
func runSetupLaunchd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cumin setup launchd", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path of the Host settings file (default ~/.config/cumin/config.toml)")
	dryRun := fs.Bool("dry-run", false, "print the plist and write nothing")
	force := fs.Bool("force", false, "replace a plist of this job that holds different contents")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitBadUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(stderr, setupUsage)
		return exitBadUsage
	}
	if runtime.GOOS != "darwin" {
		fmt.Fprintln(stderr, "cumin setup launchd: launchd is macOS only")
		return exitFailure
	}

	path := *configPath
	if path == "" {
		var err error
		if path, err = config.DefaultPath(); err != nil {
			fmt.Fprintf(stderr, "cumin setup launchd: %v\n", err)
			return exitFailure
		}
	}
	// launchd gives the job no working directory, so the plist must hold an
	// absolute path even when the Owner typed a relative one.
	absolute, err := filepath.Abs(path)
	if err != nil {
		fmt.Fprintf(stderr, "cumin setup launchd: %v\n", err)
		return exitFailure
	}
	path = absolute
	program, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "cumin setup launchd: find the path of cumin: %v\n", err)
		return exitFailure
	}
	if resolved, err := filepath.EvalSymlinks(program); err == nil {
		program = resolved
	}
	if err := setup.CheckProgram(program); err != nil {
		fmt.Fprintf(stderr, "cumin setup launchd: %v\n", err)
		return exitFailure
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(stderr, "cumin setup launchd: find the home directory: %v\n", err)
		return exitFailure
	}

	agent := setup.NewLaunchAgent(home, program, path, os.Getenv("PATH"))
	if *dryRun {
		plist, err := agent.Plist()
		if err != nil {
			fmt.Fprintf(stderr, "cumin setup launchd: %v\n", err)
			return exitFailure
		}
		fmt.Fprintf(stdout, "%s would hold:\n\n%s", agent.PlistPath, plist)
		return exitOK
	}

	result, err := setup.InstallLaunchAgent(agent, *force)
	if err != nil {
		fmt.Fprintf(stderr, "cumin setup launchd: %v\n", err)
		return exitFailure
	}
	out, errorLog := agent.LogPaths()
	fmt.Fprintf(stdout, "%s: %s\n", result, agent.PlistPath)
	fmt.Fprintf(stdout, "logs: %s and %s\n\n", out, errorLog)
	commands := agent.LaunchctlCommands(os.Getuid())
	for _, line := range []struct{ what, command string }{
		{"start it now, and at every login", commands[0]},
		{"stop it", commands[1]},
		{"restart it", commands[2]},
		{"remove it", commands[3]},
	} {
		fmt.Fprintf(stdout, "%-33s %s\n", line.what+":", line.command)
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
