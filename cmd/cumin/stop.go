package main

// This file is `cumin stop --after-current-runs`: the Owner asks the
// running cumin to start no new work, to let the agent runs that are going
// on end, and then to exit. The command writes the stop request file and
// returns; cumin run reads the file at its next poll and removes it when it
// exits (docs/ja/designs/cumin-core.md, the topic on the stop). Nothing
// leaves the Host.

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/core/state"
)

// stopRequestFile is the path of the stop request file in the state directory.
func stopRequestFile() (string, error) {
	dir, err := config.DefaultStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, state.StopRequestFileName), nil
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

	path, err := stopRequestFile()
	if err != nil {
		fmt.Fprintf(stderr, "cumin stop: %v\n", err)
		return exitFailure
	}
	// A request that is already there gets the time of now. A cumin run
	// that already stops after its runs goes on. A request that an earlier process left
	// behind becomes one of now, so the start of cumin run that removes
	// the requests of before it does not take this one for an old one.
	if err := state.WriteStopRequest(path, state.StopRequest{RequestedAt: now()}); err != nil {
		fmt.Fprintf(stderr, "cumin stop: %v\n", err)
		return exitFailure
	}
	fmt.Fprintln(stdout, "Requested. From its next poll, cumin run starts no new work; it exits when the agent runs that are going on have ended.")
	fmt.Fprintln(stdout, "Watch it with: cumin status")
	fmt.Fprintln(stdout, "A cumin run that starts later drops the request and runs as usual.")
	return exitOK
}
