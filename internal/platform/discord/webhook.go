// Package discord sends a message to a Discord channel through a webhook.
//
// Everything that is specific to Discord stops here: the address of the
// webhook, the JSON body, the answer, and the length limit of a message.
// The caller passes plain text (internal/notify), so this package imports
// no other package of cumin.
package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// contentLimit is the maximum length of the content of one message, in
	// characters (official: Discord developer documentation, "Execute
	// Webhook": "Maximum 2000 characters").
	contentLimit = 2000
	// cut marks a message that was too long for one execution.
	cut = "..."
	// defaultTimeout is how long one execution may take. A notification is
	// small, and cumin must not wait on it.
	defaultTimeout = 10 * time.Second
	// maxErrorBody is how much of an error answer is read. The answers of
	// Discord are small.
	maxErrorBody = 8 << 10
)

// Webhook executes one Discord webhook.
//
// The address of a webhook holds its token, so it is a secret: it comes
// from the Keychain, stays in this struct, and never reaches an error, a
// log, or a returned value.
type Webhook struct {
	// URL is the address of the webhook,
	// https://discord.com/api/webhooks/<id>/<token>.
	URL string
	// HTTPClient is nil for a client with defaultTimeout.
	HTTPClient *http.Client
}

// Send executes the webhook with text as the content of one message. Text
// that is longer than the limit of the API is cut to the limit.
//
// The request asks for confirmation (the query wait=true). Without it the
// API answers 204 as soon as it takes the message, and, in the words of the
// documentation, gives "no error if message fails to save"; cumin would
// then report a notification that nobody received.
//
// Official: Discord developer documentation, "Execute Webhook"
// (POST /webhooks/{webhook.id}/{webhook.token}).
func (w Webhook) Send(ctx context.Context, text string) error {
	content := Cut(text)
	if content == "" {
		return errors.New("discord: the message is empty")
	}
	address, err := w.address()
	if err != nil {
		return err
	}
	body, err := json.Marshal(struct {
		Content string `json:"content"`
	}{content})
	if err != nil {
		return fmt.Errorf("discord: build the request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewReader(body))
	if err != nil {
		// The address is in the error of NewRequest, so it is not wrapped.
		return errors.New("discord: build the request failed")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")

	response, err := w.client().Do(request)
	if err != nil {
		// The error of the client holds the address. Only the reason of
		// the transport is kept.
		return fmt.Errorf("discord: execute the webhook: %s", transportReason(err))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("discord: execute the webhook: status %d%s", response.StatusCode, apiMessage(response.Body))
	}
	return nil
}

// Cut returns text with at most the number of characters that one message
// may hold. A text that is cut ends with "...", so that the reader knows
// that the rest is missing. The limit counts characters, not bytes.
func Cut(text string) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= contentLimit {
		return string(runes)
	}
	return string(runes[:contentLimit-len(cut)]) + cut
}

// address is the address of the webhook with the query that asks for
// confirmation. It also refuses an address that is not https, so that a
// webhook token never travels in the clear.
func (w Webhook) address() (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(w.URL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", errors.New("discord: the webhook URL must be an https address")
	}
	query := parsed.Query()
	query.Set("wait", "true")
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func (w Webhook) client() *http.Client {
	if w.HTTPClient != nil {
		return w.HTTPClient
	}
	return &http.Client{Timeout: defaultTimeout}
}

// transportReason is the reason of a failed request, without the address.
// The error of net/http starts with the method and the URL, followed by
// ": " and the reason.
func transportReason(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return urlErr.Err.Error()
	}
	return "the request failed"
}

// apiMessage reads the "message" of an error answer of Discord, such as
// "Unknown Webhook". Only that field is used: the rest of the answer is
// not needed, and nothing else is put into an error.
func apiMessage(body io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(body, maxErrorBody))
	if err != nil {
		return ""
	}
	var answer struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &answer); err != nil || strings.TrimSpace(answer.Message) == "" {
		return ""
	}
	return " (" + strings.TrimSpace(answer.Message) + ")"
}
