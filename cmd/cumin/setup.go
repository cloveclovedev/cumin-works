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
	"strings"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/keychain"
	"github.com/cloveclovedev/cumin-works/internal/setup"
)

// launchctlPath is the tool that loads and unloads a job on macOS.
const launchctlPath = "/bin/launchctl"

// runSetup is `cumin setup`. It has three subcommands: github-apps,
// launchd, and notify.
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
	case "notify":
		return runSetupNotify(args[1:], stdout, stderr)
	}
	fmt.Fprintln(stderr, setupUsage)
	return exitBadUsage
}

const setupUsage = `usage: cumin setup github-apps --org <organization> [--name-prefix <prefix>] [--config <path>]
       cumin setup launchd [--config <path>] [--dry-run] [--force]
       cumin setup launchd --remove [--dry-run] [--force]
       cumin setup notify --discord-webhook`

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

// runSetupNotify is `cumin setup notify`: it puts the address of one
// notification channel into the Keychain. The channel is a flag, because
// the Discord webhook is one way of notifying and others may follow.
//
// The address is read from standard input, so that it stays out of the
// process list and out of the shell history. On a terminal the command
// asks for it; with a pipe it reads one line, so that a script can give
// it.
func runSetupNotify(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cumin setup notify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	chosen := map[string]*bool{}
	for _, channel := range setup.Channels() {
		chosen[channel.Flag] = fs.Bool(channel.Flag, false, "store the "+channel.What)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitBadUsage
	}
	var channels []setup.Channel
	for _, channel := range setup.Channels() {
		if *chosen[channel.Flag] {
			channels = append(channels, channel)
		}
	}
	// One channel for each run: the command reads one address, so two
	// flags would ask for one address and store it twice.
	if len(channels) != 1 || fs.NArg() > 0 {
		fmt.Fprintln(stderr, setupUsage)
		return exitBadUsage
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	secrets, err := keychain.Default(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "cumin setup notify: %v\n", err)
		return exitFailure
	}
	address, err := setup.ReadAddress(os.Stdin, stdout, channels[0], isTerminal(os.Stdin))
	if err != nil {
		fmt.Fprintf(stderr, "cumin setup notify: %v\n", err)
		return exitFailure
	}
	if err := setup.StoreNotifyAddress(ctx, secrets, channels[0], address, stdout); err != nil {
		fmt.Fprintf(stderr, "cumin setup notify: %v\n", err)
		return exitFailure
	}
	return exitOK
}

