package main

// This file is `cumin stop --after-current-runs`: the Owner asks the
// running cumin to start no new work, to let the agent runs that are going
// on end, and then to exit. The command writes the drain request file and
// returns; cumin run reads the file at its next poll and removes it when it
// exits (docs/ja/designs/cumin-core.md, the topic on the stop). Nothing
// leaves the Host.

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

// drainFile is the path of the drain request file in the state directory.
func drainFile() (string, error) {
	dir, err := config.DefaultStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, state.DrainFileName), nil
}

const stopUsage = "usage: cumin stop --after-current-runs"

func runStop(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cumin stop", flag.ContinueOnError)
	fs.SetOutput(stderr)
	afterRuns := fs.Bool("after-current-runs", false, "start no new work, let the agent runs that are going on end, then exit")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitBadUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "cumin stop: unexpected argument %q\n%s\n", fs.Arg(0), stopUsage)
		return exitBadUsage
	}
	// The flag is required, so that the command says what it does. A stop
	// at once is SIGTERM, which this command does not send.
	if !*afterRuns {
		fmt.Fprintf(stderr, "cumin stop: --after-current-runs is required. To stop at once, send SIGTERM to cumin run (docs/ja/development/setup-guide.md)\n%s\n", stopUsage)
		return exitBadUsage
	}

	path, err := drainFile()
	if err != nil {
		fmt.Fprintf(stderr, "cumin stop: %v\n", err)
		return exitFailure
	}
	// A request that is already there keeps its time: cumin run may have
	// read it, and cumin status shows when the drain began.
	if request, found, err := state.ReadDrain(path); err == nil && found {
		fmt.Fprintf(stdout, "A drain was already requested at %s. Nothing was changed.\n", stamp(request.RequestedAt, time.Local))
		fmt.Fprintln(stdout, "Watch it with: cumin status")
		return exitOK
	}
	if err := state.WriteDrain(path, state.Drain{RequestedAt: now()}); err != nil {
		fmt.Fprintf(stderr, "cumin stop: %v\n", err)
		return exitFailure
	}
	fmt.Fprintln(stdout, "Requested. From its next poll, cumin run starts no new work; it exits when the agent runs that are going on have ended.")
	fmt.Fprintln(stdout, "Watch it with: cumin status")
	fmt.Fprintln(stdout, "A cumin run that starts later drops the request and runs as usual.")
	return exitOK
}
