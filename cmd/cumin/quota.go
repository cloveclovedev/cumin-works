package main

// This file is `cumin quota allow` ("resume agent starts" of
// issue-states.md): the Operator lets cumin use the rest of the current 5h
// window. The command reads the
// latest usage that cumin run kept and writes the reset time of that 5h
// window to the allowance file. It writes nothing else, so each file of the
// Host keeps one writer (docs/ja/designs/quota.md, the topic on the
// allowance).

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/core/state"
)

// allowanceFile is the path of the allowance file in the state directory.
func allowanceFile() (string, error) {
	dir, err := config.DefaultStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, state.AllowanceFileName), nil
}

// now is the clock of the command. Tests replace it.
var now = time.Now

func runQuotaAllow(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cumin quota allow", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitBadUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "cumin quota allow: unexpected argument %q\nusage: cumin quota allow\n", fs.Arg(0))
		return exitBadUsage
	}

	dir, err := config.DefaultStateDir()
	if err != nil {
		fmt.Fprintf(stderr, "cumin quota allow: %v\n", err)
		return exitFailure
	}
	// Open only reads; cumin run stays the only writer of the state file.
	usage, ok := state.Open(filepath.Join(dir, stateFileName), nil).Quota()
	current := now()
	switch {
	case !ok:
		fmt.Fprintln(stderr, "cumin quota allow: cumin has not read the quota usage yet, so the current 5h window is not known. Nothing was allowed. Try again after cumin run has read the usage.")
		return exitFailure
	case !current.Before(usage.FiveHour.ResetsAt):
		fmt.Fprintf(stderr, "cumin quota allow: the last 5h window that cumin read reset at %s, so the current one is not known. Nothing was allowed. Try again after cumin run has read the usage.\n",
			usage.FiveHour.ResetsAt.Local().Format(time.DateTime))
		return exitFailure
	}
	path := filepath.Join(dir, state.AllowanceFileName)
	if err := state.WriteAllowance(path, state.Allowance{FiveHourUntil: usage.FiveHour.ResetsAt}); err != nil {
		fmt.Fprintf(stderr, "cumin quota allow: %v\n", err)
		return exitFailure
	}
	fmt.Fprintf(stdout, "cumin may use all of the current 5h window until it resets at %s. The weekly pace limit still applies.\n",
		usage.FiveHour.ResetsAt.Local().Format(time.DateTime))
	return exitOK
}
