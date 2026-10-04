package main

// This file is `cumin status` and `cumin --version`. `cumin status` shows
// the Owner what cumin does now: the issues with an agent at work and the
// issues that wait for the Owner, read from the labels on GitHub, and the
// latest quota usage with the limits of now, and whether cumin stops after its runs. It
// makes no minimal run and writes no file (docs/ja/designs/quota.md, the
// topic on cumin status).
// The usage numbers go to the terminal only, never to a log.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/core/state"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/keychain"
	"github.com/cloveclovedev/cumin-works/internal/quota"
	"github.com/cloveclovedev/cumin-works/internal/workflow"
)

// runStatus is `cumin status`.
func runStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cumin status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path of the Host settings file (default ~/.config/cumin/config.toml)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitBadUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "cumin status: unexpected argument %q\nusage: cumin status [--config <path>]\n", fs.Arg(0))
		return exitBadUsage
	}
	path := *configPath
	if path == "" {
		var err error
		if path, err = config.DefaultPath(); err != nil {
			fmt.Fprintf(stderr, "cumin status: %v\n", err)
			return exitFailure
		}
	}
	settings, err := config.Load(path)
	if err != nil {
		fmt.Fprintf(stderr, "cumin status: %v\n", err)
		return exitFailure
	}
	dir, err := config.DefaultStateDir()
	if err != nil {
		fmt.Fprintf(stderr, "cumin status: %v\n", err)
		return exitFailure
	}
	ctx := context.Background()
	// The labels are read as cumin-core, the identity of cumin run. Only
	// its key is read, for each repository, so that a key that cannot be
	// read costs the lines of its owner only; the quota and the other
	// repositories are still shown.
	store, storeErr := keychain.Default(ctx)
	client := github.NewAppClient(github.DefaultBaseURL, nil)
	read := func(ctx context.Context, repo config.Repository) (github.RepositorySnapshot, error) {
		if storeErr != nil {
			return github.RepositorySnapshot{}, storeErr
		}
		owner := strings.ToLower(repo.Owner)
		clientID, err := coreClientID(settings, repo.Owner)
		if err != nil {
			return github.RepositorySnapshot{}, err
		}
		cred, err := readAppCredential(ctx, store, owner, config.AppCuminCore, clientID)
		if err != nil {
			return github.RepositorySnapshot{}, err
		}
		token, err := github.NewTokenSource(client, cred, config.AppCuminCore, repo.Owner, repo.Name).Token(ctx)
		if err != nil {
			return github.RepositorySnapshot{}, err
		}
		return client.ReadSnapshot(ctx, token, repo.Owner, repo.Name)
	}
	if err := writeStatus(ctx, stdout, settings, dir, now(), time.Local, read); err != nil {
		fmt.Fprintf(stderr, "cumin status: %v\n", err)
		return exitFailure
	}
	return exitOK
}

// coreClientID returns the Client ID of the cumin-core App of an owner.
// cumin status needs no other App, so a missing role App of the settings
// does not stop the report. Owner names ignore case, as in appClientIDs.
func coreClientID(settings *config.Settings, owner string) (string, error) {
	var found []string
	var id string
	for org, table := range settings.GitHubApps {
		if strings.EqualFold(org, owner) {
			found = append(found, org)
			id = table[config.AppCuminCore]
		}
	}
	switch {
	case len(found) > 1:
		slices.Sort(found)
		return "", fmt.Errorf("the settings have github_apps for %s more than once: %s", owner, strings.Join(found, ", "))
	case id == "":
		return "", fmt.Errorf("github_apps.%s.%s: no Client ID", owner, config.AppCuminCore)
	}
	return id, nil
}

// readRepository reads the snapshot of one target repository. cumin status
// reads the labels only, so it sends the poll query and not the second query
// of a poll, which reads the pull requests.
type readRepository func(ctx context.Context, repo config.Repository) (github.RepositorySnapshot, error)

// agentLabels are the status labels of an issue whose agent works: the
// Planner on a requirement issue (planning), the Implementer and the
// Reviewer on an implementation issue. A requirement issue with
// cumin/status/implementing has no agent of its own (R3).
var agentLabels = map[bool][]string{
	true:  {workflow.LabelPlanning},
	false: {workflow.LabelImplementing, workflow.LabelReviewing},
}

var ownerLabels = []string{workflow.LabelAwaitingPlanReview, workflow.LabelAwaitingMergeDecision, workflow.LabelAwaitingAcceptance, workflow.LabelAwaitingDecision}

