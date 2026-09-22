package discord

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeDiscord is a webhook that records what it received. The tests of
// cumin never call the real API.
type fakeDiscord struct {
	server  *httptest.Server
	path    string
	query   string
	header  http.Header
	body    map[string]any
	status  int
	answer  string
	calls   int
	rawBody string
}

func newFakeDiscord(t *testing.T) *fakeDiscord {
	t.Helper()
	f := &fakeDiscord{status: http.StatusOK, answer: `{"id":"1","content":"ok"}`}
	// TLS, because Send refuses an address that is not https. The client
	// of the server is the only one that trusts its certificate.
	f.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		f.calls++
		f.path, f.query, f.header, f.rawBody = r.URL.Path, r.URL.RawQuery, r.Header.Clone(), string(data)
		f.body = map[string]any{}
		_ = json.Unmarshal(data, &f.body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.status)
		_, _ = io.WriteString(w, f.answer)
	}))
	t.Cleanup(f.server.Close)
	return f
}

// webhookURL is the address of the fake, with the shape of a real webhook.
func (f *fakeDiscord) webhookURL() string { return f.server.URL + "/api/webhooks/1/secret-token" }

func (f *fakeDiscord) webhook() Webhook {
	return Webhook{URL: f.webhookURL(), HTTPClient: f.server.Client()}
}

// send runs the real Send against the fake.
func send(f *fakeDiscord, text string) error {
	return f.webhook().Send(context.Background(), text)
}

func TestSend_ExecutesTheWebhookWithTheTextAsContent(t *testing.T) {
	f := newFakeDiscord(t)
	if err := send(f, "cumin: I2: the Implementer returned blocked"); err != nil {
		t.Fatalf("Send() = %v, want nil", err)
	}
	if f.calls != 1 {
		t.Fatalf("the webhook was executed %d times, want 1", f.calls)
	}
	if f.path != "/api/webhooks/1/secret-token" {
		t.Errorf("path = %q, want the path of the webhook", f.path)
	}
	// wait=true, so that Discord answers only after it saved the message.
	if f.query != "wait=true" {
		t.Errorf("query = %q, want %q", f.query, "wait=true")
	}
	if got := f.header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got, _ := f.body["content"].(string); got != "cumin: I2: the Implementer returned blocked" {
		t.Errorf("content = %q, want the text", got)
	}
	// Nothing in a notification may ring a channel: an agent writes part
	// of the text, so mention parsing is off.
	mentions, ok := f.body["allowed_mentions"].(map[string]any)
	if !ok {
		t.Fatalf("the body has no allowed_mentions: %s", f.rawBody)
	}
	if parse, ok := mentions["parse"].([]any); !ok || len(parse) != 0 {
		t.Errorf("allowed_mentions.parse = %v, want an empty list", mentions["parse"])
	}
	if len(f.body) != 2 {
		t.Errorf("the body holds %d fields (%s), want content and allowed_mentions", len(f.body), f.rawBody)
	}
}

// A message that names @everyone reaches Discord as text, and rings
// nobody, because the request turns mention parsing off.
func TestSend_DoesNotLetAMentionRingAChannel(t *testing.T) {
	f := newFakeDiscord(t)
	if err := send(f, "cumin: I2: @everyone the run stopped"); err != nil {
		t.Fatalf("Send() = %v, want nil", err)
	}
	if content, _ := f.body["content"].(string); !strings.Contains(content, "@everyone") {
		t.Errorf("content = %q, want the text as it was written", content)
	}
	mentions, _ := f.body["allowed_mentions"].(map[string]any)
	if parse, ok := mentions["parse"].([]any); !ok || len(parse) != 0 {
		t.Errorf("allowed_mentions.parse = %v, want an empty list", mentions["parse"])
	}
}

func TestSend_AcceptsTheAnswersOfTheAPI(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNoContent} {
		f := newFakeDiscord(t)
		f.status, f.answer = status, ""
		if err := send(f, "cumin: stopped"); err != nil {
			t.Errorf("status %d: Send() = %v, want nil", status, err)
		}
	}
}

func TestSend_AFailureNamesTheStatusAndNotTheAddress(t *testing.T) {
	f := newFakeDiscord(t)
	f.status, f.answer = http.StatusInternalServerError, `{"message":"Internal Server Error","code":0}`
	err := send(f, "cumin: stopped")
	if err == nil {
		t.Fatal("Send() = nil, want an error")
	}
	text := err.Error()
	if !strings.Contains(text, "500") {
		t.Errorf("the error %q does not name the status", text)
	}
	if !strings.Contains(text, "Internal Server Error") {
		t.Errorf("the error %q does not hold the message of the API", text)
	}
	for _, secret := range []string{"secret-token", f.server.URL, strings.TrimPrefix(f.server.URL, "https://")} {
		if strings.Contains(text, secret) {
			t.Errorf("the error %q holds a part of the webhook address", text)
		}
	}
}

func TestSend_RefusesAnAddressThatIsNotHTTPS(t *testing.T) {
	for _, address := range []string{"", "   ", "http://discord.com/api/webhooks/1/token", "://", "not an address"} {
		err := Webhook{URL: address}.Send(context.Background(), "cumin: stopped")
		if err == nil {
			t.Errorf("Send() with the address %q = nil, want an error", address)
			continue
		}
		if strings.Contains(err.Error(), "token") {
			t.Errorf("the error %q holds a part of the address", err.Error())
		}
	}
}

func TestSend_RefusesAnEmptyMessage(t *testing.T) {
	f := newFakeDiscord(t)
	if err := send(f, "   \n  "); err == nil {
		t.Error("Send() with an empty message = nil, want an error")
	}
	if f.calls != 0 {
		t.Errorf("the webhook was executed %d times, want 0", f.calls)
	}
}

func TestSend_CutsAMessageThatIsTooLong(t *testing.T) {
	f := newFakeDiscord(t)
	// Multi-byte characters: the limit of the API counts characters.
	long := strings.Repeat("あ", contentLimit+500)
	if err := send(f, long); err != nil {
		t.Fatalf("Send() = %v, want nil", err)
	}
	content, _ := f.body["content"].(string)
	if n := len([]rune(content)); n != contentLimit {
		t.Errorf("the content holds %d characters, want %d", n, contentLimit)
	}
	if !strings.HasSuffix(content, cut) {
		t.Errorf("the content does not end with %q", cut)
	}
}

func TestCut_KeepsAMessageThatFits(t *testing.T) {
	t.Parallel()
	text := "cumin: I2: the verification failed"
	if got := Cut(text); got != text {
		t.Errorf("Cut(%q) = %q, want the text", text, got)
	}
	if got := Cut(strings.Repeat("a", contentLimit)); len([]rune(got)) != contentLimit {
		t.Errorf("a text of the exact limit was changed")
	}
}
