package tools

import (
	"context"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// ApprovalRequester asks a human to approve or deny a named action.
type ApprovalRequester = messaging.ApprovalRequester

// ApprovalDecision is the outcome of a human consent request.
type ApprovalDecision = messaging.ApprovalDecision

const (
	ApprovalApproved            = messaging.ApprovalApproved
	ApprovalPermanentlyApproved = messaging.ApprovalPermanentlyApproved
	ApprovalDenied              = messaging.ApprovalDenied
)

var (
	ErrApprovalDenied        = messaging.ErrApprovalDenied
	ErrApprovalNotConfigured = messaging.ErrApprovalNotConfigured
)

const ToolApprovalTimeout = messaging.ToolApprovalTimeout

// WithApprovalRequester stores an ApprovalRequester on ctx.
func WithApprovalRequester(ctx context.Context, a ApprovalRequester) context.Context {
	return messaging.WithApprovalRequester(ctx, a)
}

// ApprovalFromContext returns the ApprovalRequester stored by
// WithApprovalRequester, or nil when none was stored.
func ApprovalFromContext(ctx context.Context) ApprovalRequester {
	return messaging.ApprovalFromContext(ctx)
}
