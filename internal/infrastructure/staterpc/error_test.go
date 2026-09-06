package staterpc

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/samcharles93/archie-core/internal/store"
)

// TestUnmapErrorPreservesContextIdentity pins the §6 deadline contract that
// unmapError must rehydrate a gRPC Canceled / DeadlineExceeded status back to
// the standard context sentinels. The agent's deadlineStore bounds each Store
// call with context.WithTimeout and the workflow consumer checks
// errors.Is(err, context.DeadlineExceeded) to mark a stage interrupted rather
// than failed (workflow.go); if unmapError folded those into a sanitised
// internal error, a merely interrupted stage would be parked as a failure.
func TestUnmapErrorPreservesContextIdentity(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "cancelled", err: status.Error(codes.Canceled, context.Canceled.Error()), want: context.Canceled},
		{name: "deadline exceeded", err: status.Error(codes.DeadlineExceeded, context.DeadlineExceeded.Error()), want: context.DeadlineExceeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := unmapError(tt.err)
			if !errors.Is(got, tt.want) {
				t.Fatalf("unmapError(%q) = %v, want errors.Is(err, %v)", tt.err, got, tt.want)
			}
			if errors.Is(got, store.ErrStaleTransition) {
				t.Fatalf("unmapError(%q) = %v, must not map to a store sentinel", tt.err, got)
			}
		})
	}
}

// TestUnmapErrorSentinelFidelity pins the §7 sentinel rehydration round-trip
// for each store error code, so a consumer's errors.Is(err, store.ErrX) keeps
// working across the wire.
func TestUnmapErrorSentinelFidelity(t *testing.T) {
	tests := []struct {
		name  string
		store error
		rehyd error
	}{
		{name: "stale transition", store: store.ErrStaleTransition, rehyd: store.ErrStaleTransition},
		{name: "binding not found", store: store.ErrBindingNotFound, rehyd: store.ErrBindingNotFound},
		{name: "mapping not found", store: store.ErrMappingNotFound, rehyd: store.ErrMappingNotFound},
		{name: "binding overlap", store: store.ErrBindingOverlap, rehyd: store.ErrBindingOverlap},
		{name: "binding transition", store: store.ErrBindingTransition, rehyd: store.ErrBindingTransition},
		{name: "already dispatched", store: store.ErrAlreadyDispatched, rehyd: store.ErrAlreadyDispatched},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// mapError converts the store sentinel to a gRPC status; unmapError
			// must recover the same sentinel. This is the round-trip the wire
			// contract guarantees (§7).
			mapped := mapError(tt.store)
			got := unmapError(mapped)
			if !errors.Is(got, tt.rehyd) {
				t.Fatalf("round-trip %q -> %q: got %v, want %v", tt.store, mapped, got, tt.rehyd)
			}
		})
	}
}

// TestUnmapErrorDoesNotLeakInternalDetail pins the §7 sanitisation rule: an
// infra/unknown error maps to codes.Internal and must not rehydrate to a store
// sentinel, the public message must not carry the raw wrapped detail (SQL
// paths, provider details), and a non-status error is returned unchanged.
func TestUnmapErrorDoesNotLeakInternalDetail(t *testing.T) {
	const detail = "sql: path=/var/lib/archie/archie.db secret=abcdef"
	internal := mapError(errors.New(detail))
	got := unmapError(internal)
	if errors.Is(got, store.ErrStaleTransition) || errors.Is(got, store.ErrBindingNotFound) || errors.Is(got, store.ErrAlreadyDispatched) {
		t.Fatalf("unmapError(internal) = %v, must not map to a store sentinel", got)
	}
	if st, ok := status.FromError(got); !ok || st.Code() != codes.Internal {
		t.Fatalf("unmapError(internal) = %v, want a codes.Internal status", got)
	}
	if strings.Contains(got.Error(), detail) {
		t.Fatalf("unmapError leaked raw internal detail: %q", got.Error())
	}

	// A raw (non-status) error is returned unchanged.
	raw := errors.New("transport failure")
	if got := unmapError(raw); got != raw {
		t.Fatalf("unmapError(raw) = %v, want unchanged", got)
	}
}
