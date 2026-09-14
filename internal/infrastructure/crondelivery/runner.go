package crondelivery

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/samcharles93/archie-core/internal/domain/scheduling"
)

// ChatCourier delivers a job's payload text to a chat. It is the runner behind
// the "chat" kind and the epic's headline case: a daily status summary, which
// the blueprints slice will parameterise.
type ChatCourier struct {
	specs   SpecLookup
	courier Courier
}

// NewChatCourier builds the chat runner over a spec lookup and the courier the
// deployment supplies.
func NewChatCourier(specs SpecLookup, courier Courier) (*ChatCourier, error) {
	if specs == nil {
		return nil, errors.New("crondelivery: spec lookup must not be nil")
	}
	if courier == nil {
		return nil, errors.New("crondelivery: courier must not be nil")
	}
	return &ChatCourier{specs: specs, courier: courier}, nil
}

// Run hydrates the job's spec and sends its payload text to its target chat.
//
// The pre-check matters as much as the error wrap: a cancelled run must produce
// no side effect, because the engine cancels on shutdown and a message sent
// after that is one nobody asked for. The courier's own ctx handling covers
// cancellation that arrives mid-send.
func (c *ChatCourier) Run(ctx context.Context, job scheduling.Job) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	spec, err := hydrate(ctx, c.specs, job)
	if err != nil {
		return err
	}
	if err := c.courier(ctx, spec.Target.ChatID, spec.Payload.Text); err != nil {
		return fmt.Errorf("crondelivery: deliver job %q to %q: %w", job.ID, spec.Target.ChatID, err)
	}
	return nil
}

// WorkflowTask submits a job as a unit of work. It is the runner behind the
// "workflow" kind, so a scheduled job reaches the same task lifecycle —
// scheduling, retries, gates, PR opening — that every other intake path feeds.
type WorkflowTask struct {
	specs     SpecLookup
	submitter TaskSubmitter
}

// NewWorkflowTask builds the workflow runner over a spec lookup and the
// submitter the deployment supplies.
func NewWorkflowTask(specs SpecLookup, submitter TaskSubmitter) (*WorkflowTask, error) {
	if specs == nil {
		return nil, errors.New("crondelivery: spec lookup must not be nil")
	}
	if submitter == nil {
		return nil, errors.New("crondelivery: task submitter must not be nil")
	}
	return &WorkflowTask{specs: specs, submitter: submitter}, nil
}

// Run hydrates the job's spec and submits it as work.
//
// The title falls back to the job id when Detail is empty: a submitted task is
// how a human finds the job that created it, and an untitled one is
// unfindable.
func (w *WorkflowTask) Run(ctx context.Context, job scheduling.Job) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	spec, err := hydrate(ctx, w.specs, job)
	if err != nil {
		return err
	}
	title := strings.TrimSpace(spec.Detail)
	if title == "" {
		title = job.ID
	}
	if err := w.submitter.Submit(ctx, job.ID, title, spec.Payload.Text); err != nil {
		return fmt.Errorf("crondelivery: submit job %q: %w", job.ID, err)
	}
	return nil
}

// compile-time checks: the two deliveries are the engine's contract.
var (
	_ scheduling.Runner = (*ChatCourier)(nil)
	_ scheduling.Runner = (*WorkflowTask)(nil)
)
