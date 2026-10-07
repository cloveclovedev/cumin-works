package workflow

// This file applies "write the follow-up note": when a sub-issue of an open
// requirement issue is closed and a pull request linked to close it is
// merged, cumin copies the work left into one follow-up note on the
// requirement issue. It reads apart from the snapshot, and only for closed
// sub-issues without a note. docs/ja/designs/poll.md, the topic on the
// follow-up notes.

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
)

// writeFollowUpNotes writes the missing follow-up notes of the snapshot,
// and marks each requirement issue whose closed sub-issues need no more
// note in this poll (FollowUpsDone). "request the acceptance check" waits
// for that mark, so that the notes come before the request for the
// acceptance check.
//
// The snapshot holds open requirement issues only, so a closed requirement
// issue gets nothing (principle 6). Whether a note exists is read from
// GitHub each time, never kept on the Host: a pull request with nothing to
// list leaves no marker, so it is read again at each poll while its
// requirement issue is open. A failed read or write is logged, and the next
// poll tries again.
func (s *Service) writeFollowUpNotes(ctx context.Context, log *slog.Logger, token string, target Target, snapshot *Snapshot) {
	var logins *followUpLogins
	for i := range snapshot.RequirementIssues {
		requirement := &snapshot.RequirementIssues[i]
		log := log.With("requirement_issue", requirement.Number, "action", ActionWriteTheFollowUpNote)
		requirement.FollowUpsDone = s.writeNotesOf(ctx, log, token, target, *requirement, &logins)
		if !requirement.FollowUpsDone && NeedsComments(*requirement) {
			log.Info(string(ActionRequestTheAcceptanceCheck) + ": waits for the follow-up notes of the closed sub-issues")
		}
	}
}

// followUpLogins are the logins that "write the follow-up note" needs:
// cumin-core knows its own notes, and the Reviewer App writes the review
// comments.
type followUpLogins struct {
	cumin, reviewer string
}

// writeNotesOf writes the missing notes of one requirement issue and
// reports whether no closed sub-issue needs a note any more. The logins are
// read once for the poll, when the first requirement issue needs them.
func (s *Service) writeNotesOf(ctx context.Context, log *slog.Logger, token string, target Target, requirement RequirementIssue, logins **followUpLogins) bool {
	owner, repo := target.Repository.Owner, target.Repository.Name
	since := FollowUpSince(requirement)
	if since.IsZero() {
		// No sub-issue is closed, so no note is missing.
		return true
	}
	if *logins == nil {
		cumin, reviewer, ok := s.followUpLogins(ctx, log, target)
		if !ok {
			return false
		}
		*logins = &followUpLogins{cumin: cumin, reviewer: reviewer}
	}
	read, rate, err := s.GitHub.ReadIssueComments(ctx, token, owner, repo, requirement.Number, since)
	if err != nil {
		log.Error(string(ActionWriteTheFollowUpNote)+": the comments were not read", "error", err.Error())
		return false
	}
	log.Debug(string(ActionWriteTheFollowUpNote)+": read the comments", "rate_limit_cost", rate.Cost, "rate_limit_remaining", rate.Remaining)
	comments := make([]Comment, 0, len(read))
	for _, c := range read {
		comments = append(comments, Comment{Author: c.Author, CreatedAt: c.CreatedAt, Body: c.Body})
	}
	marks := FollowUpMarks(comments, (*logins).cumin)
	done := true
	for _, sub := range FollowUpCandidates(requirement, marks) {
		written, err := s.writeFollowUpNote(ctx, log.With("issue", sub.Number), token, target, requirement.Number, sub, marks, (*logins).reviewer)
		// One pull request can close two sub-issues. The note written for
		// the first one counts for the second in this poll too.
		marks = append(marks, written...)
		if err != nil {
			done = false
		}
	}
	return done
}

