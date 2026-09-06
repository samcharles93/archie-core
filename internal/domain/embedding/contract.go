// Package embedding is the embedding capability: a narrow typed boundary
// for turning text into vectors, so consumers (curators first, per bead
// archie-core-1786637500752-415-434e5bb9 / GitHub #436) never depend on a
// specific provider SDK.
//
// Unlike curator and memory (internal/domain/curator, internal/domain/
// memory), a Client has no Lifecycle: it is call-scoped, not long-running,
// the same reasoning internal/domain/image's Provider contract already
// uses -- "is this usable right now" is exactly what Embed returning
// ErrUnavailable already communicates.
package embedding

import (
	"context"
	"errors"
)

// Sentinel errors every implementation wraps with fmt.Errorf("%w: ...", ...)
// so callers can errors.Is regardless of backend.
var (
	// ErrUnavailable means the capability cannot serve right now: no role
	// is configured, the provider is unknown, or its credential could not
	// be resolved. Per the credential-missing-degrades-not-fatal rule
	// (AGENTS.md), this must never prevent the daemon from starting or
	// fail an unrelated chat turn -- a caller must degrade (skip the
	// content, fall back to free exploration) instead.
	ErrUnavailable = errors.New("embedding capability unavailable")
	// ErrEmptyInput means Embed was called with no texts.
	ErrEmptyInput = errors.New("embedding request has no input text")
)

// Vector is one embedding: a dense float32 vector.
type Vector []float32

// Client is the narrow typed contract every embedding backend implements.
type Client interface {
	// Embed returns one Vector per entry in texts, in the same order.
	// Network calls must carry a timeout; a failure is returned as an
	// error, never a panic. Callers must treat any non-nil error as
	// "skip this batch," matching image.Provider's ErrUnavailable
	// convention -- an embedding failure must never crash or fail an
	// unrelated caller.
	Embed(ctx context.Context, texts []string) ([]Vector, error)
}
