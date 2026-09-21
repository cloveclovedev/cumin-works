package github

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
)

// newTokenSourceForTest returns a source against the fake, with a clock that
// the test moves, and tokens that expire one hour after the clock.
func newTokenSourceForTest(t *testing.T) (*fakeGitHub, *TokenSource, *time.Time) {
	t.Helper()
	fake, server := newFakeGitHub(t)
	client := NewAppClient(server.URL, server.Client())
	// The fake checks the JWT against the real clock, so only the clock of
	// the source moves.
	now := time.Now()
	fake.expiresAt = now.Add(time.Hour)
	source := NewTokenSource(client, testCredentials(), config.AppCuminCore, "example-org", "example-repo")
	source.now = func() time.Time { return now }
	return fake, source, &now
}

func TestTokenSource_ReusesTheTokenUntilCloseToItsExpiry(t *testing.T) {
	fake, source, now := newTokenSourceForTest(t)
	ctx := context.Background()

	first, err := source.Token(ctx)
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	// 54 minutes later, 6 minutes remain: still the same token.
	*now = now.Add(54 * time.Minute)
	again, err := source.Token(ctx)
	if err != nil || again != first {
		t.Errorf("second call: token = %q, err = %v, want %q", again, err, first)
	}
	if fake.tokens != 1 {
		t.Errorf("the fake created %d tokens, want 1", fake.tokens)
	}

	// 55 minutes after the creation, 5 minutes remain: a new token.
	*now = now.Add(time.Minute)
	fake.expiresAt = now.Add(time.Hour)
	renewed, err := source.Token(ctx)
	if err != nil || renewed == first {
		t.Errorf("third call: token = %q, err = %v, want a new token", renewed, err)
	}
	if fake.tokens != 2 {
		t.Errorf("the fake created %d tokens, want 2", fake.tokens)
	}
}

func TestTokenSource_KeepsNoTokenAfterAFailure(t *testing.T) {
	fake, source, _ := newTokenSourceForTest(t)
	ctx := context.Background()

	fake.failNextToken = true
	if _, err := source.Token(ctx); err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("err = %v, want status 500", err)
	}
	token, err := source.Token(ctx)
	if err != nil || token != testToken+"1" {
		t.Errorf("after the failure: token = %q, err = %v", token, err)
	}
}

func TestTokenSource_ConcurrentCallsCreateOneToken(t *testing.T) {
	fake, source, _ := newTokenSourceForTest(t)
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if _, err := source.Token(context.Background()); err != nil {
				t.Errorf("Token: %v", err)
			}
		})
	}
	wg.Wait()
	if fake.tokens != 1 {
		t.Errorf("the fake created %d tokens, want 1", fake.tokens)
	}
}

func TestTokenSource_DoesNotAppearInOutput(t *testing.T) {
	_, source, _ := newTokenSourceForTest(t)
	if _, err := source.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	formatted := fmt.Sprintf("%v %+v %#v %s %q %d %x", source, source, source, source, source, source, source)
	if strings.Contains(formatted, testToken) {
		t.Errorf("the output holds the token: %s", formatted)
	}
}
