package setup

// This file is `cumin setup notify`: it puts the address of a notification
// channel into the Keychain of the Host. The address is a secret, so it is
// read from standard input, never from an argument, and it never reaches
// the output.
//
// docs/ja/designs/cumin-core.md, the topics on the Keychain items and on
// notifications to the Owner.

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/cloveclovedev/cumin-works/internal/platform/keychain"
)

// NotifyStore stores the address of a channel. *keychain.Keychain is the
// real one. The interface exists because the acceptance tests must run
// without macOS, as SecretStore does for the private keys.
type NotifyStore interface {
	Set(ctx context.Context, service, account string, secret []byte) error
	Get(ctx context.Context, service, account string) ([]byte, error)
}

// Channel is one way of telling the Owner that cumin needs attention.
// v0.1 has one; another channel adds its own flag and its own item.
type Channel struct {
	// Flag is the flag of `cumin setup notify` that names the channel.
	Flag string
	// Account is the Keychain account that holds the address.
	Account string
	// What names the address for the person at the terminal.
	What string
	// Setting is the settings key that turns this channel on and off.
	Setting string
}

// DiscordWebhook is the channel of v0.1.
var DiscordWebhook = Channel{
	Flag:    "discord-webhook",
	Account: keychain.DiscordWebhookAccount,
	What:    "Discord webhook address",
	Setting: "notify.discord.enabled",
}

// Channels are the channels that the command knows.
func Channels() []Channel { return []Channel{DiscordWebhook} }

// CheckNotifyAddress refuses an address that cumin cannot use. The error
// never holds the address: a mistyped address is still a secret.
func CheckNotifyAddress(address string) error {
	if address == "" {
		return errors.New("the address is empty")
	}
	parsed, err := url.Parse(address)
	if err != nil {
		return errors.New("the address is not an address")
	}
	if parsed.Scheme != "https" {
		return errors.New("the address must start with https://")
	}
	if parsed.Host == "" {
		return errors.New("the address has no host")
	}
	if strings.Trim(parsed.Path, "/") == "" {
		return errors.New("the address has no path")
	}
	return nil
}

// Prompt says whether to ask for the address, and what to promise about
// the echo of the terminal. The caller knows: it holds the terminal.
type Prompt int

const (
	// NoPrompt reads the address without a word. Standard input is a pipe.
	NoPrompt Prompt = iota
	// PromptHidden asks for the address on a terminal whose echo is off.
	PromptHidden
	// PromptVisible asks on a terminal whose echo could not be turned off.
	// The address will appear on the screen, and the prompt says so.
	PromptVisible
)

// ReadAddress reads one address from in, with the prompt that the caller
// chose. The address is trimmed of spaces. Nothing here echoes it.
func ReadAddress(in io.Reader, out io.Writer, channel Channel, prompt Prompt) (string, error) {
	ask := prompt != NoPrompt
	if ask {
		fmt.Fprintf(out, "Paste the %s, then press Enter.\n", channel.What)
		if prompt == PromptHidden {
			fmt.Fprint(out, "It is not shown while you type, and it is never written to a file or to a log.\n> ")
		} else {
			fmt.Fprint(out, "This terminal shows what you paste. It is never written to a file or to a log.\n> ")
		}
	}
	scanner := bufio.NewScanner(in)
	// A webhook address is one line and far below this; the limit stops a
	// stream that holds no newline at all.
	scanner.Buffer(make([]byte, 0, 4096), 64<<10)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", fmt.Errorf("read the address: %w", err)
		}
		return "", errors.New("read the address: nothing was given")
	}
	if ask {
		fmt.Fprintln(out)
	}
	return strings.TrimSpace(scanner.Text()), nil
}

// StoreNotifyAddress puts the address of the channel into the Keychain and
// reads it back, so that the Owner learns at once when the item did not
// take. It writes what happened to out, never the address.
func StoreNotifyAddress(ctx context.Context, store NotifyStore, channel Channel, address string, out io.Writer) error {
	if err := CheckNotifyAddress(address); err != nil {
		return fmt.Errorf("the %s: %w", channel.What, err)
	}
	if err := store.Set(ctx, keychain.Service, channel.Account, []byte(address)); err != nil {
		return fmt.Errorf("store the %s: %w", channel.What, err)
	}
	stored, err := store.Get(ctx, keychain.Service, channel.Account)
	if err != nil {
		return fmt.Errorf("read the %s back: %w", channel.What, err)
	}
	if !bytes.Equal(bytes.TrimSpace(stored), []byte(address)) {
		return fmt.Errorf("the %s that came back from the Keychain is not the one that was stored", channel.What)
	}
	fmt.Fprintf(out, "stored: the %s is in the Keychain (service %s, account %s)\n",
		channel.What, keychain.Service, channel.Account)
	fmt.Fprintf(out, "cumin notifies about a repository while %s is true, which is the default.\n", channel.Setting)
	fmt.Fprintf(out, "A repository can set it in its .cumin/config.toml.\n")
	return nil
}
