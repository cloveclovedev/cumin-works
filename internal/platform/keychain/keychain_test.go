package keychain

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testService = "cumin-works-test"
	testAccount = "github-app-private-key/Iv23liEXAMPLEclientid"
)

// newTestKeychain creates a temporary keychain and deletes it after the test.
// The tests never touch the login keychain.
func newTestKeychain(t *testing.T) *Keychain {
	t.Helper()
	return newTestKeychainIn(t, t.TempDir())
}

func newTestKeychainIn(t *testing.T, dir string) *Keychain {
	t.Helper()
	if _, err := os.Stat(securityPath); err != nil {
		t.Skipf("%s does not exist: the Keychain is a macOS feature", securityPath)
	}
	before := searchList(t)

	path := filepath.Join(dir, "test.keychain-db")
	// The password protects only this throwaway keychain, so an argument is fine.
	run(t, "create-keychain", "-p", "test-password", path)
	t.Cleanup(func() {
		run(t, "delete-keychain", path)
		if after := searchList(t); after != before {
			t.Errorf("the keychain search list changed:\nbefore: %s\nafter: %s", before, after)
		}
	})
	run(t, "unlock-keychain", "-p", "test-password", path)
	return Open(path)
}

func run(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command(securityPath, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("security %s: %v: %s", args[0], err, out)
	}
	return string(out)
}

func searchList(t *testing.T) string {
	t.Helper()
	return run(t, "list-keychains")
}

func generatePEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
}

func TestKeychain_StoresAndReadsAPrivateKey(t *testing.T) {
	k := newTestKeychain(t)
	ctx := context.Background()
	key := generatePEM(t)

	if err := k.SetBase64(ctx, testService, testAccount, key); err != nil {
		t.Fatalf("SetBase64: %v", err)
	}
	got, err := k.GetBase64(ctx, testService, testAccount)
	if err != nil {
		t.Fatalf("GetBase64: %v", err)
	}
	if !bytes.Equal(got, key) {
		t.Errorf("the key changed: got %d bytes, want %d bytes", len(got), len(key))
	}

	// The stored form is one line, so the security command returns it as it is.
	stored, err := k.Get(ctx, testService, testAccount)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if bytes.ContainsAny(stored, "\n ") || bytes.HasPrefix(stored, []byte("-----BEGIN")) {
		t.Error("the stored value is not base64 on one line")
	}
}

// A home directory can have a name in any language, so the path of the default
// keychain can hold other characters than ASCII, and a space.
func TestKeychain_WorksWithANonASCIIPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "日本語 ディレクトリ")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	k := newTestKeychainIn(t, dir)
	ctx := context.Background()

	if err := k.Set(ctx, testService, testAccount, []byte("value")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := k.Get(ctx, testService, testAccount)
	if err != nil || string(got) != "value" {
		t.Errorf("Get = %q, %v, want value", got, err)
	}
	if err := k.Delete(ctx, testService, testAccount); err != nil {
		t.Errorf("Delete: %v", err)
	}
}

func TestKeychain_SetReplacesTheValue(t *testing.T) {
	k := newTestKeychain(t)
	ctx := context.Background()

	for _, value := range []string{"first-value", "second-value"} {
		if err := k.Set(ctx, testService, testAccount, []byte(value)); err != nil {
			t.Fatalf("Set(%s): %v", value, err)
		}
	}
	got, err := k.Get(ctx, testService, testAccount)
	if err != nil || string(got) != "second-value" {
		t.Errorf("Get = %q, %v, want second-value", got, err)
	}
}

func TestKeychain_MissingItemIsErrNotFound(t *testing.T) {
	k := newTestKeychain(t)
	ctx := context.Background()

	if _, err := k.Get(ctx, testService, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get: err = %v, want ErrNotFound", err)
	}
	if _, err := k.GetBase64(ctx, testService, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetBase64: err = %v, want ErrNotFound", err)
	}
	if err := k.Delete(ctx, testService, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete: err = %v, want ErrNotFound", err)
	}
}

func TestKeychain_DeleteRemovesTheItem(t *testing.T) {
	k := newTestKeychain(t)
	ctx := context.Background()

	if err := k.Set(ctx, testService, testAccount, []byte("value")); err != nil {
		t.Fatal(err)
	}
	if err := k.Delete(ctx, testService, testAccount); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := k.Get(ctx, testService, testAccount); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete: err = %v, want ErrNotFound", err)
	}
}

// Observed on macOS: the security command writes to the default keychain when
// the keychain file does not exist, and reports success. The adapter must stop
// before it runs the command.
func TestKeychain_MissingKeychainFileIsAnError(t *testing.T) {
	k := Open(filepath.Join(t.TempDir(), "does-not-exist.keychain-db"))
	ctx := context.Background()

	if err := k.Set(ctx, testService, testAccount, []byte("value")); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("Set: err = %v, want an error about the keychain file", err)
	}
	if _, err := k.Get(ctx, testService, testAccount); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("Get: err = %v, want an error about the keychain file", err)
	}
	if err := k.Delete(ctx, testService, testAccount); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("Delete: err = %v, want an error about the keychain file", err)
	}
}

