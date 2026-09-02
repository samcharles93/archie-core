// Package image is the image generation capability family: a
// provider-neutral boundary for hosted (gpt-image-2-class) and local GPU
// backends, so /image routing can target either without depending on
// provider-specific types. See docs/prds/image-capability-contract.md for
// the design and epic archie-core-1786748942120-1-6636e629.
//
// Unlike curator and memory (internal/domain/curator,
// internal/domain/memory), a Provider has no Lifecycle: it is call-scoped,
// not long-running. There is nothing to Start, and "is this usable right
// now" is exactly what Generate/Edit returning ErrUnavailable already
// communicates — a separate Health poll would duplicate that. This package
// defines the contract only; no provider implementation lives here.
package image

import (
	"context"
	"errors"
	"time"
)

// Sentinel errors every provider wraps with fmt.Errorf("%w: %s", ...) so
// callers can errors.Is regardless of backend.
var (
	// ErrUnavailable means the provider cannot serve right now (missing
	// credential, backend down). Per the epic: this must degrade the
	// capability to a reported error, never prevent Archie from starting
	// or break text chat.
	ErrUnavailable = errors.New("image provider unavailable")
	// ErrUnsupported means the request needs a capability (e.g. Edit, or
	// Mask) the provider does not declare.
	ErrUnsupported = errors.New("image capability unsupported by provider")
	// ErrInputTooLarge means an input image exceeded a limit the provider
	// itself enforces. Caller-side limits are the routing layer's concern,
	// not this contract's — see the design doc.
	ErrInputTooLarge = errors.New("image input exceeds configured limit")
	// ErrTimeout means the provider's own deadline elapsed, distinct from
	// the caller's ctx being canceled.
	ErrTimeout = errors.New("image request timed out")
	// ErrCanceled means the caller's ctx was canceled.
	ErrCanceled = errors.New("image request canceled")
)

// ProviderClass distinguishes hosted (spends money/quota per call) from
// local (spends host GPU/time). Selection and config validation key off
// this, never off Name, so a new provider cannot silently slip through the
// hosted-cost gate under an unrecognized name.
type ProviderClass string

const (
	ClassHosted ProviderClass = "hosted"
	ClassLocal  ProviderClass = "local"
)

// Capability declares what one provider supports, discoverable before a
// request is built so a caller can reject an unsupported combination (e.g.
// edit-with-mask on a generate-only provider) instead of sending it and
// parsing a provider-specific error back out.
type Capability struct {
	Generate bool
	// Edit requires at least one input image. MaxInputs bounds how many.
	Edit      bool
	MaxInputs int
	// Mask reports whether Edit supports restricting the edited region to
	// a mask image.
	Mask bool
	// Sizes lists provider-declared output sizes (e.g. "1024x1024"). Empty
	// means the provider does not constrain size.
	Sizes []string
}

// Validate checks that a declared Capability is internally consistent:
// Edit implies at least one allowed input, and Mask cannot be declared
// without Edit.
func (c Capability) Validate() error {
	if c.Edit && c.MaxInputs < 1 {
		return errors.New("image capability: edit requires MaxInputs >= 1")
	}
	if c.Mask && !c.Edit {
		return errors.New("image capability: mask requires edit")
	}
	return nil
}

// ImageData carries image bytes in-process, never a path or URL: the
// domain layer must not assume a filesystem or a fetchable location.
type ImageData struct {
	Bytes    []byte
	MIMEType string
}

// GenerateRequest asks a provider to create images from a prompt alone.
type GenerateRequest struct {
	Prompt string
	// Size is a provider-declared value from Capability.Sizes, or "" for
	// the provider's default.
	Size string
	// N is the number of images requested. Zero means 1.
	N int
	// Options carries provider-specific overflow (e.g. "quality"),
	// deliberately untyped so the contract does not grow a field per
	// provider.
	Options map[string]string
}

// Validate checks the fields every provider needs regardless of backend.
func (r GenerateRequest) Validate() error {
	if r.Prompt == "" {
		return errors.New("image generate request: prompt must not be empty")
	}
	return nil
}

// EditRequest asks a provider to edit one or more input images per a
// prompt, optionally restricted to a masked region.
type EditRequest struct {
	Prompt string
	Inputs []ImageData
	// Mask restricts the edited region. Only valid when the provider's
	// Capability.Mask is true.
	Mask *ImageData
	Size string
	N    int

	Options map[string]string
}

// Validate checks the fields every provider needs regardless of backend.
// Capability-specific bounds (MaxInputs, Mask support) are the provider's
// own responsibility to enforce against its declared Capability.
func (r EditRequest) Validate() error {
	if r.Prompt == "" {
		return errors.New("image edit request: prompt must not be empty")
	}
	if len(r.Inputs) == 0 {
		return errors.New("image edit request: at least one input image is required")
	}
	return nil
}

// Result is what a provider returns from Generate or Edit.
type Result struct {
	Images    []ImageData
	Provider  string
	Model     string
	CreatedAt time.Time
}

// Provider is the typed contract every image backend implements — hosted
// (gpt-image-2-class) or local (GPU recipe).
type Provider interface {
	Name() string
	Class() ProviderClass
	Capability() Capability
	Generate(ctx context.Context, req GenerateRequest) (Result, error)
	Edit(ctx context.Context, req EditRequest) (Result, error)
}
