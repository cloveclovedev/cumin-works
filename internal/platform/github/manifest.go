package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
)

// AppRegistration is the GitHub App that the Manifest flow registered.
// GitHub returns the private key only once, in this response.
type AppRegistration struct {
	ID            int64
	Slug          string
	ClientID      string
	HTMLURL       string
	PrivateKeyPEM []byte
}

// String keeps the private key out of formatted output.
func (r AppRegistration) String() string {
	return fmt.Sprintf("AppRegistration{%s, client ID %s, [private key redacted]}", r.Slug, r.ClientID)
}

// Format keeps the private key out of every fmt verb.
func (r AppRegistration) Format(f fmt.State, verb rune) {
	_, _ = io.WriteString(f, r.String())
}

// LogValue keeps the private key out of structured logs.
func (r AppRegistration) LogValue() slog.Value {
	return slog.GroupValue(slog.String("slug", r.Slug), slog.String("client_id", r.ClientID), slog.String("private_key", "[redacted]"))
}

// ConvertManifestCode completes the GitHub App Manifest flow. GitHub registers
// the App in this call, and returns its private key. The code comes from the
// redirect of the browser and is valid for one hour. The call needs no
// authentication.
//
// The response also holds a client secret and a webhook secret. cumin uses
// neither, so this function does not read them.
func (c *AppClient) ConvertManifestCode(ctx context.Context, code string) (AppRegistration, error) {
	if code == "" {
		return AppRegistration{}, errors.New("github: the manifest code is empty")
	}
	var converted struct {
		ID       int64  `json:"id"`
		Slug     string `json:"slug"`
		ClientID string `json:"client_id"`
		HTMLURL  string `json:"html_url"`
		PEM      string `json:"pem"`
	}
	// The code is a secret for one hour, so it must not reach an error text.
	path := "/app-manifests/" + url.PathEscape(code) + "/conversions"
	const label = "/app-manifests/{code}/conversions"
	if err := c.do(ctx, "", http.MethodPost, path, label, nil, http.StatusCreated, &converted); err != nil {
		return AppRegistration{}, fmt.Errorf("github: register the App from the manifest: %w", err)
	}
	if converted.ClientID == "" || converted.PEM == "" || converted.Slug == "" {
		return AppRegistration{}, errors.New("github: register the App from the manifest: the response has no client ID, no slug, or no private key")
	}
	return AppRegistration{
		ID:            converted.ID,
		Slug:          converted.Slug,
		ClientID:      converted.ClientID,
		HTMLURL:       converted.HTMLURL,
		PrivateKeyPEM: []byte(converted.PEM),
	}, nil
}