// Get and Delete must name the keychain file. Without it, the security command
// searches every keychain in the search list.
func TestItemArgs_NameTheKeychainFile(t *testing.T) {
	args, err := Open("/tmp/test.keychain-db").itemArgs("find-generic-password", testService, testAccount, "-w")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"find-generic-password", "-s", testService, "-a", testAccount, "-w", "/tmp/test.keychain-db"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("args = %q, want %q", args, want)
	}
}

func TestParseDefaultKeychain(t *testing.T) {
	got, err := parseDefaultKeychain([]byte("    \"/Users/example/Library/Keychains/login.keychain-db\"\n"))
	if err != nil || got != "/Users/example/Library/Keychains/login.keychain-db" {
		t.Errorf("got %q, %v", got, err)
	}
	for name, out := range map[string]string{
		"empty":      "",
		"no quotes":  "/Users/example/login.keychain-db\n",
		"two lines":  "    \"/a\"\n    \"/b\"\n",
		"empty path": "    \"\"\n",
	} {
		if _, err := parseDefaultKeychain([]byte(out)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// Default only asks for the path. It reads and writes no item.
func TestDefault_ReturnsAnExistingKeychainFile(t *testing.T) {
	if _, err := os.Stat(securityPath); err != nil {
		t.Skipf("%s does not exist: the Keychain is a macOS feature", securityPath)
	}
	k, err := Default(context.Background())
	if err != nil {
		t.Fatalf("Default: %v", err)
	}
	if err := k.checkFile(); err != nil {
		t.Errorf("the default keychain: %v", err)
	}
}

func TestCommandError_HidesTheSecret(t *testing.T) {
	secret := []byte("c2VjcmV0LXZhbHVl")
	stderr := []byte("security: something failed for c2VjcmV0LXZhbHVl\nadd-generic-password: returned -1\n")

	err := commandError("store", testService, testAccount, errors.New("exit status 1"), stderr, secret)
	if strings.Contains(err.Error(), string(secret)) {
		t.Errorf("the error holds the secret: %v", err)
	}
	for _, want := range []string{"store", testService, testAccount, "exit status 1", "[redacted]"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "returned -1") {
		t.Errorf("err = %q, want only the first line of stderr", err)
	}
}

// This test needs no macOS: it checks how the command is built.
func TestSetCommand_KeepsTheSecretOutOfTheArguments(t *testing.T) {
	secret := "c2VjcmV0LXZhbHVl"
	args, stdin, err := Open("/tmp/with space/test.keychain-db").setCommand(testService, testAccount, []byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 1 || args[0] != "-i" {
		t.Errorf("args = %q, want only -i", args)
	}
	if strings.Contains(strings.Join(args, " "), secret) {
		t.Error("the arguments hold the secret")
	}
	want := `add-generic-password -U -s "cumin-works-test" -a "github-app-private-key/Iv23liEXAMPLEclientid" -w c2VjcmV0LXZhbHVl "/tmp/with space/test.keychain-db"` + "\n"
	if string(stdin) != want {
		t.Errorf("stdin = %q, want %q", stdin, want)
	}
}

func TestSetCommand_WritesANonASCIIPathAsItIs(t *testing.T) {
	_, stdin, err := Open("/Users/山田 太郎/Library/Keychains/login.keychain-db").setCommand(testService, testAccount, []byte("value"))
	if err != nil {
		t.Fatal(err)
	}
	if want := ` "/Users/山田 太郎/Library/Keychains/login.keychain-db"` + "\n"; !strings.HasSuffix(string(stdin), want) {
		t.Errorf("stdin = %q, want the suffix %q", stdin, want)
	}
}

func TestSetCommand_RejectsAPathThatTheCommandLineCannotCarry(t *testing.T) {
	for name, path := range map[string]string{
		"double quote":  `/tmp/a"b.keychain-db`,
		"backslash":     `/tmp/a\b.keychain-db`,
		"line break":    "/tmp/a\nadd-generic-password.keychain-db",
		"invalid UTF-8": "/tmp/\xff.keychain-db",
		"empty":         "",
	} {
		if _, _, err := Open(path).setCommand(testService, testAccount, []byte("value")); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestSetCommand_RejectsValuesThatTheCommandLineCannotCarry(t *testing.T) {
	k := Open("/tmp/test.keychain-db")
	for name, c := range map[string]struct{ service, account, secret string }{
		"secret with a line break":   {testService, testAccount, "line one\nline two"},
		"secret with a space":        {testService, testAccount, "two words"},
		"secret with a quote":        {testService, testAccount, `a"b`},
		"secret with a backslash":    {testService, testAccount, `a\b`},
		"empty secret":               {testService, testAccount, ""},
		"empty service":              {"", testAccount, "value"},
		"account with a quote":       {testService, `a"b`, "value"},
		"account with a line break":  {testService, "a\nadd-generic-password", "value"},
		"service with a backslash":   {`a\b`, testAccount, "value"},
		"account with a non-ASCII é": {testService, "café", "value"},
	} {
		if _, _, err := k.setCommand(c.service, c.account, []byte(c.secret)); err == nil {
			t.Errorf("%s: no error", name)
		} else if c.secret != "" && strings.Contains(err.Error(), c.secret) {
			t.Errorf("%s: the error holds the secret", name)
		}
	}
}
