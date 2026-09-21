package agent

// This file builds the environment of the CLI process. The environment
// of cumin holds the credentials of the Host user (the gh login, the SSH
// agent socket, and so on). The agent gets none of them: its environment
// starts from a fixed list of Host variables and gets only the token of
// its own GitHub App. docs/ja/designs/agent-run.md ("Agentの環境") records
// the decision. The variables come from the official documentation of
// git (git(1) "Environment Variables", git-config(1)), gh ("gh help
// environment"), and Claude Code ("Environment variables").

import (
	"encoding/base64"
	"errors"
	"os"
)

// hostVariables are the variables of the Host that the CLI gets, when
// they are set. Nothing else of the environment of cumin reaches the
// agent. HOME stays, because Claude Code keeps its subscription login and
// its session files under ~/.claude.
var hostVariables = []string{"PATH", "HOME", "TMPDIR", "LANG", "LC_ALL", "LC_CTYPE", "SHELL", "USER", "LOGNAME"}

// Credentials are what an agent may use on GitHub: the installation
// access token of the App of its role, and the identity of its commits.
type Credentials struct {
	Token string
	// AuthorName and AuthorEmail are the author and the committer of the
	// commits of the agent: the bot user of the App, so that GitHub links
	// the commits to it (measured-constraints.md row 49).
	AuthorName  string
	AuthorEmail string
}

func (c Credentials) validate() error {
	var errs []error
	if c.Token == "" {
		errs = append(errs, errors.New("the request has no token"))
	}
	if c.AuthorName == "" {
		errs = append(errs, errors.New("the request has no author name"))
	}
	if c.AuthorEmail == "" {
		errs = append(errs, errors.New("the request has no author email"))
	}
	return errors.Join(errs...)
}

// baseEnvironment is the environment of every CLI process of cumin: the
// Host variables of the fixed list, and auto memory off.
func baseEnvironment() []string {
	var env []string
	for _, name := range hostVariables {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	// --setting-sources project does not stop auto memory. The variable
	// does (measured-constraints.md row 28; Claude Code "Environment
	// variables").
	return append(env, "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1")
}

// environment is the environment of an agent run: the base, and the
// credentials for git and gh. ghConfigDir is an empty directory for the
// configuration of gh, so that gh reads no file of the Host user.
func environment(cred Credentials, ghConfigDir string) []string {
	basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + cred.Token))
	return append(baseEnvironment(),
		// git reads no configuration file of the Host user or of the
		// system, so no credential helper and no identity of the Owner
		// (git(1): GIT_CONFIG_GLOBAL, GIT_CONFIG_NOSYSTEM).
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		// No prompt for a password: nobody answers (git(1)).
		"GIT_TERMINAL_PROMPT=0",
		// No SSH: the keys of the Host user stay out of reach. The
		// remote of the worktree is HTTPS (git(1): GIT_SSH_COMMAND).
		"GIT_SSH_COMMAND=false",
		// The token, as the header that GitHub accepts for git over
		// HTTPS, through the environment instead of a file
		// (git-config(1): GIT_CONFIG_COUNT).
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.https://github.com/.extraheader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic "+basic,
		"GIT_AUTHOR_NAME="+cred.AuthorName,
		"GIT_AUTHOR_EMAIL="+cred.AuthorEmail,
		"GIT_COMMITTER_NAME="+cred.AuthorName,
		"GIT_COMMITTER_EMAIL="+cred.AuthorEmail,
		// gh uses the token instead of the stored login of the Host user,
		// reads its configuration from the empty directory, and never
		// prompts ("gh help environment").
		"GH_TOKEN="+cred.Token,
		"GH_CONFIG_DIR="+ghConfigDir,
		"GH_PROMPT_DISABLED=1",
		"GH_NO_UPDATE_NOTIFIER=1",
	)
}
