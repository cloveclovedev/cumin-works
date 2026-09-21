// Package agent starts an agent for one request and returns its result.
//
// This file holds the types that the rest of cumin sees: the request, the
// result, the quota usage, the run, and the abnormal end. Everything that
// is specific to one CLI stays in the adapter of that CLI. The result
// format and its schema come from docs/ja/requirements/agents/common.md.
package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
)

// Request is one request to an agent.
type Request struct {
	Role config.Role
	// RoleInstruction is the instruction of the role, from roles/.
	RoleInstruction string
	// Text is the request text: what the agent must do this time.
	Text string
	// WorkDir is the worktree that the agent runs in.
	WorkDir string
	// SessionID continues an earlier session. Empty starts a new session.
	SessionID string
	// Model is the model of the CLI. Empty uses the default of the CLI.
	Model string
	// TimeLimit is the run time limit of the role.
	TimeLimit time.Duration
	// Credentials are the token and the commit identity of the role. Run
	// refuses a request without them.
	Credentials Credentials
}

// Result values.
const (
	ResultDone    = "done"
	ResultBlocked = "blocked"
)

// Result is the JSON that an agent returns at the end of a run.
type Result struct {
	Result        string `json:"result"`
	Summary       string `json:"summary"`
	BlockedReason string `json:"blocked_reason"`
}

// ResultSchema is the JSON Schema of Result. It is the schema block in
// docs/ja/requirements/agents/common.md. A test keeps the two equal.
const ResultSchema = `{
  "type": "object",
  "properties": {
    "result": { "type": "string", "enum": ["done", "blocked"] },
    "summary": { "type": "string" },
    "blocked_reason": { "type": "string" }
  },
  "required": ["result", "summary", "blocked_reason"],
  "additionalProperties": false
}`

// resultProperties are the properties of ResultSchema, all required, all
// strings. ValidateResult checks the schema by hand, so that no JSON
// Schema library is needed.
var resultProperties = []string{"result", "summary", "blocked_reason"}

// ValidateResult checks raw against ResultSchema and the rule that
// "blocked" needs a reason. The error names the rule that failed.
func ValidateResult(raw json.RawMessage) (Result, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return Result{}, errors.New("the result is not a JSON object")
	}

	fields := map[string]string{}
	for _, name := range resultProperties {
		value, ok := object[name]
		if !ok {
			return Result{}, fmt.Errorf("the result has no property %q", name)
		}
		// A pointer tells a JSON null from an empty string.
		var s *string
		if err := json.Unmarshal(value, &s); err != nil || s == nil {
			return Result{}, fmt.Errorf("the property %q is not a string", name)
		}
		fields[name] = *s
	}
	for name := range object {
		if !contains(resultProperties, name) {
			return Result{}, fmt.Errorf("the result has an additional property %q", name)
		}
	}

	r := Result{Result: fields["result"], Summary: fields["summary"], BlockedReason: fields["blocked_reason"]}
	if r.Result != ResultDone && r.Result != ResultBlocked {
		return Result{}, fmt.Errorf("the property \"result\" is %q, not %q or %q", r.Result, ResultDone, ResultBlocked)
	}
	if r.Result == ResultBlocked && strings.TrimSpace(r.BlockedReason) == "" {
		return Result{}, errors.New("the result is \"blocked\" with an empty \"blocked_reason\"")
	}
	return r, nil
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

// QuotaWindow is the usage of one quota window.
type QuotaWindow struct {
	// Utilization is the usage from 0 to 1.
	Utilization float64
	ResetsAt    time.Time
}

// QuotaUsage is the usage of the two quota windows, as read from one run.
type QuotaUsage struct {
	FiveHour QuotaWindow
	Weekly   QuotaWindow
	// ReadAt is when the usage was read.
	ReadAt time.Time
}

// Run is the normal end of a request.
type Run struct {
	SessionID string
	Result    Result
	// Quota is the quota usage that the run reported. QuotaRead is false
	// when the run reported none; then Quota is empty.
	Quota     QuotaUsage
	QuotaRead bool
}

// EndKind says why a run ended abnormally.
type EndKind int

const (
	// EndTimeLimit: cumin stopped the run at the time limit, or the
	// context was cancelled.
	EndTimeLimit EndKind = iota + 1
	// EndProcessFailed: the CLI did not start, or exited with a non-zero code.
	EndProcessFailed
	// EndNoResult: the CLI exited without a result event.
	EndNoResult
	// EndError: the CLI reported an error in its result event.
	EndError
	// EndInvalidResult: the result does not match ResultSchema, or is
	// "blocked" without a reason.
	EndInvalidResult
)

func (k EndKind) String() string {
	switch k {
	case EndTimeLimit:
		return "time limit"
	case EndProcessFailed:
		return "process failed"
	case EndNoResult:
		return "no result"
	case EndError:
		return "error reported by the CLI"
	case EndInvalidResult:
		return "invalid result"
	}
	return fmt.Sprintf("EndKind(%d)", int(k))
}

// AbnormalEnd is the error of a run that did not end normally. The rest
// of cumin retries once on an abnormal end (a later requirement).
type AbnormalEnd struct {
	Kind EndKind
	// SessionID is set when the run reported one before the end.
	SessionID string
	// PID is the process of the CLI. 0 when the CLI did not start.
	PID int
	// Detail is a short reason for a log or a comment. It holds no quota
	// number and no secret.
	Detail string
	// Err is the underlying error, when there is one.
	Err error
}

func (e *AbnormalEnd) Error() string {
	msg := "abnormal end (" + e.Kind.String() + ")"
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *AbnormalEnd) Unwrap() error { return e.Err }
