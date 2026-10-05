package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	workflowtask "github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/domain/workintake"
	"github.com/samcharles93/archie-core/internal/eventbus"
)

// ReactionSource is the pull surface the reaction consumer needs: fetch one
// message from the reaction stream, or eventbus.ErrNoMessage when it is
// drained. Narrow on purpose -- the consumer never publishes and never
// subscribes.
type ReactionSource interface {
	Fetch(ctx context.Context) (eventbus.Message, error)
}

// maxReactionsPerCycle bounds one drain pass. Reactions are row updates, not
// agent runs, but an unbounded drain could hold the dispatch loop hostage
// behind a burst; anything past the bound waits for the next cycle.
const maxReactionsPerCycle = 32

// remediableReviewStates are the review states a remediation unit addresses.
// approved is a decision-5 exclusion (recording, not remediating; merging
// stays an operator action) and dismissed is withdrawn feedback.
var remediableReviewStates = map[string]bool{
	"requested_changes": true,
	"commented":         true,
}

// reactionConsumer turns forge review reactions on Archie-owned PRs into queued
// remediate runs.
type reactionConsumer struct {
	lookup       workintake.ReviewTaskLookup
	remediations storecontract.RemediationStarter
	botUser      func(*workflowtask.Task) string
	log          *slog.Logger
	dropped      int64
}

func newReactionConsumer(
	lookup workintake.ReviewTaskLookup,
	remediations storecontract.RemediationStarter,
	botUser func(*workflowtask.Task) string,
	log *slog.Logger,
) *reactionConsumer {
	return &reactionConsumer{lookup: lookup, remediations: remediations, botUser: botUser, log: log}
}

// drain fetches and handles reactions until the stream is idle or the
// per-cycle bound is reached, returning how many messages it handled.
func (c *reactionConsumer) drain(ctx context.Context, src ReactionSource, limit int) (int, error) {
	handled := 0
	for handled < limit {
		msg, err := src.Fetch(ctx)
		if err != nil {
			if errors.Is(err, eventbus.ErrNoMessage) {
				return handled, nil
			}
			return handled, fmt.Errorf("fetch reaction: %w", err)
		}
		if ctx.Err() != nil {
			return handled, ctx.Err()
		}
		c.handle(ctx, msg)
		handled++
	}
	return handled, nil
}

// handle processes one reaction to a terminal outcome: Ack on every decided
// path (drop, dedup, or work queued), Nak only on a transient store failure
// the next delivery can succeed against.
func (c *reactionConsumer) handle(ctx context.Context, msg eventbus.Message) {
	envelope, err := workintake.DecodeReviewComment(msg.Data())
	if err != nil {
		c.log.Error("review reaction decode failed", "subject", msg.Subject(), "err", err)
		_ = msg.Ack() // it will never decode on redelivery
		return
	}

	task, err := c.lookup.OpenTaskByPR(ctx, envelope.Owner, envelope.Repo, envelope.PRNumber)
	if err != nil {
		c.log.Error("review reaction lookup failed", "owner", envelope.Owner, "repo", envelope.Repo, "pr", envelope.PRNumber, "err", err)
		_ = msg.Nak()
		return
	}
	if task == nil {
		// The resolver counts this: a reaction that does not resolve to a
		// live task Archie owns is dropped, never work.
		c.drop(msg, "no live task owns this PR")
		return
	}
	if c.botUser(task) != "" && envelope.Author == c.botUser(task) {
		// Decision 5: only Archie's own comments are excluded, so its
		// ReplyToReview replies never retrigger it. Every other author --
		// human or bot -- is actionable.
		c.drop(msg, "reaction is archie's own comment")
		return
	}

	switch envelope.Kind {
	case workintake.ReviewReactionReview:
		c.handleReview(ctx, msg, task, envelope)
	case workintake.ReviewReactionComment:
		c.handleComment(ctx, msg, task, envelope)
	default:
		c.drop(msg, "unknown reaction kind")
	}
}

