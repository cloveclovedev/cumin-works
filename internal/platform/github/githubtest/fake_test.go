package githubtest_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
)

// The four calls of an agent start (agent-run.md, topic "Steps of one request") go
// through the real client to the fake: the installation, the token, the
// App, and the bot user.
func TestFake_ServesTheEndpointsOfAnAgentStart(t *testing.T) {
	fake, server := githubtest.New(t)
	fake.AddRepository("example-org", "example-repo")
	fake.AddApp(githubtest.App{Slug: "example-implementer", Owner: "example-org", BotID: 424242})
	fake.AddUser("octocat", 583231)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cred := github.AppCredentials{ClientID: "Iv23liEXAMPLE", PrivateKey: key}
	client := github.NewAppClient(server.URL, server.Client())
	ctx := context.Background()

	token, err := client.CreateInstallationToken(ctx, cred, string(config.RoleImplementer), "example-org", "example-repo")
	if err != nil {
		t.Fatalf("CreateInstallationToken: %v", err)
	}
	if token.Token != githubtest.Token {
		t.Errorf("token = %q, want the token of the fake", token.Token)
	}
	requests := fake.TokenRequests()
	if len(requests) != 1 || strings.Join(requests[0].Repositories, ",") != "example-repo" || requests[0].Permissions["contents"] != "write" {
		t.Errorf("token requests = %+v, want one for example-repo with the permissions of the Implementer", requests)
	}

	app, err := client.GetApp(ctx, cred)
	if err != nil {
		t.Fatalf("GetApp: %v", err)
	}
	if app.Slug != "example-implementer" || app.Owner != "example-org" {
		t.Errorf("app = %+v", app)
	}

	bot, err := client.GetUser(ctx, token.Token, "example-implementer[bot]")
	if err != nil {
		t.Fatalf("GetUser(bot): %v", err)
	}
	if bot.ID != 424242 || bot.Login != "example-implementer[bot]" {
		t.Errorf("bot = %+v", bot)
	}
	user, err := client.GetUser(ctx, token.Token, "octocat")
	if err != nil {
		t.Fatalf("GetUser(octocat): %v", err)
	}
	if user.ID != 583231 {
		t.Errorf("user = %+v", user)
	}
}

func TestFake_UnknownRepositoryAndUserAre404(t *testing.T) {
	fake, server := githubtest.New(t)
	fake.AddRepository("example-org", "example-repo")
	fake.AddApp(githubtest.App{Slug: "example-implementer", Owner: "example-org", BotID: 1})
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cred := github.AppCredentials{ClientID: "Iv23liEXAMPLE", PrivateKey: key}
	client := github.NewAppClient(server.URL, server.Client())
	ctx := context.Background()

	if _, err := client.CreateInstallationToken(ctx, cred, string(config.RoleImplementer), "example-org", "missing-repo"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("token for a missing repository: err = %v, want 404", err)
	}
	if _, err := client.GetUser(ctx, githubtest.Token, "nobody"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("unknown user: err = %v, want 404", err)
	}
	// The installation token does not open the App endpoints without a
	// bearer; a wrong installation token is refused elsewhere.
	if _, err := client.GetUser(ctx, "ghs_wrongToken", "example-implementer[bot]"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("wrong token: err = %v, want 401", err)
	}
}

// A fake that never answers is a stalled connection. The timeout of the
// client ends a REST call and a GraphQL call with an error, and the fake
// answers the call after it.
func TestFake_HangNextHoldsTheRequestUntilTheClientGivesUp(t *testing.T) {
	fake, server := githubtest.New(t)
	fake.AddRepository("example-org", "example-repo")
	client := github.NewAppClient(server.URL, &http.Client{Timeout: 200 * time.Millisecond})
	ctx := context.Background()
	labels := []github.Label{{Name: "risk/low", Color: "C2E0C6"}}

	calls := []struct {
		name, method, path string
		call               func() error
	}{
		{"REST", http.MethodGet, "/repos/example-org/example-repo/labels", func() error {
			_, err := client.EnsureLabels(ctx, githubtest.Token, "example-org", "example-repo", labels)
			return err
		}},
		{"GraphQL", http.MethodPost, "/graphql", func() error {
			_, err := client.ReadSnapshot(ctx, githubtest.Token, "example-org", "example-repo")
			return err
		}},
	}
	for _, c := range calls {
		t.Run(c.name, func(t *testing.T) {
			fake.HangNext(c.method, c.path)
			start := time.Now()
			err := c.call()
			if err == nil || !strings.Contains(err.Error(), "Client.Timeout") {
				t.Fatalf("err = %v, want the timeout of the client", err)
			}
			if strings.Contains(err.Error(), server.URL) {
				t.Errorf("err = %v, holds the address of the request", err)
			}
			if took := time.Since(start); took > 5*time.Second {
				t.Errorf("the call took %v, want the timeout of the client", took)
			}
			if err := c.call(); err != nil {
				t.Errorf("the call after the timeout: %v", err)
			}
		})
	}
}
