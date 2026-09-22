package notify

// This file is the entry of the package: the notifier that the rest of
// cumin calls, and the sender that one channel implements.

import (
	"context"
	"errors"
)

// Sender takes the finished text of one notification to one channel.
// internal/platform/discord implements it. The value is plain text, not a
// type of this package, so that a provider package never imports a feature
// package (docs/ja/designs/code-layout.md, the topic on the direction of
// the dependencies).
type Sender interface {
	Send(ctx context.Context, text string) error
}

// ErrNoSender says that no channel is configured. The caller logs it; it
// never undoes what cumin already wrote on GitHub.
var ErrNoSender = errors.New("notify: no sender is configured")

// Notifier sends one notification through its sender.
type Notifier struct {
	Sender Sender
}

// New returns a notifier that sends through sender.
func New(sender Sender) *Notifier { return &Notifier{Sender: sender} }

// Notify sends one notification. It returns the error of the sender
// unchanged, so that the caller can log it with its own fields. A nil
// notifier and a notifier without a sender return ErrNoSender.
func (n *Notifier) Notify(ctx context.Context, notification Notification) error {
	if n == nil || n.Sender == nil {
		return ErrNoSender
	}
	return n.Sender.Send(ctx, Message(notification))
}