func (c *reactionConsumer) handleReview(ctx context.Context, msg eventbus.Message, task *workflowtask.Task, e workintake.ReviewCommentEnvelope) {
	if !remediableReviewStates[e.State] {
		// approved: no remediation, and never auto-merge (decision 5). The
		// review state is visible on the forge itself.
		c.drop(msg, fmt.Sprintf("review state %q does not trigger remediation", e.State))
		return
	}

	unit := workflow.ReviewUnit{ReviewID: e.ReviewID, Author: e.Author, State: e.State, Body: e.Body}
	payload, err := workflow.EncodeReviewUnit(unit)
	if err != nil {
		c.log.Error("review unit encode failed", "task", task.ID, "err", err)
		_ = msg.Nak()
		return
	}
	if err := c.remediations.BeginRemediation(ctx, task.ID, payload); err != nil {
		if errors.Is(err, storecontract.ErrStaleTransition) {
			// The task left the states a remediation can start or queue
			// behind, so the reaction resolves to no work. A review that
			// arrives mid-run is absorbed by the store, not dropped here.
			c.log.Debug("task does not accept remediation", "task", task.ID, "review", e.ReviewID)
			_ = msg.Ack()
			return
		}
		c.log.Error("BeginRemediation failed", "task", task.ID, "err", err)
		_ = msg.Nak()
		return
	}
	c.log.Info("review reaction queued a remediation", "task", task.ID, "pr", fmt.Sprintf("%s/%s#%d", e.Owner, e.Repo, e.PRNumber), "review", e.ReviewID, "state", e.State)
	_ = msg.Ack()
}

func (c *reactionConsumer) handleComment(ctx context.Context, msg eventbus.Message, task *workflowtask.Task, e workintake.ReviewCommentEnvelope) {
	if e.ReviewID == 0 {
		// Decision 5: a standalone comment with no parent review is its own
		// unit of one.
		unit := workflow.ReviewUnit{Comments: []workflow.ReviewUnitComment{{
			CommentID: e.CommentID, Path: e.Path, Line: e.Line, Body: e.Body,
		}}}
		payload, err := workflow.EncodeReviewUnit(unit)
		if err != nil {
			c.log.Error("review unit encode failed", "task", task.ID, "err", err)
			_ = msg.Nak()
			return
		}
		if err := c.remediations.BeginRemediation(ctx, task.ID, payload); err != nil {
			if errors.Is(err, storecontract.ErrStaleTransition) {
				_ = msg.Ack()
				return
			}
			c.log.Error("BeginRemediation failed", "task", task.ID, "err", err)
			_ = msg.Nak()
			return
		}
		_ = msg.Ack()
		return
	}

	// A comment with a parent review is collected into that review's unit
	// while the unit is unclaimed: the queued active unit, or one waiting
	// behind a running remediation. A claimed unit's input is frozen, so a
	// late comment is dropped and the round cap bounds the exchange if the
	// reviewer re-reviews.
	payload, err := workflow.EncodeReviewUnit(workflow.ReviewUnit{
		ReviewID: e.ReviewID,
		Comments: []workflow.ReviewUnitComment{{CommentID: e.CommentID, Path: e.Path, Line: e.Line, Body: e.Body}},
	})
	if err != nil {
		c.log.Error("review unit encode failed", "task", task.ID, "err", err)
		_ = msg.Nak()
		return
	}
	if err := c.remediations.UpdateReviewPayload(ctx, task.ID, payload); err != nil {
		if errors.Is(err, storecontract.ErrStaleTransition) {
			c.drop(msg, "parent review's unit is no longer pending")
			return
		}
		c.log.Error("UpdateReviewPayload failed", "task", task.ID, "err", err)
		_ = msg.Nak()
		return
	}
	_ = msg.Ack()
}

func (c *reactionConsumer) drop(msg eventbus.Message, reason string) {
	c.dropped++
	c.log.Info("review reaction dropped", "subject", msg.Subject(), "reason", reason)
	_ = msg.Ack()
}
