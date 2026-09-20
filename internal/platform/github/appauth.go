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
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBaseURL is the address of the GitHub REST API.
const DefaultBaseURL = "https://api.github.com"

const (
	// GitHub recommends an issue time 60 seconds in the past, against clock drift.
	jwtBackdate = 60 * time.Second
	// GitHub accepts an expiry time of 10 minutes or less in the future.
	jwtLifetime = 9 * time.Minute
	// An error body from GitHub is small. Do not read more than this.
	maxErrorBody = 64 << 10
)

// AppCredentials identify one GitHub App.
type AppCredentials struct {
	ClientID   string
	PrivateKey *rsa.PrivateKey
}

// InstallationToken is an installation access token. GitHub fixes the expiry
// time at one hour after the token is created.
type InstallationToken struct {
	Token     string
	ExpiresAt time.Time
}

// String keeps the token out of formatted output.
func (t InstallationToken) String() string {
	return "InstallationToken{[redacted], expires " + t.ExpiresAt.Format(time.RFC3339) + "}"
}

// Format keeps the token out of every fmt verb. Without it, verbs such as %#v
// and %d print the fields and do not call String.
func (t InstallationToken) Format(f fmt.State, verb rune) {
	_, _ = io.WriteString(f, t.String())
}

// LogValue keeps the token out of structured logs.
func (t InstallationToken) LogValue() slog.Value {
	return slog.GroupValue(slog.String("token", "[redacted]"), slog.Time("expires_at", t.ExpiresAt))
}

// AppClient calls the GitHub REST API as a GitHub App.
type AppClient struct {
	baseURL string
	http    *http.Client
	now     func() time.Time
}

// NewAppClient returns a client for the API at baseURL. Tests pass the address
// of a fake GitHub. A nil httpClient means http.DefaultClient.
func NewAppClient(baseURL string, httpClient *http.Client) *AppClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &AppClient{baseURL: strings.TrimRight(baseURL, "/"), http: httpClient, now: time.Now}
}

// ParsePrivateKey reads the PEM of a GitHub App private key. GitHub issues
// PKCS #1 keys. PKCS #8 is accepted too.
func ParsePrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("github: the private key is not PEM")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("github: the private key is not PKCS #1 or PKCS #8")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("github: the private key is not an RSA key")
	}
	return key, nil
}

// CreateInstallationToken creates an installation access token for one GitHub
// App. The token is limited to the one repository and to the permissions of
// the App in the permission table. app is config.AppCuminCore or an agent role.
func (c *AppClient) CreateInstallationToken(ctx context.Context, cred AppCredentials, app, owner, repo string) (InstallationToken, error) {
	permissions, ok := AppPermissions(app)
	if !ok {
		return InstallationToken{}, fmt.Errorf("github: unknown app %q", app)
	}
	if cred.ClientID == "" || cred.PrivateKey == nil {
		return InstallationToken{}, fmt.Errorf("github: app %q has no client ID or no private key", app)
	}
	jwt, err := signJWT(cred, c.now())
	if err != nil {
		return InstallationToken{}, err
	}

	var installation struct {
		ID int64 `json:"id"`
	}
	path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/installation"
	if err := c.do(ctx, jwt, http.MethodGet, path, nil, http.StatusOK, &installation); err != nil {
		return InstallationToken{}, fmt.Errorf("github: find the installation of app %q on %s/%s: %w", app, owner, repo, err)
	}

	request := struct {
		Repositories []string          `json:"repositories"`
		Permissions  map[string]string `json:"permissions"`
	}{[]string{repo}, permissions}
	var created struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	path = fmt.Sprintf("/app/installations/%d/access_tokens", installation.ID)
	if err := c.do(ctx, jwt, http.MethodPost, path, request, http.StatusCreated, &created); err != nil {
		return InstallationToken{}, fmt.Errorf("github: create a token for app %q on %s/%s: %w", app, owner, repo, err)
	}
	if created.Token == "" {
		return InstallationToken{}, fmt.Errorf("github: create a token for app %q on %s/%s: the response has no token", app, owner, repo)
	}
	return InstallationToken{Token: created.Token, ExpiresAt: created.ExpiresAt}, nil
}

// signJWT signs the JSON Web Token that authenticates a GitHub App.
func signJWT(cred AppCredentials, now time.Time) (string, error) {
	header := `{"alg":"RS256","typ":"JWT"}`
	claims, err := json.Marshal(struct {
		IssuedAt  int64  `json:"iat"`
		ExpiresAt int64  `json:"exp"`
		Issuer    string `json:"iss"`
	}{now.Add(-jwtBackdate).Unix(), now.Add(jwtLifetime).Unix(), cred.ClientID})
	if err != nil {
		return "", err
	}
	encode := base64.RawURLEncoding.EncodeToString
	signingInput := encode([]byte(header)) + "." + encode(claims)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, cred.PrivateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", errors.New("github: sign the JWT: " + err.Error())
	}
	return signingInput + "." + encode(signature), nil
}

// do sends one request with the JWT. An error never holds the JWT or a
// response body that could hold a token: it holds only the status code and the
// "message" field of the error body from GitHub.
func (c *AppClient) do(ctx context.Context, jwt, method, path string, body any, wantStatus int, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "cumin-works")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != wantStatus {
		var apiError struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, maxErrorBody)).Decode(&apiError)
		return fmt.Errorf("%s %s: status %d: %s", method, path, resp.StatusCode, apiError.Message)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
