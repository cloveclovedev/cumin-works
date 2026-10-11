package scripts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The names that scripts/tailscale/policy.hujson, the template of the
// tailnet policy, must use. The placeholder stands for the login of a
// member; the script of the policy repository fills it.
const (
	tailscalePolicyTag         = "tag:cumin-host"
	tailscalePolicyMember      = "autogroup:member"
	tailscalePolicyPlaceholder = "__MEMBER_LOGIN__"
)

// tailscalePolicyPorts are the only ports of the Host that a member device
// reaches: SSH and screen sharing.
var tailscalePolicyPorts = []string{"22", "5900"}

var tailscalePolicyEmail = regexp.MustCompile(`[^\s"@:]+@[^\s"@:]+`)

// tailscalePolicy holds every field that the template may use. A field
// that it does not name, such as the section "acls", fails the check.
type tailscalePolicy struct {
	TagOwners map[string][]string `json:"tagOwners"`
	Grants    []struct {
		Src []string `json:"src"`
		Dst []string `json:"dst"`
		IP  []string `json:"ip"`
	} `json:"grants"`
	Tests []struct {
		Src    string   `json:"src"`
		Proto  string   `json:"proto"`
		Accept []string `json:"accept"`
		Deny   []string `json:"deny"`
	} `json:"tests"`
}

// checkTailscalePolicy returns what is wrong with a tailnet policy, as the
// template must be: nothing starts from the tag of the Host, a member device
// reaches the tag on the two ports only, the section "tests" proves both,
// and the file holds no email address.
func checkTailscalePolicy(data []byte) error {
	if email := tailscalePolicyEmail.Find(data); email != nil {
		return fmt.Errorf("the file holds the email address %q", email)
	}
	var policy tailscalePolicy
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&policy); err != nil {
		return fmt.Errorf("the file is not the plain JSON of the template: %w", err)
	}
	if owners := policy.TagOwners[tailscalePolicyTag]; len(policy.TagOwners) != 1 || !slices.Equal(owners, []string{"autogroup:admin"}) {
		return fmt.Errorf("tagOwners is %v, want only %s owned by autogroup:admin", policy.TagOwners, tailscalePolicyTag)
	}

	var memberToMember, memberToTag bool
	for i, grant := range policy.Grants {
		if !slices.Equal(grant.Src, []string{tailscalePolicyMember}) {
			return fmt.Errorf("grant %d has the source %v, which may cover the Host; want only %s", i, grant.Src, tailscalePolicyMember)
		}
		switch {
		case slices.Equal(grant.Dst, []string{tailscalePolicyMember}):
			memberToMember = true
		case slices.Equal(grant.Dst, []string{tailscalePolicyTag}):
			if len(grant.IP) == 0 {
				return fmt.Errorf("grant %d names no port of %s", i, tailscalePolicyTag)
			}
			for _, ip := range grant.IP {
				port, isTCP := strings.CutPrefix(ip, "tcp:")
				if !isTCP || !slices.Contains(tailscalePolicyPorts, port) {
					return fmt.Errorf("grant %d lets a member reach %q of %s, want only tcp of %v", i, ip, tailscalePolicyTag, tailscalePolicyPorts)
				}
			}
			memberToTag = true
		default:
			return fmt.Errorf("grant %d has the destination %v, want only %s or only %s", i, grant.Dst, tailscalePolicyMember, tailscalePolicyTag)
		}
	}
	if !memberToMember || !memberToTag {
		return fmt.Errorf("the grants lack the rule from member to member, or the rule from member to %s", tailscalePolicyTag)
	}

	var accepts, denies bool
	for _, test := range policy.Tests {
		if test.Proto != "tcp" {
			continue
		}
		if test.Src == tailscalePolicyPlaceholder && holdsBothPorts(test.Accept, tailscalePolicyTag) {
			accepts = true
		}
		if test.Src == tailscalePolicyTag && len(test.Accept) == 0 && holdsBothPorts(test.Deny, tailscalePolicyPlaceholder) {
			denies = true
		}
	}
	if !accepts {
		return fmt.Errorf("the tests lack the accept test from %s to the ports %v of %s", tailscalePolicyPlaceholder, tailscalePolicyPorts, tailscalePolicyTag)
	}
	if !denies {
		return fmt.Errorf("the tests lack the deny test from %s to the ports %v of %s", tailscalePolicyTag, tailscalePolicyPorts, tailscalePolicyPlaceholder)
	}
	return nil
}

