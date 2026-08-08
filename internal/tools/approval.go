// Package tools: approval.go — tool-approval contract.
//
// Some tools are irreversible on a path the model controls (deleting a
// session, cancelling a task, sending email). The dispatch layer blocks
// those calls until a human consents, through an ApprovalRequester the
// adapter supplies. One adapter renders inline buttons in Telegram; the
// web UI could render a modal. Neither the tool nor the dispatch layer
// knows which adapter is asking — they only see the decision.
//
// This contract lives in internal/tools rather than internal/gateway so
// that internal/agentexec (the tool-dispatch layer, also used by the
// archie-agent sandbox binary) can import it without pulling in the
// chat-gateway domain stack.

package tools

import (
	"context"
	"errors"
	"time"
)

// ApprovalRequester asks a human to approve or deny a named action.
//
// The call blocks until the human decides or the context is cancelled. An
// adapter supplies the implementation — Telegram sends an inline keyboard,
// the web UI shows a modal, a headless test injects a decision directly.
//
// Implementations must honour ctx cancellation promptly (the turn lane holds
// the LLM connection open) and return [ErrApprovalDenied] or a context error,
// never nil, when approval was not granted.
type ApprovalRequester interface {
	RequestApproval(ctx context.Context, action, description string) (ApprovalDecision, error)
}

// ApprovalDecision is the outcome of a human consent request.
type ApprovalDecision int

const (
	// ApprovalApproved means the human approved this one execution.
	// The same tool invoked again will ask again.
	ApprovalApproved ApprovalDecision = iota

	// ApprovalPermanentlyApproved means the human approved this action
	// for the lifetime of the session. The adapter may cache this; the
	// dispatch layer checks it on every call.
	ApprovalPermanentlyApproved

	// ApprovalDenied means the human explicitly refused. The call must
	// not retry: the model should report the refusal to the user.
	ApprovalDenied
)

// ErrApprovalDenied is returned when the human refuses the action. Callers
// should distinguish this from a timeout or transport error so the model
// can report the refusal accurately.
var ErrApprovalDenied = errors.New("approval denied")

// ErrApprovalNotConfigured is returned when a tool requires approval but no
// ApprovalRequester is wired. It is an adapter problem, not a user decision
// — the model should report that the action cannot be performed, not that
// it was refused.
var ErrApprovalNotConfigured = errors.New("approval is not configured")

// ToolApprovalTimeout bounds how long dispatch waits for a human to act.
//
// Longer than this and the LLM connection may time out or the user may have
// walked away; shorter and the human may not have time to read and decide.
// It is the dispatch layer's deadline, not the adapter's, and adapters
// should apply their own shorter timeout to the pending token.
const ToolApprovalTimeout = 2 * time.Minute

// ── context plumbing ─────────────────────────────────────────────────

type approvalCtxKey struct{}

// WithApprovalRequester stores an ApprovalRequester on ctx so the tool
// dispatch layer can extract it without importing any adapter package.
// Callers that don't have an approver simply don't call this, and the
// dispatch layer treats a missing value as "not configured".
func WithApprovalRequester(ctx context.Context, a ApprovalRequester) context.Context {
	return context.WithValue(ctx, approvalCtxKey{}, a)
}

// ApprovalFromContext returns the ApprovalRequester stored by
// WithApprovalRequester, or nil when none was stored.
func ApprovalFromContext(ctx context.Context) ApprovalRequester {
	a, _ := ctx.Value(approvalCtxKey{}).(ApprovalRequester)
	return a
}
