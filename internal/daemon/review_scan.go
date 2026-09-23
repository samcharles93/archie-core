package daemon

import (
	"context"
	"fmt"

	workflowtask "github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/domain/workintake"
	"github.com/samcharles93/archie-core/internal/forge"
)

// ReactionPublisher delivers one reaction to the reaction stream. The
// composition root wires it to the task bus's PublishUnique against the
// reaction subject, so webhook and poll deliveries of the same record dedup
// on the envelope's source-independent key.
type ReactionPublisher func(ctx context.Context, reaction workintake.ReviewCommentEnvelope) error

// scanPRReviewReactions is the poll backstop's core for one PR (decision 2):
// list review activity since the task's persisted cursors, publish every
// non-own record as a reaction, and return the advanced cursors. The cursors
// advance past every record seen -- skipped own comments included -- so a
// record is listed once and never re-listed on a later scan. On a publish
// failure the error carries: the caller keeps the old cursors, and the next
// scan re-fetches the records it did not get to (PublishUnique's dedup
// absorbs any re-delivery).
func scanPRReviewReactions(
	ctx context.Context,
	reader forge.PullRequestReviewReader,
	task *workflowtask.Task,
	botUser string,
	reviewCursor, commentCursor int64,
	publish ReactionPublisher,
) (int64, int64, error) {
	newReviewCursor, newCommentCursor := reviewCursor, commentCursor

	reviews, err := reader.ListReviews(ctx, task.Owner, task.Repo, task.PRNumber, reviewCursor)
	if err != nil {
		return reviewCursor, commentCursor, err
	}
	for _, r := range reviews {
		if r.ID > newReviewCursor {
			newReviewCursor = r.ID
		}
		if botUser != "" && r.Author == botUser {
			continue
		}
		reaction := workintake.ReviewCommentEnvelope{
			Owner: task.Owner, Repo: task.Repo, PRNumber: task.PRNumber,
			Kind:     workintake.ReviewReactionReview,
			ReviewID: r.ID, Author: r.Author, State: r.State, Body: r.Body,
		}
		if err := publish(ctx, reaction); err != nil {
			return reviewCursor, commentCursor, fmt.Errorf("publish review %d: %w", r.ID, err)
		}
	}

	comments, err := reader.ListReviewComments(ctx, task.Owner, task.Repo, task.PRNumber, commentCursor)
	if err != nil {
		return reviewCursor, commentCursor, err
	}
	for _, cm := range comments {
		if cm.ID > newCommentCursor {
			newCommentCursor = cm.ID
		}
		if botUser != "" && cm.Author == botUser {
			continue
		}
		reaction := workintake.ReviewCommentEnvelope{
			Owner: task.Owner, Repo: task.Repo, PRNumber: task.PRNumber,
			Kind:      workintake.ReviewReactionComment,
			ReviewID:  cm.ReviewID,
			CommentID: cm.ID,
			Author:    cm.Author, Body: cm.Body, Path: cm.Path, Line: cm.Line,
		}
		if err := publish(ctx, reaction); err != nil {
			return reviewCursor, commentCursor, fmt.Errorf("publish comment %d: %w", cm.ID, err)
		}
	}

	return newReviewCursor, newCommentCursor, nil
}

// scanPRReviews runs the shared per-PR review scan over every open PR task
// (reconcilePRs's shape): one pass over all pr_open tasks, each scanned with
// the forge client its own identity owns. A forge without the review-reader
// capability is skipped, not an error. Wired after reconcilePRs so a PR
// that just left pr_open is not scanned for reactions in the same pass.
func (d *Daemon) scanPRReviews(ctx context.Context) {
	if d.Store == nil || d.Tasks == nil {
		return
	}
	tasks, err := d.Store.OpenPRs(ctx)
	if err != nil {
		d.Log.Error("review scan query failed", "err", err)
		return
	}
	if d.reactionPublisher == nil {
		d.reactionPublisher = d.publishReaction
	}
	for i := range tasks {
		t := &tasks[i]
		reader, ok := d.forgeFor(t).(forge.PullRequestReviewReader)
		if !ok {
			continue // the noop forge and any forge without the capability
		}
		d.scanOnePR(ctx, reader, t)
	}
}

func (d *Daemon) scanOnePR(ctx context.Context, reader forge.PullRequestReviewReader, t *workflowtask.Task) {
	// A forge read failure skips the task for this scan with its cursors
	// unchanged: the next scan re-fetches, and PublishUnique's dedup covers
	// anything the previous scan already published.
	reviewCursor, commentCursor, err := scanPRReviewReactions(
		ctx, reader, t, d.botUserForTask(t),
		t.ReviewCursor, t.WatchCommentID, d.reactionPublisher)
	if err != nil {
		d.Log.Warn("review scan failed", "repo", t.Owner+"/"+t.Repo, "pr", t.PRNumber, "err", err)
		return
	}
	if reviewCursor == t.ReviewCursor && commentCursor == t.WatchCommentID {
		return
	}
	if err := d.Store.SetReviewCursors(ctx, t.ID, reviewCursor, commentCursor); err != nil {
		// A stale guard here means the task was claimed between the scan and
		// the write; the next scan re-reads and re-lists from the old cursor,
		// and PublishUnique plus the consumer's dedup absorb the repeats.
		d.Log.Warn("review cursor persist failed", "task", t.ID, "err", err)
	}
}

// publishReaction encodes one reaction and publishes it on the reaction
// subject with its source-independent idempotency key, so a webhook delivery
// and a poll record of the same review collapse into one.
func (d *Daemon) publishReaction(ctx context.Context, reaction workintake.ReviewCommentEnvelope) error {
	payload, err := reaction.Encode()
	if err != nil {
		return err
	}
	return d.Tasks.PublishUnique(ctx, reaction.Subject(), reaction.IdempotencyKey(), payload)
}
