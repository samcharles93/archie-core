package gateway

import "github.com/samcharles93/archie-core/internal/tools"

// Re-exported types so gateway-internal code uses the same concrete types
// as the dispatch layer without qualification churn.
type (
	ApprovalRequester = tools.ApprovalRequester
	ApprovalDecision  = tools.ApprovalDecision
)

// Re-exported constants.
const (
	ApprovalApproved            = tools.ApprovalApproved
	ApprovalPermanentlyApproved = tools.ApprovalPermanentlyApproved
	ApprovalDenied              = tools.ApprovalDenied
	ToolApprovalTimeout         = tools.ToolApprovalTimeout
)

// Re-exported sentinel errors.
var (
	ErrApprovalDenied        = tools.ErrApprovalDenied
	ErrApprovalNotConfigured = tools.ErrApprovalNotConfigured
)

// Re-exported context plumbing.
var (
	WithApprovalRequester = tools.WithApprovalRequester
	ApprovalFromContext   = tools.ApprovalFromContext
)
