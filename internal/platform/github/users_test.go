package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetUser(t *testing.T) {
	var gotAuth, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		if r.URL.Path == "/users/missing%5Bbot%5D" || r.URL.Path == "/users/missing[bot]" {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"message": "Not Found"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 987654, "login": "example-implementer[bot]", "type": "Bot"})
	}))
	t.Cleanup(server.Close)
	c := NewAppClient(server.URL, nil)

	user, err := c.GetUser(context.Background(), "ghs_token", "example-implementer[bot]")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if user.ID != 987654 || user.Login != "example-implementer[bot]" {
		t.Errorf("user = %+v", user)
	}
	if gotAuth != "Bearer ghs_token" {
		t.Errorf("Authorization = %q, want the token as a bearer", gotAuth)
	}
	if !strings.HasPrefix(gotPath, "/users/") {
		t.Errorf("path = %q", gotPath)
	}

	if _, err := c.GetUser(context.Background(), "ghs_token", "missing[bot]"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("GetUser(missing) err = %v, want a 404", err)
	}
	if _, err := c.GetUser(context.Background(), "", "x"); err == nil {
		t.Error("GetUser with an empty token: want an error")
	}
}