// holdsBothPorts reports whether the destinations of a test hold the two
// ports of the host.
func holdsBothPorts(destinations []string, host string) bool {
	for _, port := range tailscalePolicyPorts {
		if !slices.Contains(destinations, host+":"+port) {
			return false
		}
	}
	return true
}

func readTailscalePolicy(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("tailscale/policy.hujson")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestTailscalePolicyTemplateLimitsTheHost(t *testing.T) {
	if err := checkTailscalePolicy([]byte(readTailscalePolicy(t))); err != nil {
		t.Fatal(err)
	}
}

// Each case changes one place of the template into a policy that the check
// must refuse.
func TestTailscalePolicyCheckRefusesAWeakerPolicy(t *testing.T) {
	const (
		memberSrc  = `"src": ["autogroup:member"]`
		tagPorts   = `"ip": ["tcp:22", "tcp:5900"]`
		acceptTest = `"accept": ["tag:cumin-host:22", "tag:cumin-host:5900"]`
		denyTest   = `"deny": ["__MEMBER_LOGIN__:22", "__MEMBER_LOGIN__:5900"]`
	)
	cases := []struct {
		name     string
		old, new string
		want     string
	}{
		{"the tag is a source", memberSrc, `"src": ["tag:cumin-host"]`, "may cover the Host"},
		{"every device is a source", memberSrc, `"src": ["*"]`, "may cover the Host"},
		{"the members plus the tag are a source", memberSrc, `"src": ["autogroup:member", "tag:cumin-host"]`, "may cover the Host"},
		{"a section of another syntax", `"grants"`, `"acls": [{"action": "accept", "src": ["tag:cumin-host"], "dst": ["*:*"]}], "grants"`, "not the plain JSON of the template"},
		{"a third port of the tag", tagPorts, `"ip": ["tcp:22", "tcp:5900", "tcp:80"]`, `"tcp:80"`},
		{"every port of the tag", tagPorts, `"ip": ["*"]`, `"*"`},
		{"a port of the tag with no protocol", tagPorts, `"ip": ["22", "5900"]`, `"22"`},
		{"the tag beside the members as a destination", `"dst": ["autogroup:member"]`, `"dst": ["autogroup:member", "tag:cumin-host"]`, "has the destination"},
		{"no accept test of port 5900", acceptTest, `"accept": ["tag:cumin-host:22"]`, "lack the accept test"},
		{"no accept test", acceptTest, `"accept": []`, "lack the accept test"},
		{"no deny test from the tag", denyTest, `"deny": []`, "lack the deny test"},
		{"the deny test starts from a member", `"src": "tag:cumin-host"`, `"src": "__MEMBER_LOGIN__"`, "lack the deny test"},
		{"an email address in place of the placeholder", `"src": "__MEMBER_LOGIN__"`, `"src": "member@example.com"`, "email address"},
		{"an email address as an owner of the tag", `["autogroup:admin"]`, `["autogroup:admin", "admin@example.com"]`, "email address"},
	}
	template := readTailscalePolicy(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(template, c.old) {
				t.Fatalf("the template does not hold %s", c.old)
			}
			err := checkTailscalePolicy([]byte(strings.Replace(template, c.old, c.new, 1)))
			if err == nil {
				t.Fatal("the check accepts the policy")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("the check refuses the policy for another reason: %v", err)
			}
		})
	}
}