// writeFollowUpNote writes the notes of one closed sub-issue: one for each
// merged pull request that is linked to close it, has no note yet, and
// leaves work. Who closed the sub-issue does not matter (the Note on #239).
// It reads every pull request before it
// writes, and each marker names all the pull requests that need a note, so
// that a write that fails halfway leaves the sub-issue to read again
// (FollowUpCandidates). It returns the markers of the notes that it wrote.
// An error means that a read or a write failed, and that a note may still
// be missing.
func (s *Service) writeFollowUpNote(ctx context.Context, log *slog.Logger, token string, target Target, requirement int, sub SubIssue, marks []FollowUpMark, reviewer string) ([]FollowUpMark, error) {
	owner, repo := target.Repository.Owner, target.Repository.Name
	linked, rate, err := s.GitHub.ReadLinkedPullRequests(ctx, token, owner, repo, sub.Number)
	if err != nil {
		log.Error(string(ActionWriteTheFollowUpNote)+": the linked pull requests were not read", "error", err.Error())
		return nil, err
	}
	log.Debug(string(ActionWriteTheFollowUpNote)+": read the linked pull requests", "rate_limit_cost", rate.Cost, "rate_limit_remaining", rate.Remaining)
	if len(linked) == 0 {
		log.Debug(string(ActionWriteTheFollowUpNote) + ": no pull request is linked to the issue")
	}
	// The pull requests that need a note: those with a note already, those
	// that leave work now, and those that are still open.
	var notes []int
	for _, m := range marks {
		if m.Issue == sub.Number && !m.At.Before(sub.ClosedAt) {
			notes = append(notes, m.PullRequest)
		}
	}
	type pending struct {
		number int
		pr     MergedPullRequest
	}
	var toWrite []pending
	for _, l := range linked {
		if !l.Closed {
			// A linked pull request that is still open may be merged
			// later. The marker names it, so that the sub-issue is read
			// again until then.
			notes = append(notes, l.Number)
			continue
		}
		if !l.Merged || HasFollowUpNote(marks, l.Number) {
			continue
		}
		pr, err := s.readPullRequestNote(ctx, log.With("pull_request", l.Number), token, target, l.Number)
		if err != nil {
			return nil, err
		}
		if _, ok := FollowUpNote(sub, pr, reviewer, nil); !ok {
			log.Debug(string(ActionWriteTheFollowUpNote)+": nothing is left to copy", "pull_request", l.Number)
			continue
		}
		toWrite = append(toWrite, pending{number: l.Number, pr: pr})
		notes = append(notes, l.Number)
	}
	slices.Sort(notes)
	notes = slices.Compact(notes)
	var written []FollowUpMark
	for _, p := range toWrite {
		note, _ := FollowUpNote(sub, p.pr, reviewer, notes)
		comment, err := s.GitHub.CreateIssueComment(ctx, token, owner, repo, requirement, note)
		if err != nil {
			log.Error(string(ActionWriteTheFollowUpNote)+": the follow-up note was not written", "pull_request", p.number, "error", err.Error())
			return written, err
		}
		log.Info(string(ActionWriteTheFollowUpNote)+": wrote the follow-up note", "pull_request", p.number, "comment", comment.URL)
		written = append(written, FollowUpMark{Issue: sub.Number, PullRequest: p.number, At: time.Now(), Notes: notes})
	}
	return written, nil
}

// readPullRequestNote reads the description and the review threads of one
// pull request.
func (s *Service) readPullRequestNote(ctx context.Context, log *slog.Logger, token string, target Target, number int) (MergedPullRequest, error) {
	read, rate, err := s.GitHub.ReadPullRequestNote(ctx, token, target.Repository.Owner, target.Repository.Name, number)
	if err != nil {
		log.Error(string(ActionWriteTheFollowUpNote)+": the pull request was not read", "error", err.Error())
		return MergedPullRequest{}, err
	}
	log.Debug(string(ActionWriteTheFollowUpNote)+": read the pull request", "rate_limit_cost", rate.Cost, "rate_limit_remaining", rate.Remaining)
	pr := MergedPullRequest{Number: read.Number, Merged: read.Merged, Body: read.Body}
	for _, t := range read.Threads {
		thread := ReviewThread{Path: t.Path, Line: t.Line}
		for _, c := range t.Comments {
			thread.Comments = append(thread.Comments, ReviewComment{Author: c.Author, Body: c.Body, URL: c.URL})
		}
		pr.Threads = append(pr.Threads, thread)
	}
	return pr, nil
}

// followUpLogins returns the logins of cumin-core and of the Reviewer App.
// cumin knows its own notes by the first, and the review comments of the
// Reviewer by the second.
func (s *Service) followUpLogins(ctx context.Context, log *slog.Logger, target Target) (cumin, reviewer string, ok bool) {
	if target.Login == nil || s.Agents == nil {
		log.Debug(string(ActionWriteTheFollowUpNote) + ": no login of cumin-core or of the Reviewer; no follow-up note")
		return "", "", false
	}
	cumin, err := target.Login(ctx)
	if err != nil {
		log.Error(string(ActionWriteTheFollowUpNote)+": the login of cumin-core was not read", "error", err.Error())
		return "", "", false
	}
	reviewer, err = s.Agents.BotLogin(ctx, target.Repository.Owner, config.RoleReviewer)
	if err != nil {
		log.Error(string(ActionWriteTheFollowUpNote)+": the login of the Reviewer App was not read", "error", err.Error())
		return "", "", false
	}
	return cumin, reviewer, true
}
