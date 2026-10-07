package github_test

import (
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestLiveMergeFacts measures what the merge of #222 relies on: how the
// token of cumin-core reads the permission of the account of a review
// (I12), and how GitHub answers a merge that conflicts, a merge whose head
// moved, and a clean merge (I6).
//
// Official, REST: "Get repository permissions for a user" (permission is
// admin, write, read, or none; maintain maps to write; Permissions required
// for GitHub Apps: Metadata read); "Merge a pull request" (405 if the merge
// cannot be performed, 409 if sha does not match the head); "Get a pull
// request" (mergeable is the result of a test merge commit, null while
// GitHub computes it).
func TestLiveMergeFacts(t *testing.T) {
	l := newLive(t)
	defer func() { t.Log("\n" + l.table()) }()

	maintainer := os.Getenv("CUMIN_LIVE_OWNER")
	if maintainer == "" {
		t.Fatal("set CUMIN_LIVE_OWNER to the login of a human account with admin or write permission on the sandbox")
	}
	core := l.token(t, "cumin-core")
	implementer := l.token(t, "implementer")
	botLogin := l.botLogin(t, "implementer")
	repo := l.newGitRepo(t, implementer, botLogin, botLogin+"@users.noreply.github.com")

	// Fact M1: the permission of three accounts, with the token of
	// cumin-core.
	// Only the Owner is a user with admin or write; the bot and the account
	// that is not a collaborator must never read as an Owner.
	for i, account := range []struct {
		login, what, userType string
		isMaintainer          bool
	}{
		{maintainer, "the Owner", "User", true},
		{botLogin, "the bot of the Implementer App", "Bot", false},
		{"octocat", "an account that is not a collaborator", "User", false},
	} {
		resp := l.api(t, core, http.MethodGet, "/repos/{repo}/collaborators/"+account.login+"/permission", nil)
		var body struct {
			Permission string `json:"permission"`
			RoleName   string `json:"role_name"`
			User       *struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			} `json:"user"`
		}
		if resp.status == http.StatusOK {
			resp.json(t, &body)
		}
		userType := "null"
		if body.User != nil {
			userType = body.User.Type
		}
		l.record(fmt.Sprintf("M1.%d", i+1), "`collaborators/{username}/permission` with the token of cumin-core, for "+account.what, "Readable; the Owner is admin or write",
			fmt.Sprintf("Status %d: %s. `permission` `%s`, `role_name` `%s`, `user.type` `%s`", resp.status, resp.message(), body.Permission, body.RoleName, userType))
		writes := body.Permission == "admin" || body.Permission == "write"
		if resp.status != http.StatusOK || userType != account.userType || writes != account.isMaintainer {
			t.Errorf("fact M1.%d: %s: status %d, permission %q, type %q", i+1, account.what, resp.status, body.Permission, userType)
		}
	}

	// Two pull requests change the same new file in two ways, so the second
	// one conflicts after the first one is merged.
	path := "live/" + l.runID + "-conflict.md"
	branchA, branchB := "live-"+l.runID+"-merge-a", "live-"+l.runID+"-merge-b"
	shaA := repo.commitFile(t, branchA, path, "version A\n")
	l.pushBranch(t, repo, implementer, branchA)
	shaB := repo.commitFile(t, branchB, path, "version B\n")
	l.pushBranch(t, repo, implementer, branchB)
	pullA := l.openPullWithBody(t, implementer, branchA, "test: live merge facts A "+l.runID, "A live check of cumin-works. The test closes it.")
	pullB := l.openPullWithBody(t, implementer, branchB, "test: live merge facts B "+l.runID, "A live check of cumin-works. The test closes it.")
	l.waitForRequiredChecks(t, core, shaA)
	l.waitForRequiredChecks(t, core, shaB)

	// Fact M2: a merge with a sha that is not the head.
	moved := l.api(t, core, http.MethodPut, fmt.Sprintf("/repos/{repo}/pulls/%d/merge", pullA.Number), map[string]any{"merge_method": "squash", "sha": shaB})
	l.record("M2", "The cumin-core App merges with a `sha` that is not the head of the pull request", "409, nothing merged",
		fmt.Sprintf("Status %d: %s", moved.status, moved.message()))
	if moved.status != http.StatusConflict {
		t.Errorf("fact M2: status %d: %s", moved.status, moved.message())
	}

	// Fact M3: a clean merge with the sha of the head, and what GitHub says
	// of the pull request before it.
	mergeableA, stateA := l.mergeable(t, core, pullA.Number)
	clean := l.api(t, core, http.MethodPut, fmt.Sprintf("/repos/{repo}/pulls/%d/merge", pullA.Number), map[string]any{"merge_method": "squash", "sha": shaA})
	l.record("M3", "The cumin-core App merges a clean pull request with `sha` = its head", "200",
		fmt.Sprintf("Before: `mergeable` %s, `mergeable_state` `%s`. Merge: status %d", mergeableA, stateA, clean.status))
	if clean.status != http.StatusOK {
		t.Fatalf("fact M3: status %d: %s", clean.status, clean.message())
	}

	// Fact M4: the merge of the pull request that now conflicts.
	mergeableB, stateB := l.mergeable(t, core, pullB.Number)
	conflict := l.api(t, core, http.MethodPut, fmt.Sprintf("/repos/{repo}/pulls/%d/merge", pullB.Number), map[string]any{"merge_method": "squash", "sha": shaB})
	l.record("M4", "The cumin-core App merges a pull request that conflicts with the default branch, right after the default branch moved", "An error that names the conflict",
		fmt.Sprintf("Before: `mergeable` %s, `mergeable_state` `%s`. Merge: status %d: %s", mergeableB, stateB, conflict.status, conflict.message()))
	if conflict.status != http.StatusMethodNotAllowed {
		t.Errorf("fact M4: status %d: %s", conflict.status, conflict.message())
	}

	// Fact M5: mergeable may still hold the value from before the default
	// branch moved. Read it again until it is false, for up to one minute.
	start := time.Now()
	after, stateAfter := "", ""
	for {
		after, stateAfter = l.mergeable(t, core, pullB.Number)
		if after == "false" || time.Since(start) > time.Minute {
			break
		}
		time.Sleep(5 * time.Second)
	}
	l.record("M5", "`mergeable` of the conflicting pull request, read again after the merge failed", "false",
		fmt.Sprintf("`mergeable` %s, `mergeable_state` `%s`, after about %d seconds", after, stateAfter, int(time.Since(start).Seconds())))
	if after != "false" {
		t.Errorf("fact M5: mergeable %s after one minute", after)
	}
}

// waitForRequiredChecks waits for every required check of the default
// branch on the commit, so that a merge is not refused for a check that is
// still running.
func (l *live) waitForRequiredChecks(t *testing.T, token, sha string) {
	t.Helper()
	status, required := l.requiredChecks(t, token)
	if status != http.StatusOK {
		t.Fatalf("read the required checks: status %d", status)
	}
	for _, name := range required {
		l.waitForCheck(t, token, sha, name)
	}
}

// mergeable reads mergeable and mergeable_state of a pull request. GitHub
// answers null while it computes the test merge commit, so it reads again
// for up to one minute.
func (l *live) mergeable(t *testing.T, token string, number int) (string, string) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		var pull struct {
			Mergeable      *bool  `json:"mergeable"`
			MergeableState string `json:"mergeable_state"`
		}
		l.api(t, token, http.MethodGet, fmt.Sprintf("/repos/{repo}/pulls/%d", number), nil).mustJSON(t, http.StatusOK, &pull)
		if pull.Mergeable != nil {
			return fmt.Sprint(*pull.Mergeable), pull.MergeableState
		}
		if time.Now().After(deadline) {
			return "null", pull.MergeableState
		}
		time.Sleep(3 * time.Second)
	}
}
