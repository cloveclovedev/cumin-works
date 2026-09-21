package workflow

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
)

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
	output, err := cmd.CombinedOutput()
	log.Debug("request command output", "output", string(output))
	if err != nil {
		return fmt.Errorf("run %s: %w", s.RequestCommand, err)
	}
	log.Info("I1: requested the work", "command", s.RequestCommand)
	return nil
}
