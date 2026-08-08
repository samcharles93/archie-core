// Package gateway: approval.go — re-exports the tool-approval contract
// from internal/tools so existing gateway-internal and adapter code
// continues to compile against the names it already uses.
//
// The canonical definitions live in internal/tools/approval.go so that
// internal/agentexec (also used by the archie-agent sandbox binary) can
// import them without pulling in the chat-gateway domain stack.

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