// writeStatus writes the whole report. A repository that cannot be read
// is named with the reason, and the report goes on; the error then says
// that the report is not complete. The time bands and the times of the
// report use loc; cumin status passes the time zone of the Host.
func writeStatus(ctx context.Context, w io.Writer, settings *config.Settings, stateDir string, at time.Time, loc *time.Location, read readRepository) error {
	writeQuota(w, settings, stateDir, at, loc)
	writeStopRequest(w, stateDir, loc)

	var working, waiting, failed []string
	for _, repo := range settings.Repositories {
		snapshot, err := read(ctx, repo)
		if err != nil {
			failed = append(failed, fmt.Sprintf("  %s: not read: %v", repo, err))
			continue
		}
		visit := func(issue github.Issue, requirement bool) {
			for _, label := range issue.Labels {
				line := fmt.Sprintf("  %s #%d %s", repo, issue.Number, label)
				switch {
				case slices.Contains(agentLabels[requirement], label):
					working = append(working, line)
				case slices.Contains(ownerLabels, label):
					waiting = append(waiting, line)
				}
			}
		}
		for _, requirement := range snapshot.RequirementIssues {
			visit(requirement, true)
			for _, sub := range requirement.SubIssues {
				if !sub.Closed {
					visit(sub, false)
				}
			}
		}
	}
	writeList(w, "Agents at work (from the labels on GitHub):", working)
	writeList(w, "Waiting for the Owner:", waiting)
	if len(failed) > 0 {
		writeList(w, "Repositories not read:", failed)
		return fmt.Errorf("%d of %d repositories were not read", len(failed), len(settings.Repositories))
	}
	return nil
}

// writeStopRequest says that cumin stops after its runs, when the Owner asked for it with
// `cumin stop --after-current-runs`. cumin run removes the request when it
// exits, so the lines are there only while that stop is asked for or going
// on. The runs that it waits for are the agents at work below.
func writeStopRequest(w io.Writer, stateDir string, loc *time.Location) {
	request, found, err := state.ReadStopRequest(filepath.Join(stateDir, state.StopRequestFileName))
	switch {
	case err != nil:
		fmt.Fprintln(w)
		fmt.Fprintf(w, "Stop:\n  the stop request file was not read: %v\n", err)
	case found:
		fmt.Fprintln(w)
		fmt.Fprintf(w, "Stop:\n  stopping after the current runs, requested at %s: cumin starts no new work and exits when the agents at work have ended\n", stamp(request.RequestedAt, loc))
	}
}

func writeList(w io.Writer, heading string, lines []string) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, heading)
	if len(lines) == 0 {
		fmt.Fprintln(w, "  none")
		return
	}
	for _, line := range lines {
		fmt.Fprintln(w, line)
	}
}

// writeQuota writes the latest usage that cumin run kept, the limits at
// the time of the report, and the allowance.
func writeQuota(w io.Writer, settings *config.Settings, stateDir string, at time.Time, loc *time.Location) {
	fmt.Fprintln(w, "Quota:")
	stored, ok := state.Open(filepath.Join(stateDir, stateFileName), nil).Quota()
	if !ok {
		fmt.Fprintln(w, "  not read yet: cumin run reads the usage before its first start")
		return
	}
	var allowance quota.Allowance
	if read, err := state.ReadAllowance(filepath.Join(stateDir, state.AllowanceFileName)); err != nil {
		fmt.Fprintf(w, "  the allowance file was not read: %v\n", err)
	} else {
		allowance.FiveHourUntil = read.FiveHourUntil
	}
	usage := quota.Usage{
		FiveHour: quota.Window{Utilization: stored.FiveHour.Utilization, ResetsAt: stored.FiveHour.ResetsAt},
		Weekly:   quota.Window{Utilization: stored.Weekly.Utilization, ResetsAt: stored.Weekly.ResetsAt},
	}
	decision := quota.Decide(usage, settings.Quota, allowance, at, loc)
	fmt.Fprintf(w, "  read at %s\n", stamp(stored.ReadAt, loc))
	fmt.Fprintf(w, "  5h window:     %s used, limit %s, resets at %s\n",
		percent(stored.FiveHour.Utilization), percentOf(decision.FiveHourLimit), stamp(stored.FiveHour.ResetsAt, loc))
	fmt.Fprintf(w, "  weekly window: %s used, pace limit %s, resets at %s\n",
		percent(stored.Weekly.Utilization), percentOf(decision.WeeklyLimit), stamp(stored.Weekly.ResetsAt, loc))
	if at.Before(allowance.FiveHourUntil) {
		fmt.Fprintf(w, "  allowance: the 5h limit is 100%% until %s\n", stamp(allowance.FiveHourUntil, loc))
	}
	if decision.Allows() {
		fmt.Fprintln(w, "  new starts: go on")
		return
	}
	names := make([]string, len(decision.Stopped))
	for i, name := range decision.Stopped {
		names[i] = string(name)
	}
	line := "  new starts: stopped by the " + strings.Join(names, " and the ") + " window"
	if next, ok := quota.NextTry(usage, settings.Quota, allowance, at, loc); ok {
		line += ", next try at " + stamp(next, loc)
	}
	fmt.Fprintln(w, line)
}

func percent(utilization float64) string { return percentOf(utilization * 100) }

func percentOf(p float64) string { return fmt.Sprintf("%.1f%%", p) }

func stamp(t time.Time, loc *time.Location) string { return t.In(loc).Format(time.DateTime) }

// version returns the version of the binary from the build information of
// Go: the version of the main module, and the commit when the build ran in
// a git checkout (runtime/debug, BuildInfo and BuildSetting).
func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	v := info.Main.Version
	if v == "" {
		v = "(devel)"
	}
	var revision, modified string
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value
		}
	}
	if revision != "" {
		v += " " + revision
		if modified == "true" {
			v += " (modified)"
		}
	}
	return v
}
