// Package embedding defines the text embedding contract.
package embedding

import (
	"context"
	"errors"
)

// Sentinel errors every implementation wraps with fmt.Errorf("%w: ...", ...)
// so callers can errors.Is regardless of backend.
var (
	// ErrUnavailable means embedding is not configured or cannot run; callers
	// degrade.
	ErrUnavailable = errors.New("embedding capability unavailable")
	// ErrEmptyInput means Embed was called with no texts.
	ErrEmptyInput = errors.New("embedding request has no input text")
)

// Vector is one embedding: a dense float32 vector.
type Vector []float32

// Client is the narrow typed contract every embedding backend implements.
type Client interface {
	// Embed returns one Vector per text, in order.
	Embed(ctx context.Context, texts []string) ([]Vector, error)
}
