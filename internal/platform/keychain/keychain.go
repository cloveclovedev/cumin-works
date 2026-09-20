// Package keychain stores secrets in the macOS Keychain as generic passwords.
// It calls the security command of macOS, so it needs no cgo and no dependency.
//
// docs/ja/designs/cumin-core.md lists the items that cumin keeps.
package keychain

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const securityPath = "/usr/bin/security"

// Exit code of the security command when the item does not exist
// (errSecItemNotFound). Observed on macOS, together with the message
// "The specified item could not be found in the keychain."
const exitItemNotFound = 44

// ErrNotFound reports that the Keychain has no item with the service and the
// account.
var ErrNotFound = errors.New("keychain: item not found")

// Keychain is one keychain file, or the default keychain of the user.
type Keychain struct {
	path string
}

// Default returns the default keychain of the user (the login keychain).
func Default() *Keychain { return &Keychain{} }

// Open returns the keychain file at path. Tests use a temporary keychain.
func Open(path string) *Keychain { return &Keychain{path: path} }

// checkFile stops every operation on a keychain file that does not exist.
// Observed on macOS: "security add-generic-password" with the path of a
// missing file reports success and writes to the default keychain instead.
func (k *Keychain) checkFile() error {
	if k.path == "" {
		return nil
	}
	if _, err := os.Stat(k.path); err != nil {
		return fmt.Errorf("keychain: the keychain file does not exist: %w", err)
	}
	return nil
}

// Set stores the secret, and replaces an existing item with the same service
// and account.
//
// The secret reaches the security command through standard input, never
// through an argument, because every user can read the arguments of a
// process. The secret must be one line of printable ASCII with no space, quote,
// or backslash. Use SetBase64 for any other value.
func (k *Keychain) Set(ctx context.Context, service, account string, secret []byte) error {
	if err := k.checkFile(); err != nil {
		return err
	}
	args, stdin, err := k.setCommand(service, account, secret)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, securityPath, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return commandError("store", service, account, err, stderr.Bytes(), secret)
	}
	return nil
}

// Get reads the secret. It returns ErrNotFound when the item does not exist.
func (k *Keychain) Get(ctx context.Context, service, account string) ([]byte, error) {
	if err := k.checkFile(); err != nil {
		return nil, err
	}
	args, err := k.itemArgs("find-generic-password", service, account, "-w")
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, securityPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, commandError("read", service, account, err, stderr.Bytes(), stdout.Bytes())
	}
	return bytes.TrimSuffix(stdout.Bytes(), []byte("\n")), nil
}

// Delete removes the item. It returns ErrNotFound when the item does not exist.
func (k *Keychain) Delete(ctx context.Context, service, account string) error {
	if err := k.checkFile(); err != nil {
		return err
	}
	args, err := k.itemArgs("delete-generic-password", service, account)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, securityPath, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr // the command prints the attributes of the item on stdout; drop them
	if err := cmd.Run(); err != nil {
		return commandError("delete", service, account, err, stderr.Bytes(), nil)
	}
	return nil
}

// SetBase64 stores a value that can hold line breaks, such as a PEM, as base64
// on one line. The security command returns a value with line breaks as
// hexadecimal, so cumin never stores one as it is.
func (k *Keychain) SetBase64(ctx context.Context, service, account string, value []byte) error {
	encoded := make([]byte, base64.StdEncoding.EncodedLen(len(value)))
	base64.StdEncoding.Encode(encoded, value)
	return k.Set(ctx, service, account, encoded)
}

// GetBase64 reads a value that SetBase64 stored.
func (k *Keychain) GetBase64(ctx context.Context, service, account string) ([]byte, error) {
	encoded, err := k.Get(ctx, service, account)
	if err != nil {
		return nil, err
	}
	value := make([]byte, base64.StdEncoding.DecodedLen(len(encoded)))
	n, err := base64.StdEncoding.Decode(value, encoded)
	if err != nil {
		// The error of the decoder holds only a position, not the value.
		return nil, fmt.Errorf("keychain: item %s %s is not base64: %w", service, account, err)
	}
	return value[:n], nil
}

// setCommand returns the arguments and the standard input of the security
// command that stores the secret. In interactive mode (-i) the command reads
// one command line from standard input, so the arguments hold no secret.
func (k *Keychain) setCommand(service, account string, secret []byte) (args []string, stdin []byte, err error) {
	if err := checkName("service", service); err != nil {
		return nil, nil, err
	}
	if err := checkName("account", account); err != nil {
		return nil, nil, err
	}
	if len(secret) == 0 {
		return nil, nil, errors.New("keychain: the secret is empty")
	}
	for _, c := range secret {
		if c <= ' ' || c > '~' || c == '"' || c == '\'' || c == '\\' {
			return nil, nil, errors.New("keychain: the secret must be one line of printable ASCII with no space, quote, or backslash")
		}
	}
	var line strings.Builder
	fmt.Fprintf(&line, "add-generic-password -U -s %q -a %q -w %s", service, account, secret)
	if k.path != "" {
		if err := checkName("keychain path", k.path); err != nil {
			return nil, nil, err
		}
		fmt.Fprintf(&line, " %q", k.path)
	}
	line.WriteString("\n")
	return []string{"-i"}, []byte(line.String()), nil
}

func (k *Keychain) itemArgs(command, service, account string, flags ...string) ([]string, error) {
	if err := checkName("service", service); err != nil {
		return nil, err
	}
	if err := checkName("account", account); err != nil {
		return nil, err
	}
	args := append([]string{command, "-s", service, "-a", account}, flags...)
	if k.path != "" {
		args = append(args, k.path)
	}
	return args, nil
}

// checkName rejects a value that the quoting of the interactive mode cannot
// carry. The names that cumin uses are plain ASCII.
func checkName(what, value string) error {
	if value == "" {
		return fmt.Errorf("keychain: the %s is empty", what)
	}
	for _, c := range value {
		if c < ' ' || c > '~' || c == '"' || c == '\\' {
			return fmt.Errorf("keychain: the %s must be printable ASCII with no double quote and no backslash", what)
		}
	}
	return nil
}

// commandError builds an error that never holds the secret. The security
// command does not print the secret on failure, and the error text is
// scrubbed as well.
func commandError(action, service, account string, err error, stderr, secret []byte) error {
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == exitItemNotFound {
		return fmt.Errorf("%w: %s %s", ErrNotFound, service, account)
	}
	message := strings.TrimSpace(string(stderr))
	if first, _, found := strings.Cut(message, "\n"); found {
		message = first
	}
	if len(secret) > 0 {
		message = strings.ReplaceAll(message, string(secret), "[redacted]")
	}
	return fmt.Errorf("keychain: %s item %s %s: %v: %s", action, service, account, err, message)
}
