// Command cumin is the workflow engine of cumin-works.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// Exit codes.
const (
	exitOK       = 0
	exitFailure  = 1
	exitBadUsage = 2
)

// command is one subcommand that the Owner uses. The list comes from
// docs/ja/requirements/cumin-core.md.
type command struct {
	name    string // may have two words, such as "quota allow"
	summary string
}

var commands = []command{
	{"run", "Run as a resident program. launchd starts this command."},
	{"status", "Show running agents, issues that wait for the Owner, and the quota usage."},
	{"quota allow", "Allow cumin to use all of the current 5h quota window."},
	{"setup", "Set up cumin on the Host. \"setup github-apps\" registers the GitHub App of each role."},
}

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr))
}

// runCLI runs cumin with the given arguments and returns the exit code.
// Tests call it without starting a process.
func runCLI(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cumin", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // runCLI prints the usage itself
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(stdout)
			return exitOK
		}
		fmt.Fprintf(stderr, "cumin: %v\n\n", err)
		printUsage(stderr)
		return exitBadUsage
	}

	rest := fs.Args()
	if len(rest) == 0 {
		printUsage(stderr)
		return exitBadUsage
	}

	cmd, ok := findCommand(rest)
	if !ok {
		fmt.Fprintf(stderr, "cumin: unknown command %q\n\n", strings.Join(rest, " "))
		printUsage(stderr)
		return exitBadUsage
	}

	// No subcommand is built yet. Each one replaces this message when it is built.
	fmt.Fprintf(stderr, "cumin %s: not built yet\n", cmd.name)
	return exitFailure
}

// findCommand matches the start of args against the command names.
func findCommand(args []string) (command, bool) {
	for _, c := range commands {
		words := strings.Fields(c.name)
		if len(args) < len(words) {
			continue
		}
		if strings.Join(args[:len(words)], " ") == c.name {
			return c, true
		}
	}
	return command{}, false
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "cumin is the workflow engine of cumin-works.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  cumin <command>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	for _, c := range commands {
		fmt.Fprintf(w, "  %-12s %s\n", c.name, c.summary)
	}
}
