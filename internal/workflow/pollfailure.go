package workflow

// A poll of one repository can fail on its own: a connection of the
// snapshot above its page size, a wrong .cumin/config.toml, a label change
// that GitHub refused. Each failure is logged and the other repositories
// go on, but a failure that repeats means that nothing moves in that
// repository, and the Owner has to know.
//
// This file counts the consecutive failures of each repository and tells
// the Owner once. docs/ja/designs/poll.md, the topic on a poll that keeps
// failing.

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/notify"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
)

// pollFailuresBeforeNotice is how many polls of one repository must fail
// with the same reason before cumin tells the Owner (cumin-core.md, the
// table of notifications).
const pollFailuresBeforeNotice = 3

// repeatedFailure is what one repository failed with, and how often in a
// row. It lives in memory only. A restart of cumin loses it, which can
// only delay a notification by three more polls; the requirements list
// what cumin may keep on the Host, and this count is not on the list
// (cumin-core.md, the section on where the state lives).
type repeatedFailure struct {
	// reason is the text of the failure. Two failures are the same when
	// this text is equal.
	reason string
	// count is how many polls in a row failed with that reason.
	count int
	// told is true after the Owner was told about this run of failures.
	// It goes back to false when a poll of the repository succeeds.
	told bool
}

// pollSucceeded forgets the failures of one repository. The next run of
// failures is counted from the start, and the Owner is told again.
func (s *Service) pollSucceeded(repository config.Repository) {
	s.failureMu.Lock()
	defer s.failureMu.Unlock()
	delete(s.pollFailures, repositoryKey(repository))
}

// pollFailed counts one failed poll and tells the Owner on the third
// failure with the same reason. A different reason starts the count again,
// because it is another problem. After the Owner was told, nothing more is
// sent until a poll of that repository succeeds.
func (s *Service) pollFailed(ctx context.Context, repository config.Repository, err error) {
	reason := err.Error()
	s.failureMu.Lock()
	if s.pollFailures == nil {
		s.pollFailures = map[string]*repeatedFailure{}
	}
	failure, ok := s.pollFailures[repositoryKey(repository)]
	if !ok || failure.reason != reason {
		failure = &repeatedFailure{reason: reason}
		s.pollFailures[repositoryKey(repository)] = failure
	}
	failure.count++
	tell := failure.count >= pollFailuresBeforeNotice && !failure.told
	failure.told = failure.told || tell
	count := failure.count
	s.failureMu.Unlock()

	if !tell {
		return
	}
	log := s.logger().With("repository", repository.String())
	log.Warn("the poll of the repository keeps failing", "failures", count)
	// No row of issue-states.md covers this line of the table of
	// notifications, so the notification carries no row number.
	s.notifyOwner(ctx, log, s.notifyEnabled(repository), notify.Notification{
		Reason:     fmt.Sprintf("The poll of this repository failed %d times in a row with the same reason: %s", count, oneLine(reason)),
		Repository: repository.String(),
		Link:       github.RepositoryURL(repository.Owner, repository.Name),
	})
}

// notifyEnabled says whether cumin notifies about one repository. The
// settings that a poll kept decide. Before the first poll read them, and
// when the file of the repository is the reason of the failure, the Host
// settings decide, so that a repository cannot silence a failure that it
// caused.
func (s *Service) notifyEnabled(repository config.Repository) bool {
	s.settingsMu.Lock()
	kept, ok := s.repositorySettings[repositoryKey(repository)]
	s.settingsMu.Unlock()
	if ok {
		return kept.Settings.Notify.DiscordEnabled
	}
	return s.Settings != nil && s.Settings.Notify.DiscordEnabled
}

// repositoryKey names one repository in the maps of the service. GitHub
// account names ignore case.
func repositoryKey(repository config.Repository) string {
	return strings.ToLower(repository.String())
}

// oneLine turns a reason of more than one line into one line, so that the
// message keeps its shape. An error of a repository file names every key
// that is wrong, on a line of its own.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
