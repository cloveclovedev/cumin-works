package github

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
)

const (
	testClientID       = "Iv23liEXAMPLEclientid"
	testInstallationID = 4242
	testToken          = "ghs_fakeInstallationToken"
)

var testKey = sync.OnceValue(func() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return key
})

// fakeGitHub has only the two endpoints that the token code uses. It checks
// the JWT of every request with the public key of the App.
type fakeGitHub struct {
	t         *testing.T
	publicKey *rsa.PublicKey

	mu        sync.Mutex
	jwts      []string
	tokenBody map[string]any
}

func newFakeGitHub(t *testing.T) (*fakeGitHub, *httptest.Server) {
	t.Helper()
	fake := &fakeGitHub{t: t, publicKey: &testKey().PublicKey}
	server := httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(server.Close)
	return fake, server
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	jwt, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || !f.validJWT(jwt) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"message": "A JSON web token could not be decoded"})
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jwts = append(f.jwts, jwt)

	switch r.Method + " " + r.URL.Path {
	case "GET /repos/example-org/example-repo/installation":
		writeJSON(w, http.StatusOK, map[string]any{"id": testInstallationID})
	case fmt.Sprintf("POST /app/installations/%d/access_tokens", testInstallationID):
		if err := json.NewDecoder(r.Body).Decode(&f.tokenBody); err != nil {
			f.t.Errorf("token request body: %v", err)
		}
		writeJSON(w, http.StatusCreated, map[string]any{"token": testToken, "expires_at": "2026-09-20T10:00:00Z"})
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
	}
}

// validJWT checks the signature and the claims the way GitHub documents them.
func (f *fakeGitHub) validJWT(jwt string) bool {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		f.t.Errorf("the JWT has %d parts, want 3", len(parts))
		return false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		f.t.Errorf("JWT signature: %v", err)
		return false
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(f.publicKey, crypto.SHA256, digest[:], signature); err != nil {
		f.t.Errorf("JWT signature does not verify: %v", err)
		return false
	}

	var header struct{ Alg, Typ string }
	var claims struct {
		Iat, Exp int64
		Iss      string
	}
	decodeSegment(f.t, parts[0], &header)
	decodeSegment(f.t, parts[1], &claims)
	if header.Alg != "RS256" {
		f.t.Errorf("JWT alg = %q, want RS256", header.Alg)
	}
	if claims.Iss != testClientID {
		f.t.Errorf("JWT iss = %q, want the client ID", claims.Iss)
	}
	now := time.Now().Unix()
	if claims.Iat > now {
		f.t.Errorf("JWT iat is %d seconds in the future", claims.Iat-now)
	}
	if claims.Exp <= now || claims.Exp-now > 600 {
		f.t.Errorf("JWT exp is %d seconds from now, want 1 to 600", claims.Exp-now)
	}
	if claims.Exp-claims.Iat > 660 {
		f.t.Errorf("JWT exp - iat = %d seconds", claims.Exp-claims.Iat)
	}
	return !f.t.Failed()
}

