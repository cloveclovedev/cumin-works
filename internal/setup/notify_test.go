package setup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/platform/keychain"
)

// theAddress stands for a webhook address in the tests. It is made up.
const theAddress = "https://discord.test/api/webhooks/1234567890/made-up-token"

func TestCheckNotifyAddress(t *testing.T) {
	t.Parallel()
	good := []string{
		theAddress,
		"https://discord.com/api/webhooks/1/2",
	}
	for _, address := range good {
		if err := CheckNotifyAddress(address); err != nil {
			t.Errorf("CheckNotifyAddress(%q) = %v, want nil", address, err)
		}
	}
	bad := []struct {
		name    string
		address string
	}{
		{"empty", ""},
		{"only spaces", "   "},
		{"http", "http://discord.test/api/webhooks/1/2"},
		{"no scheme", "discord.test/api/webhooks/1/2"},
		{"no host", "https:///api/webhooks/1/2"},
		{"no path", "https://discord.test"},
		{"only a slash", "https://discord.test/"},
		{"not an address", "https://discord.test/%zz"},
	}
	for _, test := range bad {
		err := CheckNotifyAddress(strings.TrimSpace(test.address))
		if err == nil {
			t.Errorf("CheckNotifyAddress(%s) = nil, want an error", test.name)
			continue
		}
		if strings.Contains(err.Error(), "webhooks") {
			t.Errorf("the error of %s holds a part of the address: %v", test.name, err)
		}
	}
}

func TestReadAddress_ReadsOneLine(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	got, err := ReadAddress(strings.NewReader("  "+theAddress+"  \nsomething else\n"), &out, DiscordWebhook, false)
	if err != nil {
		t.Fatalf("ReadAddress: %v", err)
	}
	if got != theAddress {
		t.Errorf("ReadAddress = %q, want the address without spaces", got)
	}
	if out.Len() != 0 {
		t.Errorf("a pipe was asked for the address: %q", out.String())
	}
}

func TestReadAddress_AsksOnATerminalAndReportsAnEmptyInput(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	if _, err := ReadAddress(strings.NewReader(theAddress+"\n"), &out, DiscordWebhook, true); err != nil {
		t.Fatalf("ReadAddress: %v", err)
	}
	if !strings.Contains(out.String(), DiscordWebhook.What) {
		t.Errorf("the prompt does not name the address:\n%s", out.String())
	}
	if strings.Contains(out.String(), theAddress) {
		t.Errorf("the prompt echoes the address:\n%s", out.String())
	}

	if _, err := ReadAddress(strings.NewReader(""), &bytes.Buffer{}, DiscordWebhook, false); err == nil {
		t.Error("ReadAddress with no input = nil, want an error")
	}
}

// fakeStore is the Keychain of the tests that run without macOS.
type fakeStore struct {
	items   map[string][]byte
	setErr  error
	getErr  error
	changed []byte
}

func (f *fakeStore) Set(_ context.Context, service, account string, secret []byte) error {
	if f.setErr != nil {
		return f.setErr
	}
	if f.items == nil {
		f.items = map[string][]byte{}
	}
	f.items[service+"/"+account] = secret
	return nil
}

func (f *fakeStore) Get(_ context.Context, service, account string) ([]byte, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.changed != nil {
		return f.changed, nil
	}
	value, ok := f.items[service+"/"+account]
	if !ok {
		return nil, keychain.ErrNotFound
	}
	return value, nil
}

func TestStoreNotifyAddress_StoresAndSaysWhatIsNext(t *testing.T) {
	t.Parallel()
	store := &fakeStore{}
	var out bytes.Buffer
	if err := StoreNotifyAddress(context.Background(), store, DiscordWebhook, theAddress, &out); err != nil {
		t.Fatalf("StoreNotifyAddress: %v", err)
	}
	if got := string(store.items[keychain.Service+"/"+keychain.DiscordWebhookAccount]); got != theAddress {
		t.Errorf("the Keychain holds %q, want the address", got)
	}
	text := out.String()
	if strings.Contains(text, theAddress) {
		t.Errorf("the output holds the address:\n%s", text)
	}
	for _, want := range []string{"stored", keychain.Service, keychain.DiscordWebhookAccount, DiscordWebhook.Setting} {
		if !strings.Contains(text, want) {
			t.Errorf("the output has no %q:\n%s", want, text)
		}
	}
}

func TestStoreNotifyAddress_Failures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		store   *fakeStore
		address string
	}{
		{"a wrong address", &fakeStore{}, "http://discord.test/api/webhooks/1/2"},
		{"the Keychain refuses to store", &fakeStore{setErr: errors.New("no")}, theAddress},
		{"the item cannot be read back", &fakeStore{getErr: errors.New("no")}, theAddress},
		{"another value comes back", &fakeStore{changed: []byte("https://discord.test/other")}, theAddress},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var out bytes.Buffer
			err := StoreNotifyAddress(context.Background(), test.store, DiscordWebhook, test.address, &out)
			if err == nil {
				t.Fatal("StoreNotifyAddress = nil, want an error")
			}
			if strings.Contains(err.Error(), "webhooks") || strings.Contains(out.String(), "webhooks") {
				t.Errorf("the address reached the error or the output: %v %q", err, out.String())
			}
			if strings.Contains(out.String(), "stored") {
				t.Errorf("the output says stored after a failure:\n%s", out.String())
			}
		})
	}
}

// The command against a real Keychain: a temporary one, never the login
// keychain. The test is skipped where the security command does not exist.
func TestStoreNotifyAddress_RealKeychain(t *testing.T) {
	if _, err := os.Stat("/usr/bin/security"); err != nil {
		t.Skip("/usr/bin/security does not exist: the Keychain is a macOS feature")
	}
	path := filepath.Join(t.TempDir(), "notify-test.keychain-db")
	// The password protects only this throwaway keychain.
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("/usr/bin/security", args...).CombinedOutput(); err != nil {
			t.Fatalf("security %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("create-keychain", "-p", "test-password", path)
	t.Cleanup(func() { _ = exec.Command("/usr/bin/security", "delete-keychain", path).Run() })
	run("unlock-keychain", "-p", "test-password", path)

	store := keychain.Open(path)
	ctx := context.Background()
	var out bytes.Buffer
	if err := StoreNotifyAddress(ctx, store, DiscordWebhook, theAddress, &out); err != nil {
		t.Fatalf("StoreNotifyAddress: %v", err)
	}
	// A second run replaces the item and does not fail.
	const other = "https://discord.test/api/webhooks/1234567890/another-made-up-token"
	if err := StoreNotifyAddress(ctx, store, DiscordWebhook, other, &out); err != nil {
		t.Fatalf("StoreNotifyAddress again: %v", err)
	}
	got, err := store.Get(ctx, keychain.Service, keychain.DiscordWebhookAccount)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != other {
		t.Errorf("the item holds the older address")
	}
	if strings.Contains(out.String(), "webhooks") {
		t.Errorf("the output holds an address:\n%s", out.String())
	}
}