// isTerminal reports whether the file is a terminal, so that the command
// asks for the address only when a person is there to paste it.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// runSetupLaunchd writes the LaunchAgent of the current user, so that
// launchd starts `cumin run` at login and starts it again when it ends
// with an error. Every value comes from this process: the path of the
// running binary, the settings file, the home directory, and PATH.
func runSetupLaunchd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cumin setup launchd", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path of the Host settings file (default ~/.config/cumin/config.toml)")
	dryRun := fs.Bool("dry-run", false, "print what would happen and change nothing")
	force := fs.Bool("force", false, "act on a plist of this path that does not hold this job")
	remove := fs.Bool("remove", false, "stop the job and remove its plist")
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
	// The settings file has no part in removing the job.
	if *remove && *configPath != "" {
		fmt.Fprintln(stderr, "cumin setup launchd: --remove takes no --config")
		fmt.Fprintln(stderr, setupUsage)
		return exitBadUsage
	}
	if runtime.GOOS != "darwin" {
		fmt.Fprintln(stderr, "cumin setup launchd: launchd is macOS only")
		return exitFailure
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(stderr, "cumin setup launchd: find the home directory: %v\n", err)
		return exitFailure
	}
	if *remove {
		return removeLaunchd(setup.NewLaunchAgent(home, "", "", ""), *dryRun, *force, stdout, stderr)
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
	// The plist keeps the path as it is. A symbolic link such as
	// /usr/local/bin/cumin is the stable name: after an upgrade it points at
	// the new binary, while the path it resolves to today may be gone. The
	// resolved path is checked as well, so that a link into a temporary
	// build is still refused.
	if err := setup.CheckProgram(program); err != nil {
		fmt.Fprintf(stderr, "cumin setup launchd: %v\n", err)
		return exitFailure
	}
	if resolved, err := filepath.EvalSymlinks(program); err == nil {
		if err := setup.CheckProgram(resolved); err != nil {
			fmt.Fprintf(stderr, "cumin setup launchd: %v\n", err)
			return exitFailure
		}
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

// removeLaunchd is `cumin setup launchd --remove`: it stops the job when
// it is loaded and removes its plist. Nothing else goes: the logs, the
// state, and the binary stay, and the command says where they are.
func removeLaunchd(agent setup.LaunchAgent, dryRun, force bool, stdout, stderr io.Writer) int {
	target := agent.ServiceTarget(os.Getuid())
	loaded := exec.Command(launchctlPath, "print", target).Run() == nil

	if dryRun {
		if loaded {
			fmt.Fprintf(stdout, "would stop the job: launchctl bootout %s\n", target)
		} else {
			fmt.Fprintf(stdout, "the job %s is not loaded\n", target)
		}
		// The same check as the removal, so that the preview is right in
		// the case that the check is there for. With --force the file is
		// not read, there as here.
		state := setup.PlistOfThisJob
		if !force {
			var err error
			if state, err = agent.PlistState(); err != nil {
				fmt.Fprintf(stderr, "cumin setup launchd: %v\n", err)
				return exitFailure
			}
		} else if _, err := os.Stat(agent.PlistPath); os.IsNotExist(err) {
			state = setup.PlistAbsent
		}
		switch state {
		case setup.PlistAbsent:
			fmt.Fprintf(stdout, "no plist at: %s\n", agent.PlistPath)
		case setup.PlistOfAnotherJob:
			fmt.Fprintf(stdout, "would keep: %s holds another job. Run again with --force to remove it anyway\n", agent.PlistPath)
		default:
			fmt.Fprintf(stdout, "would remove: %s\n", agent.PlistPath)
		}
		printWhatStays(stdout, agent)
		return exitOK
	}

	if loaded {
		if out, err := exec.Command(launchctlPath, "bootout", target).CombinedOutput(); err != nil {
			fmt.Fprintf(stderr, "cumin setup launchd: stop the job %s: %v %s\n", target, err, strings.TrimSpace(string(out)))
			return exitFailure
		}
		fmt.Fprintf(stdout, "stopped: %s\n", target)
	} else {
		fmt.Fprintf(stdout, "the job %s was not loaded\n", target)
	}

	result, err := setup.RemoveLaunchAgentFile(agent, force)
	if err != nil {
		fmt.Fprintf(stderr, "cumin setup launchd: %v\n", err)
		return exitFailure
	}
	fmt.Fprintf(stdout, "%s: %s\n", result, agent.PlistPath)
	printWhatStays(stdout, agent)
	return exitOK
}

// printWhatStays names the files that the removal leaves on the Host.
func printWhatStays(w io.Writer, agent setup.LaunchAgent) {
	out, errorLog := agent.LogPaths()
	fmt.Fprintf(w, "\nthese stay. Remove them by hand when you want them gone:\n")
	fmt.Fprintf(w, "  logs and state:  %s (%s, %s)\n", agent.StateDir, filepath.Base(out), filepath.Base(errorLog))
	fmt.Fprintf(w, "  the binary:      the cumin that you installed, for example ~/.local/bin/cumin\n")
	fmt.Fprintf(w, "  the settings:    ~/.config/cumin/, the work directory, and the Keychain items\n")
}

// openBrowser opens the address in the default browser of macOS.
func openBrowser(url string) error {
	if runtime.GOOS != "darwin" {
		return errors.New("cumin opens a browser on macOS only")
	}
	return exec.Command("/usr/bin/open", url).Run()
}