func decodeSegment(t *testing.T, segment string, out any) {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		t.Fatalf("JWT segment: %v", err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("JWT segment: %v", err)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func testCredentials() AppCredentials {
	return AppCredentials{ClientID: testClientID, PrivateKey: testKey()}
}

var allApps = []string{
	config.AppCuminCore,
	string(config.RoleChiefEngineer),
	string(config.RoleImplementer),
	string(config.RoleReviewer),
}

func TestAppToken_IsLimitedToOneRepositoryAndToThePermissionsOfTheApp(t *testing.T) {
	for _, app := range allApps {
		t.Run(app, func(t *testing.T) {
			fake, server := newFakeGitHub(t)
			client := NewAppClient(server.URL, server.Client())

			token, err := client.CreateInstallationToken(context.Background(), testCredentials(), app, "example-org", "example-repo")
			if err != nil {
				t.Fatalf("CreateInstallationToken: %v", err)
			}
			if token.Token != testToken {
				t.Errorf("Token = %q", token.Token)
			}
			if want := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC); !token.ExpiresAt.Equal(want) {
				t.Errorf("ExpiresAt = %v, want %v", token.ExpiresAt, want)
			}

			wantPermissions := map[string]any{}
			for name, level := range appPermissions[app] {
				wantPermissions[name] = level
			}
			want := map[string]any{
				"repositories": []any{"example-repo"},
				"permissions":  wantPermissions,
			}
			got, _ := json.Marshal(fake.tokenBody)
			wantJSON, _ := json.Marshal(want)
			if !bytes.Equal(got, wantJSON) {
				t.Errorf("token request body = %s, want %s", got, wantJSON)
			}
		})
	}
}

// The values come from the table in docs/ja/development/github-app-setup.md.
// Change the document and this test together.
func TestAppPermissions_MatchTheSetupDocument(t *testing.T) {
	want := map[string]map[string]string{
		"cumin-core":     {"contents": "write", "pull_requests": "write", "issues": "write"},
		"chief-engineer": {"issues": "write", "contents": "read"},
		"implementer":    {"contents": "write", "pull_requests": "write", "issues": "read"},
		"reviewer":       {"pull_requests": "write", "contents": "read", "issues": "read"},
	}
	if got := slices.Sorted(maps.Keys(appPermissions)); !slices.Equal(got, slices.Sorted(maps.Keys(want))) {
		t.Fatalf("apps = %v", got)
	}
	for app, wantPermissions := range want {
		got, ok := AppPermissions(app)
		if !ok || !maps.Equal(got, wantPermissions) {
			t.Errorf("AppPermissions(%q) = %v, want %v", app, got, wantPermissions)
		}
		for _, forbidden := range []string{"administration", "workflows"} {
			if _, has := got[forbidden]; has {
				t.Errorf("app %q has the %s permission", app, forbidden)
			}
		}
	}
}

func TestAppPermissions_ReturnsACopy(t *testing.T) {
	got, _ := AppPermissions(config.AppCuminCore)
	got["administration"] = "write"
	again, _ := AppPermissions(config.AppCuminCore)
	if _, has := again["administration"]; has {
		t.Error("a caller changed the permission table")
	}
}

func TestAppToken_UnknownAppMakesNoRequest(t *testing.T) {
	fake, server := newFakeGitHub(t)
	client := NewAppClient(server.URL, server.Client())

	_, err := client.CreateInstallationToken(context.Background(), testCredentials(), "owner", "example-org", "example-repo")
	if err == nil || !strings.Contains(err.Error(), `unknown app "owner"`) {
		t.Errorf("err = %v", err)
	}
	if len(fake.jwts) != 0 {
		t.Errorf("the client sent %d requests", len(fake.jwts))
	}
}

func TestAppToken_ErrorNamesTheStatusCodeAndHidesTheJWT(t *testing.T) {
	fake, server := newFakeGitHub(t)
	client := NewAppClient(server.URL, server.Client())

	// The App is not installed on this repository.
	_, err := client.CreateInstallationToken(context.Background(), testCredentials(), config.AppCuminCore, "example-org", "other-repo")
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{"status 404", "Not Found", "example-org/other-repo", "cumin-core"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to contain %q", err, want)
		}
	}
	if len(fake.jwts) != 1 {
		t.Fatalf("the fake saw %d requests, want 1", len(fake.jwts))
	}
	if strings.Contains(err.Error(), fake.jwts[0]) {
		t.Error("the error holds the JWT")
	}
}

func TestInstallationToken_DoesNotAppearInOutput(t *testing.T) {
	token := InstallationToken{Token: testToken, ExpiresAt: time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)}

	var logs bytes.Buffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("created", "token", token)
	formatted := fmt.Sprintf("%v %+v %#v %s %q %d %x", token, token, token, token, token, token, &token)

	for name, output := range map[string]string{"log": logs.String(), "fmt": formatted} {
		if strings.Contains(output, testToken) {
			t.Errorf("%s output holds the token: %s", name, output)
		}
		if !strings.Contains(output, "2026-09-20T10:00:00Z") {
			t.Errorf("%s output = %s, want the expiry time", name, output)
		}
	}
}

func TestParsePrivateKey(t *testing.T) {
	key := testKey()
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	for name, block := range map[string]*pem.Block{
		"PKCS #1 (the form that GitHub issues)": {Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)},
		"PKCS #8":                               {Type: "PRIVATE KEY", Bytes: pkcs8},
	} {
		got, err := ParsePrivateKey(pem.EncodeToMemory(block))
		if err != nil || !got.Equal(key) {
			t.Errorf("%s: key equal = %v, err = %v", name, err == nil && got.Equal(key), err)
		}
	}

	for name, input := range map[string][]byte{
		"not PEM":   []byte("not a key"),
		"not a key": pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: []byte("junk")}),
	} {
		if _, err := ParsePrivateKey(input); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
