package github_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
)

var testLabels = []github.Label{
	{Name: "cumin/status/ready", Color: "0E8A16", Description: "The Owner says: go"},
	{Name: "cumin/status/implementing", Color: "1D76DB", Description: "The Implementer works"},
	{Name: "risk/low", Color: "C2E0C6", Description: "A few lines with an obvious effect"},
}

func TestEnsureLabels_CreatesOnlyTheMissingLabels(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	// One label exists with another case of the name and another color.
	fake.AddLabel(repo, githubtest.Label{Name: "Cumin/Status/Ready", Color: "ffffff"})
	client := github.NewAppClient(server.URL, server.Client())
	ctx := context.Background()

	created, err := client.EnsureLabels(ctx, githubtest.Token, "example-org", "example-repo", testLabels)
	if err != nil {
		t.Fatalf("EnsureLabels: %v", err)
	}
	if want := []string{"cumin/status/implementing", "risk/low"}; !slices.Equal(created, want) {
		t.Errorf("created = %v, want %v", created, want)
	}
	if got := fake.LabelNames(repo); !slices.Equal(got, []string{"Cumin/Status/Ready", "cumin/status/implementing", "risk/low"}) {
		t.Errorf("labels of the repository = %v", got)
	}
	if n := fake.CountRequests(http.MethodPost, "/repos/example-org/example-repo/labels"); n != 2 {
		t.Errorf("%d POST requests, want 2", n)
	}

	// The second call creates nothing.
	created, err = client.EnsureLabels(ctx, githubtest.Token, "example-org", "example-repo", testLabels)
	if err != nil || len(created) != 0 {
		t.Errorf("second call: created = %v, err = %v, want nothing", created, err)
	}
	if n := fake.CountRequests(http.MethodPost, "/repos/example-org/example-repo/labels"); n != 2 {
		t.Errorf("%d POST requests after the second call, want 2", n)
	}
}

func TestEnsureLabels_ReadsEveryPage(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	for i := range 101 {
		fake.AddLabel(repo, githubtest.Label{Name: "label-" + string(rune('a'+i%26)) + string(rune('a'+i/26))})
	}
	fake.AddLabel(repo, githubtest.Label{Name: "risk/low"})
	client := github.NewAppClient(server.URL, server.Client())

	created, err := client.EnsureLabels(context.Background(), githubtest.Token, "example-org", "example-repo", testLabels)
	if err != nil {
		t.Fatalf("EnsureLabels: %v", err)
	}
	if want := []string{"cumin/status/ready", "cumin/status/implementing"}; !slices.Equal(created, want) {
		t.Errorf("created = %v, want %v (risk/low is on the second page)", created, want)
	}
	if n := fake.CountRequests(http.MethodGet, "/repos/example-org/example-repo/labels"); n != 2 {
		t.Errorf("%d GET requests, want 2 pages", n)
	}
}

func TestEnsureLabels_ErrorNamesTheStatusWithoutTheToken(t *testing.T) {
	fake, server := githubtest.New(t)
	fake.AddRepository("example-org", "example-repo")
	client := github.NewAppClient(server.URL, server.Client())

	_, err := client.EnsureLabels(context.Background(), githubtest.Token, "example-org", "missing-repo", testLabels)
	if err == nil || !strings.Contains(err.Error(), "status 404") || !strings.Contains(err.Error(), "Not Found") {
		t.Errorf("err = %v, want status 404 and the message", err)
	}
	if err != nil && strings.Contains(err.Error(), githubtest.Token) {
		t.Errorf("the error holds the token: %v", err)
	}
}

func TestSetIssueLabels_ReplacesTheLabelsOfTheIssue(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddIssue(repo, &githubtest.Issue{Number: 10, Labels: []string{"cumin/status/ready", "risk/low"}})
	client := github.NewAppClient(server.URL, server.Client())
	ctx := context.Background()

	if err := client.SetIssueLabels(ctx, githubtest.Token, "example-org", "example-repo", 10, []string{"risk/low", "cumin/status/implementing"}); err != nil {
		t.Fatalf("SetIssueLabels: %v", err)
	}
	if got := fake.Issue(repo, 10).Labels; !slices.Equal(got, []string{"risk/low", "cumin/status/implementing"}) {
		t.Errorf("labels = %v", got)
	}
	if n := fake.CountRequests(http.MethodPut, "/repos/example-org/example-repo/issues/10/labels"); n != 1 {
		t.Errorf("%d PUT requests, want 1", n)
	}

	// An empty list removes every label.
	if err := client.SetIssueLabels(ctx, githubtest.Token, "example-org", "example-repo", 10, nil); err != nil {
		t.Fatalf("SetIssueLabels with no labels: %v", err)
	}
	if got := fake.Issue(repo, 10).Labels; len(got) != 0 {
		t.Errorf("labels = %v, want none", got)
	}

	err := client.SetIssueLabels(ctx, githubtest.Token, "example-org", "example-repo", 11, []string{"risk/low"})
	if err == nil || !strings.Contains(err.Error(), "status 404") || strings.Contains(err.Error(), githubtest.Token) {
		t.Errorf("missing issue: err = %v, want status 404 without the token", err)
	}
}
