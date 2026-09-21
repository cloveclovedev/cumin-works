package workflow

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
)

// hostVariables are the variables of the Host that the request command
// gets, when they are set. Nothing else of the environment of cumin reaches
// the command: the credentials of the Host user (GH_TOKEN, SSH_AUTH_SOCK,
// ...) stay out, as for an agent (internal/agent; the topic "Agent
// environment" in docs/ja/designs/agent-run.md). The list is the same as
// the agent's.
var hostVariables = []string{"PATH", "HOME", "TMPDIR", "LANG", "LC_ALL", "LC_CTYPE", "SHELL", "USER", "LOGNAME"}

// commandEnvironment builds the environment of the request command from
// the fixed list.
func commandEnvironment() []string {
	env := []string{}
	for _, name := range hostVariables {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return env
}

// request runs the request command with the repository and the issue number
// as arguments, and waits for it. This is the whole of "request the work"
// until the agent start is connected to the poll. An empty command logs the
// request and runs nothing.
func (s *Service) request(ctx context.Context, repository config.Repository, number int) error {
	log := s.logger().With("repository", repository.String(), "issue", number)
	if s.RequestCommand == "" {
		log.Info("I1: requested the work (no request command is configured)")
		return nil
	}
	cmd := exec.CommandContext(ctx, s.RequestCommand, repository.String(), strconv.Itoa(number))
	cmd.Env = commandEnvironment()
	output, err := cmd.CombinedOutput()
	log.Debug("request command output", "output", string(output))
	if err != nil {
		return fmt.Errorf("run %s: %w", s.RequestCommand, err)
	}
	log.Info("I1: requested the work", "command", s.RequestCommand)
	return nil
}
