// Package gateway: interactive.go re-exports the adapter interactive-UX
// contract from internal/domain/messaging so gateway-internal code and
// channel tests can name it without qualifying the domain package.
//
// The contract is the interactive counterpart of the media/delete
// capabilities in adapter_capabilities.go: clarify and picker are optional
// interfaces an adapter type-asserts through ClarifierOf/PickerOf, and
// approval reuses the existing ApprovalRequester flow rather than a second
// decision model. TextFallback is the shared text degrade for an adapter
// with no native question or selection UI.

package gateway

import "github.com/samcharles93/archie-core/internal/domain/messaging"

// Interactive request and answer types.
type (
	InteractiveChoice = messaging.InteractiveChoice
	ClarifyRequest    = messaging.ClarifyRequest
	PickerRequest     = messaging.PickerRequest
)

// Optional interactive interfaces an adapter may implement.
type (
	ClarifyRequester = messaging.ClarifyRequester
	PickerRequester  = messaging.PickerRequester
)

// Interactive is the per-turn bundle of requesters a channel adapter can
// carry. WithInteractive stores it on a turn's context and
// InteractiveFromContext reads it back, so the gateway can offer a question
// tool only when the channel can answer it.
type Interactive = messaging.Interactive

// InteractiveOf resolves the requesters a channel adapter supports.
//
// WithInteractive / InteractiveFromContext are the context carrier.
var (
	InteractiveOf          = messaging.InteractiveOf
	WithInteractive        = messaging.WithInteractive
	InteractiveFromContext = messaging.InteractiveFromContext
)

// TextFallback is the shared text-based degrade.
type TextFallback = messaging.TextFallback

// Re-exported sentinel errors.
var (
	ErrNoPickerOptions           = messaging.ErrNoPickerOptions
	ErrEmptyReply                = messaging.ErrEmptyReply
	ErrTextFallbackNotConfigured = messaging.ErrTextFallbackNotConfigured
)

// Re-exported capability lookups.
var (
	ClarifierOf = messaging.ClarifierOf
	PickerOf    = messaging.PickerOf
	ApproverOf  = messaging.ApproverOf
)

// Re-exported text rendering and parsing helpers.
var (
	FormatClarifyText = messaging.FormatClarifyText
	FormatPickerText  = messaging.FormatPickerText
	ParsePickerReply  = messaging.ParsePickerReply
)
