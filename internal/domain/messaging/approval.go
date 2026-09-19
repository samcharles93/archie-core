package messaging

import (
	"context"
	"errors"
	"time"
)

// ApprovalRequester asks a human to approve or deny a named action.
type ApprovalRequester interface {
	RequestApproval(ctx context.Context, action, description string) (ApprovalDecision, error)
}

// ApprovalDecision is the outcome of a human consent request.
type ApprovalDecision int

const (
	// ApprovalApproved means the human approved this one execution.
	ApprovalApproved ApprovalDecision = iota

	// ApprovalPermanentlyApproved means the human approved this action
	// for the lifetime of the session.
	ApprovalPermanentlyApproved

	// ApprovalDenied means the human explicitly refused.
	ApprovalDenied
)

// ErrApprovalDenied is returned when the human refuses the action.
var ErrApprovalDenied = errors.New("approval denied")

// ErrApprovalNotConfigured is returned when a tool requires approval but no
// ApprovalRequester is wired.
var ErrApprovalNotConfigured = errors.New("approval is not configured")

// ToolApprovalTimeout bounds how long dispatch waits for a human to act.
const ToolApprovalTimeout = 2 * time.Minute

type approvalCtxKey struct{}

// WithApprovalRequester stores an ApprovalRequester on ctx.
func WithApprovalRequester(ctx context.Context, a ApprovalRequester) context.Context {
	return context.WithValue(ctx, approvalCtxKey{}, a)
}

// ApprovalFromContext returns the ApprovalRequester stored on ctx.
func ApprovalFromContext(ctx context.Context) ApprovalRequester {
	a, _ := ctx.Value(approvalCtxKey{}).(ApprovalRequester)
	return a
}
