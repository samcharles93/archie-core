// Package source owns the capture source: the endpoint one sender posts to,
// and its signing setting. See docs/prds/event-automation.md "Sources".
package source

import (
	"errors"
	"time"
)

// Signing is a source's signing state. A source is signed by default; turning
// signing off is requested and then approved, the same two-step gate as
// arming a binding, and only the approved state accepts unsigned events.
type Signing string

const (
	SigningSigned          Signing = "signed"
	SigningUnsignedPending Signing = "unsigned_pending_approval"
	SigningUnsigned        Signing = "unsigned"
)

// MaxPathLen bounds a custom path.
const MaxPathLen = 128

// MinSecretLen is the length of a generated secret: 32 random bytes, hex.
const MinSecretLen = 64

var (
	// ErrInvalidPath refuses a custom path that is not URL-safe.
	ErrInvalidPath = errors.New("source: path must be 1-128 URL-safe characters (A-Z a-z 0-9 - . _ ~), not a dot segment")
	// ErrSigningTransition refuses an approval with no pending request.
	ErrSigningTransition = errors.New("source: signing transition rejected")
)

// Source is one capture endpoint. Path is its URL segment and its identity:
// captures and binding matchers reference it, so it does not change after
// creation. Secret is the HMAC key a signed sender signs with.
type Source struct {
	Path      string    `json:"path"`
	Signing   Signing   `json:"signing"`
	Secret    string    `json:"secret,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// New builds a signed source with a generated secret. An empty customPath
// gets a UUIDv7 path; a non-empty one must be URL-safe. Uniqueness is the
// store's to enforce.
func New(customPath string) (Source, error) {
	path := customPath
	if path == "" {
		generated, err := NewPath()
		if err != nil {
			return Source{}, err
		}
		path = generated
	} else if err := ValidatePath(path); err != nil {
		return Source{}, err
	}
	secret, err := NewSecret()
	if err != nil {
		return Source{}, err
	}
	return Source{Path: path, Signing: SigningSigned, Secret: secret}, nil
}

// Unsigned reports whether events on this source dispatch without a
// signature. Only an approved request counts.
func (s *Source) Unsigned() bool { return s.Signing == SigningUnsigned }

// RequestUnsigned asks to turn signing off. The source stays signed until
// ApproveUnsigned.
func (s *Source) RequestUnsigned() error {
	if s.Signing == SigningSigned {
		s.Signing = SigningUnsignedPending
	}
	return nil
}

// ApproveUnsigned is the only transition that turns signing off.
func (s *Source) ApproveUnsigned() error {
	if s.Signing != SigningUnsignedPending {
		return ErrSigningTransition
	}
	s.Signing = SigningUnsigned
	return nil
}

// RequireSigning turns signing back on. It needs no approval: it only
// narrows what dispatches.
func (s *Source) RequireSigning() error {
	s.Signing = SigningSigned
	return nil
}
