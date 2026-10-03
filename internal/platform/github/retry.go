package github

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// maxRetries is how many times a read is sent again after a temporary
	// failure. With the first try, a read is sent 4 times at most.
	maxRetries = 3
	// retryWait is the time between two tries.
	retryWait = 2 * time.Second
)

// TemporaryError is a failure of a call that can pass by itself: a network
// error or a 5xx answer. Err is the error of the last try. A caller tells
// it from a lasting failure with IsTemporary.
type TemporaryError struct {
	Err error
}

func (e *TemporaryError) Error() string { return e.Err.Error() }

func (e *TemporaryError) Unwrap() error { return e.Err }

// IsTemporary reports whether err is a failure of a call to GitHub that can
// pass by itself. The same call may succeed later. A full rate limit and a
// secondary rate limit (RateLimitError) are temporary too: they pass at
// their reset time.
func IsTemporary(err error) bool {
	var temporary *TemporaryError
	return errors.As(err, &temporary) || isRateLimit(err)
}

// SetLogger sets the logger of the retries. Without it, the client logs to
// the default logger.
func (c *AppClient) SetLogger(logger *slog.Logger) {
	c.logger = logger
}

// SetRetryWait replaces the wait between two tries. Tests pass a wait that
// does not sleep. The wait returns an error when the context ends first.
func (c *AppClient) SetRetryWait(wait func(ctx context.Context, d time.Duration) error) {
	c.wait = wait
}

// sleep waits for d. A cancelled context ends the wait at once.
func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// retry sends one call, and sends a read again after a temporary failure,
// up to maxRetries times. A write is sent once: a write whose answer was
// lost may have happened, and a second one could write the same thing twice.
// Each try is a new request with its own deadline. A full rate limit and a
// secondary rate limit are not tried again: a call before the reset time
// fails again.
func (c *AppClient) retry(ctx context.Context, method, label string, read bool, send func() error) error {
	for try := 1; ; try++ {
		err := send()
		if err == nil || !read || !IsTemporary(err) || isRateLimit(err) || try > maxRetries {
			return err
		}
		logger := c.logger
		if logger == nil {
			logger = slog.Default()
		}
		logger.Warn("try a call to GitHub again", "request", method+" "+label, "try", try, "reason", retryReason(err))
		if c.wait(ctx, retryWait) != nil {
			// The caller gave up. The failure of the try is no longer
			// the reason, and a later call is not what the caller wants.
			return errors.New(method + " " + label + ": " + ctx.Err().Error())
		}
	}
}

// isRead reports whether a request only reads: a GET, or a GraphQL query.
// A GraphQL mutation is a write.
func isRead(method, path string, body any) bool {
	if method == http.MethodGet {
		return true
	}
	if method != http.MethodPost || path != "/graphql" {
		return false
	}
	request, _ := body.(map[string]any)
	query, _ := request["query"].(string)
	return strings.HasPrefix(strings.TrimSpace(query), "query")
}

// retryReason is the reason of a retry for the log. It holds the status
// code or the cause of the network error, and no address and no token.
func retryReason(err error) string {
	var status *StatusError
	if errors.As(err, &status) {
		return "status " + strconv.Itoa(status.Status)
	}
	// The text of a net.OpError holds the addresses of both ends.
	var op *net.OpError
	if errors.As(err, &op) {
		return op.Op + ": " + op.Err.Error()
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "time-out"
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return "the connection closed"
	}
	return "network error"
}

// networkError wraps the error of a request that got no whole answer. It is
// temporary, unless the caller ended the context.
func networkError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return err
	}
	return &TemporaryError{Err: err}
}

// bodyReader remembers the error of a read, so that a caller can tell a
// broken connection from a body that is not what the caller wanted.
type bodyReader struct {
	r   io.Reader
	err error
}

func (b *bodyReader) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		b.err = err
	}
	return n, err
}
