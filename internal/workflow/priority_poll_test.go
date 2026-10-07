package workflow_test

import (
	"context"
	"slices"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
	"github.com/cloveclovedev/cumin-works/internal/workflow"
)

// The test of a top-level requirement in cumin-core.md: of two ready issues,
// the one with the higher
// priority label starts first, although its number is higher. The settings
// name no priority labels, so the labels are the default ones, and cumin
// creates them.
func TestTheHigherPriorityStartsFirst(t *testing.T) {
	sc := newScene(t, cliOptions{sleeps: true})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 11, Parent: 6, Title: "Add the logout screen",
		Labels: []string{"cumin/status/ready", "risk/low", "cumin/priority/P1"}})
	service := sc.service()

	startAndStop(t, sc, service)

	if got := sc.fake.Issue(sc.repo, 11).Labels; !slices.Contains(got, workflow.LabelImplementing) {
		t.Errorf("labels of #11 = %v, want the claim: it has the priority label", got)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, "cumin/status/ready") {
		t.Errorf("labels of #10 = %v, want cumin/status/ready: the limit is 1", got)
	}
	labels := sc.fake.LabelNames(sc.repo)
	for _, want := range []string{"cumin/priority/P0", "cumin/priority/P1", "cumin/priority/P2", "cumin/priority/P3"} {
		if !slices.Contains(labels, want) {
			t.Errorf("the repository has no label %s: %v", want, labels)
		}
	}
}

// The setting of the repository replaces the default names: the label of
// the organization orders the starts, the default name means nothing, and
// cumin creates no priority label, neither the default ones nor the ones
// that the setting names.
func TestTheSettingOfTheRepositoryNamesThePriorityLabels(t *testing.T) {
	sc := newScene(t, cliOptions{sleeps: true})
	sc.fake.SetFile(sc.repo, ".cumin/config.toml", githubtest.File{Content: "priority_labels = [\"priority/P0\", \"priority/P1\"]\n"})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle,
		Labels: []string{"cumin/status/ready", "risk/low", "cumin/priority/P0"}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 11, Parent: 6, Title: "Add the logout screen",
		Labels: []string{"cumin/status/ready", "risk/low", "priority/P1"}})
	service := sc.service()

	startAndStop(t, sc, service)

	if got := sc.fake.Issue(sc.repo, 11).Labels; !slices.Contains(got, workflow.LabelImplementing) {
		t.Errorf("labels of #11 = %v, want the claim: it has a label of the setting", got)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, "cumin/status/ready") {
		t.Errorf("labels of #10 = %v, want cumin/status/ready", got)
	}
	if labels := sc.fake.LabelNames(sc.repo); len(labels) != 0 {
		t.Errorf("cumin created labels: %v, want none", labels)
	}
}

// startAndStop does one poll, waits until the agent run that it started
// sleeps, and stops the run, so that only the first start counts.
func startAndStop(t *testing.T, sc *scene, service *workflow.Service) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := service.Poll(ctx); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	waitForAgentRun(t, sc)
	cancel()
	service.Wait()
}
