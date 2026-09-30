package workflow

// This file applies I9: after a merged pull request closed a sub-issue of
// an open requirement issue, cumin copies the work left into one follow-up
// note on the requirement issue. It reads apart from the snapshot, and only
// for closed sub-issues without a note. docs/ja/designs/poll.md, the topic
// on the follow-up notes.

import (
	"context"
	"log/slog"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
)

// writeFollowUpNotes writes the missing follow-up notes of the snapshot.
// The snapshot holds open requirement issues only, so a closed requirement
// issue gets nothing (principle 6). Whether a note exists is read from
// GitHub each time, never kept on the Host: a pull request with nothing to
// list leaves no marker, so it is read again at each poll while its
// requirement issue is open. A failed read or write is logged, and the next
// poll tries again.
func (s *Service) writeFollowUpNotes(ctx context.Context, log *slog.Logger, token string, target Target, snapshot Snapshot) {
	owner, repo := target.Repository.Owner, target.Repository.Name
	var cumin, reviewer string
	for _, requirement := range snapshot.RequirementIssues {
		since := FollowUpSince(requirement)
		if since.IsZero() {
			continue
		}
		if cumin == "" {
			var ok bool
			cumin, reviewer, ok = s.followUpLogins(ctx, log, target)
			if !ok {
				return
			}
		}
		log := log.With("requirement_issue", requirement.Number, "row", RowI9)
		read, rate, err := s.GitHub.ReadIssueComments(ctx, token, owner, repo, requirement.Number, since)
		if err != nil {
			log.Error("I9: the comments were not read", "error", err.Error())
			continue
		}
		log.Debug("I9: read the comments", "rate_limit_cost", rate.Cost, "rate_limit_remaining", rate.Remaining)
		comments := make([]Comment, 0, len(read))
		for _, c := range read {
			comments = append(comments, Comment{Author: c.Author, CreatedAt: c.CreatedAt, Body: c.Body})
		}
		marks := FollowUpMarks(comments, cumin)
		for _, sub := range FollowUpCandidates(requirement, marks) {
			s.writeFollowUpNote(ctx, log.With("issue", sub.Number), token, target, requirement.Number, sub, marks, reviewer)
		}
	}
}

// writeFollowUpNote reads the pull request that closed one sub-issue and
// writes its note when it was merged, has no note yet, and leaves work.
func (s *Service) writeFollowUpNote(ctx context.Context, log *slog.Logger, token string, target Target, requirement int, sub SubIssue, marks []FollowUpMark, reviewer string) {
	owner, repo := target.Repository.Owner, target.Repository.Name
	read, rate, err := s.GitHub.ReadClosingPullRequest(ctx, token, owner, repo, sub.Number)
	if err != nil {
		log.Error("I9: the pull request that closed the issue was not read", "error", err.Error())
		return
	}
	log.Debug("I9: read the pull request that closed the issue", "rate_limit_cost", rate.Cost, "rate_limit_remaining", rate.Remaining)
	if read == nil || !read.Merged {
		log.Debug("I9: no merged pull request closed the issue")
		return
	}
	log = log.With("pull_request", read.Number)
	if HasFollowUpNote(marks, read.Number) {
		return
	}
	pr := MergedPullRequest{Number: read.Number, Merged: read.Merged, Body: read.Body}
	for _, t := range read.Threads {
		thread := ReviewThread{Path: t.Path, Line: t.Line}
		for _, c := range t.Comments {
			thread.Comments = append(thread.Comments, ReviewComment{Author: c.Author, Body: c.Body, URL: c.URL})
		}
		pr.Threads = append(pr.Threads, thread)
	}
	note, ok := FollowUpNote(sub, pr, reviewer)
	if !ok {
		log.Debug("I9: nothing is left to copy")
		return
	}
	comment, err := s.GitHub.CreateIssueComment(ctx, token, owner, repo, requirement, note)
	if err != nil {
		log.Error("I9: the follow-up note was not written", "error", err.Error())
		return
	}
	log.Info("I9: wrote the follow-up note", "comment", comment.URL)
}

// followUpLogins returns the logins of cumin-core and of the Reviewer App.
// cumin knows its own notes by the first, and the review comments of the
// Reviewer by the second.
func (s *Service) followUpLogins(ctx context.Context, log *slog.Logger, target Target) (cumin, reviewer string, ok bool) {
	if target.Login == nil || s.Agents == nil {
		log.Debug("I9: no login of cumin-core or of the Reviewer; no follow-up note")
		return "", "", false
	}
	cumin, err := target.Login(ctx)
	if err != nil {
		log.Error("I9: the login of cumin-core was not read", "error", err.Error())
		return "", "", false
	}
	reviewer, err = s.Agents.BotLogin(ctx, target.Repository.Owner, config.RoleReviewer)
	if err != nil {
		log.Error("I9: the login of the Reviewer App was not read", "error", err.Error())
		return "", "", false
	}
	return cumin, reviewer, true
}
