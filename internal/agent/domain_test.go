package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The tests cover the fifth requirement of #7: the result is validated
// against the schema in agents/common.md, and "blocked" needs a reason.

func TestValidateResult(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    Result
		wantErr string // empty means valid
	}{
		{
			name: "valid done",
			raw:  `{"result":"done","summary":"I added the test.","blocked_reason":""}`,
			want: Result{Result: "done", Summary: "I added the test."},
		},
		{
			name: "valid blocked",
			raw:  `{"result":"blocked","summary":"I stopped.","blocked_reason":"## Decision needed: which sign-in method?"}`,
			want: Result{Result: "blocked", Summary: "I stopped.", BlockedReason: "## Decision needed: which sign-in method?"},
		},
		{
			name:    "blocked with an empty reason",
			raw:     `{"result":"blocked","summary":"I stopped.","blocked_reason":""}`,
			wantErr: `"blocked" with an empty "blocked_reason"`,
		},
		{
			name:    "blocked with a blank reason",
			raw:     `{"result":"blocked","summary":"I stopped.","blocked_reason":" \n"}`,
			wantErr: `"blocked" with an empty "blocked_reason"`,
		},
		{
			name:    "missing property",
			raw:     `{"result":"done","summary":"x"}`,
			wantErr: `no property "blocked_reason"`,
		},
		{
			name:    "additional property",
			raw:     `{"result":"done","summary":"x","blocked_reason":"","extra":1}`,
			wantErr: `additional property "extra"`,
		},
		{
			name:    "result outside the enum",
			raw:     `{"result":"finished","summary":"x","blocked_reason":""}`,
			wantErr: `"result" is "finished"`,
		},
		{
			name:    "wrong type",
			raw:     `{"result":"done","summary":42,"blocked_reason":""}`,
			wantErr: `"summary" is not a string`,
		},
		{
			name:    "null property",
			raw:     `{"result":"done","summary":"x","blocked_reason":null}`,
			wantErr: `"blocked_reason" is not a string`,
		},
		{
			name:    "not an object",
			raw:     `["done"]`,
			wantErr: "not a JSON object",
		},
		{
			name:    "null",
			raw:     `null`,
			wantErr: "not a JSON object",
		},
		{
			name:    "empty",
			raw:     ``,
			wantErr: "not a JSON object",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateResult(json.RawMessage(tt.raw))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateResult: %v", err)
				}
				if got != tt.want {
					t.Errorf("ValidateResult = %+v, want %+v", got, tt.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateResult = %+v, want an error with %q", got, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// The schema in the code must stay equal to the schema in the requirement.
func TestResultSchemaMatchesCommonDocument(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "ja", "requirements", "agents", "common.md"))
	if err != nil {
		t.Fatal(err)
	}
	var documented any
	rest := string(doc)
	for {
		var block string
		var found bool
		_, rest, found = strings.Cut(rest, "```json\n")
		if !found {
			t.Fatal("common.md has no json block with a schema")
		}
		block, rest, _ = strings.Cut(rest, "```")
		var v map[string]any
		if err := json.Unmarshal([]byte(block), &v); err != nil {
			continue
		}
		if _, ok := v["properties"]; ok {
			documented = v
			break
		}
	}
	var inCode any
	if err := json.Unmarshal([]byte(ResultSchema), &inCode); err != nil {
		t.Fatalf("ResultSchema is not JSON: %v", err)
	}
	if !reflect.DeepEqual(inCode, documented) {
		t.Errorf("ResultSchema differs from common.md:\ncode: %s\ndocument: %v", ResultSchema, documented)
	}
}

func TestAbnormalEndError(t *testing.T) {
	e := &AbnormalEnd{Kind: EndInvalidResult, SessionID: "s", Detail: "the result has no property \"summary\""}
	if got := e.Error(); got != `abnormal end (invalid result): the result has no property "summary"` {
		t.Errorf("Error() = %q", got)
	}
	for k := EndTimeLimit; k <= EndInvalidResult; k++ {
		if strings.HasPrefix(k.String(), "EndKind(") {
			t.Errorf("EndKind %d has no name", int(k))
		}
	}
}
