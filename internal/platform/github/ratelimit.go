package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RateLimitError says that the primary rate limit of GitHub is full, or that
// GitHub answered a secondary rate limit. The client sends no call of the
// same installation before Reset. It holds no token. It is a temporary
// failure (IsTemporary), and it is never tried again at once: the caller
// tries again after Reset.
type RateLimitError struct {
	Method, Label string
	// Resource is the rate limit that is full, as the header
	// x-ratelimit-resource names it: "core" for REST, "graphql" for GraphQL.
	// It is "secondary" for a secondary rate limit.
	Resource string
	// Reset is the time at which GitHub gives a new limit. For a secondary
	// rate limit, it is the end of the wait that GitHub asks for.
	Reset time.Time
}

func (e *RateLimitError) Error() string {
	if e.Resource == secondaryResource {
		return fmt.Sprintf("%s %s: GitHub answered a secondary rate limit, no call until %s",
			e.Method, e.Label, e.Reset.UTC().Format(time.RFC3339))
	}
	return fmt.Sprintf("%s %s: the rate limit of GitHub (%s) is full until %s",
		e.Method, e.Label, e.Resource, e.Reset.UTC().Format(time.RFC3339))
}

// isRateLimit reports whether err is a full rate limit.
func isRateLimit(err error) bool {
	var limit *RateLimitError
	return errors.As(err, &limit)
}

// rateLimits remembers which installations have a full rate limit or a
// secondary rate limit. It is
// safe for concurrent use.
type rateLimits struct {
	mu sync.Mutex
	// installations is the installation of each token that the client
	// created. Several tokens of one installation share one rate limit.
	installations map[string]tokenInstallation
	// full is the full rate limit of each caller, until its reset time.
	full map[string]fullRateLimit
}

type tokenInstallation struct {
	id        int64
	expiresAt time.Time
}

type fullRateLimit struct {
	resource string
	reset    time.Time
}

// rememberInstallation records the installation of a token that the client
// created, and forgets the tokens that have expired.
func (c *AppClient) rememberInstallation(token string, id int64, expiresAt time.Time) {
	now := c.now()
	c.limits.mu.Lock()
	defer c.limits.mu.Unlock()
	if c.limits.installations == nil {
		c.limits.installations = map[string]tokenInstallation{}
	}
	for known, installation := range c.limits.installations {
		if !now.Before(installation.expiresAt) {
			delete(c.limits.installations, known)
		}
	}
	c.limits.installations[token] = tokenInstallation{id: id, expiresAt: expiresAt}
}

// caller names who a rate limit counts for: the installation of the token.
// A token that the client did not create stands for itself. The JWT of an
// App is new for every call, so a call with a JWT is always sent. The
// caller must hold the lock.
func (l *rateLimits) caller(token string) string {
	if installation, ok := l.installations[token]; ok {
		return "installation " + strconv.FormatInt(installation.id, 10)
	}
	return token
}

// fullRateLimit returns a RateLimitError when the installation of the token
// has a full rate limit, or a secondary rate limit, whose reset time has not
// come. The call is then not sent.
func (c *AppClient) fullRateLimit(token, method, label string) error {
	now := c.now()
	c.limits.mu.Lock()
	defer c.limits.mu.Unlock()
	limit, ok := c.limits.full[c.limits.caller(token)]
	if !ok || !now.Before(limit.reset) {
		return nil
	}
	return &RateLimitError{Method: method, Label: label, Resource: limit.resource, Reset: limit.reset}
}

// secondaryWait is how long the client sends no call after a secondary rate
// limit whose answer has no retry-after header. GitHub Docs say: wait for at
// least one minute.
const secondaryWait = time.Minute

// secondaryResource is the Resource of a RateLimitError of a secondary rate
// limit. A secondary rate limit has no header x-ratelimit-resource.
const secondaryResource = "secondary"

// secondarySignal is the text of the error message of GitHub that names a
// secondary rate limit.
const secondarySignal = "secondary rate limit"

// rateLimited reads the rate limit signals of an answer that failed: the
// headers, and the error message of GitHub. When they say a rate limit, it
// remembers the time to wait for the installation of the token, logs one
// line at warn level, and returns a RateLimitError. Otherwise it returns nil.
//
// GitHub Docs, "Rate limits for the REST API" and "Rate limits and query
// limits for the GraphQL API", in the order of "Exceeding the rate limit":
//   - The header retry-after holds the seconds to wait. Only a secondary
//     rate limit has it.
//   - A full primary rate limit has the header x-ratelimit-remaining with
//     0, and x-ratelimit-reset holds the reset time in UTC epoch seconds.
//   - Otherwise an error message that names a secondary rate limit means:
//     wait for at least one minute.
func (c *AppClient) rateLimited(token, method, label string, header http.Header, message string) error {
	now := c.now()
	var limit fullRateLimit
	if seconds, err := strconv.ParseInt(header.Get("retry-after"), 10, 64); err == nil && seconds > 0 {
		limit = fullRateLimit{resource: secondaryResource, reset: now.Add(time.Duration(seconds) * time.Second).UTC()}
	} else if seconds, err := strconv.ParseInt(header.Get("x-ratelimit-reset"), 10, 64); err == nil && header.Get("x-ratelimit-remaining") == "0" {
		limit = fullRateLimit{resource: header.Get("x-ratelimit-resource"), reset: time.Unix(seconds, 0).UTC()}
	} else if strings.Contains(message, secondarySignal) {
		limit = fullRateLimit{resource: secondaryResource, reset: now.Add(secondaryWait).UTC()}
	} else {
		return nil
	}

	c.limits.mu.Lock()
	if c.limits.full == nil {
		c.limits.full = map[string]fullRateLimit{}
	}
	for caller, known := range c.limits.full {
		if !now.Before(known.reset) {
			delete(c.limits.full, caller)
		}
	}
	c.limits.full[c.limits.caller(token)] = limit
	c.limits.mu.Unlock()

	logger := c.logger
	if logger == nil {
		logger = slog.Default()
	}
	found := "the rate limit of GitHub is full"
	if limit.resource == secondaryResource {
		found = "GitHub answered a secondary rate limit"
	}
	logger.Warn(found, "request", method+" "+label,
		"resource", limit.resource, "reset", limit.reset.Format(time.RFC3339))
	return &RateLimitError{Method: method, Label: label, Resource: limit.resource, Reset: limit.reset}
}

// graphQLErrorMessages returns the messages of the errors in the body of a GraphQL
// answer, joined by a new line, and whether the body holds an error.
func graphQLErrorMessages(body []byte) (string, bool) {
	var answer struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if json.Unmarshal(body, &answer) != nil || len(answer.Errors) == 0 {
		return "", false
	}
	messages := make([]string, len(answer.Errors))
	for i, e := range answer.Errors {
		messages[i] = e.Message
	}
	return strings.Join(messages, "\n"), true
}
