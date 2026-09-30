package workflow

// This file cleans up after a sub-issue closes: its worktrees of every
// role, the local branch, and its entry in the state file.
// docs/ja/designs/agent-run.md, the topic on the work directory.

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/cloveclovedev/cumin-works/internal/agent"
	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/core/state"
)

// cleanupRoles are the roles whose worktree a closed issue can have.
var cleanupRoles = []config.Role{config.RoleImplementer, config.RoleReviewer, config.RolePlanner}

// cleanUp removes what the Host keeps for each closed sub-issue of the
// snapshot. A worktree with work that is not on GitHub stays; the log says
// so once, and cumin does not check it again until it restarts. The
// snapshot shows only the sub-issues of open requirement issues, so the
// worktree of an issue that it does not show stays too (the manual
// procedure in docs/ja/development/work-directory.md removes it).
func (s *Service) cleanUp(ctx context.Context, log *slog.Logger, target Target, snapshot Snapshot) {
	repository := target.Repository.String()
	for _, number := range IssuesToCleanUp(snapshot) {
		log := log.With("issue", number)
		for _, role := range cleanupRoles {
			checkout := agent.Checkout{Owner: target.Repository.Owner, Repo: target.Repository.Name, Issue: number, Role: role}
			key := fmt.Sprintf("%s#%d-%s", repository, number, role)
			if s.wasKept(key) {
				continue
			}
			kept, err := s.Workspace.RemoveClosed(ctx, checkout)
			if err != nil {
				log.Error("cleanup: the worktree was not removed", "role", role, "error", err.Error())
				continue
			}
			if kept {
				s.keep(key)
				log.Warn("cleanup: the worktree of a closed issue holds work that is not on GitHub; it stays",
					"role", role, "work_dir", s.Workspace.Dir(checkout))
			}
		}
		if s.State.Issue(repository, number) == (state.Issue{}) {
			continue
		}
		if err := s.State.Clear(repository, number); err != nil {
			log.Error("cleanup: the entry of the state file was not removed", "error", err.Error())
			continue
		}
		log.Info("cleanup: removed the entry of the state file")
	}
}

// wasKept reports whether the poll kept the worktree of the key before.
func (s *Service) wasKept(key string) bool {
	s.keptMu.Lock()
	defer s.keptMu.Unlock()
	return s.kept[key]
}

// keep records that the poll kept the worktree of the key.
func (s *Service) keep(key string) {
	s.keptMu.Lock()
	defer s.keptMu.Unlock()
	if s.kept == nil {
		s.kept = map[string]bool{}
	}
	s.kept[key] = true
}
