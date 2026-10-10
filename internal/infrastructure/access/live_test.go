package access

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/access"
)

// TestPendingChainRefusesUntilLoaded pins that a chain built before its store
// is up refuses every request until the first load succeeds, then allows.
func TestPendingChainRefusesUntilLoaded(t *testing.T) {
	loaded := false
	live := NewPending(func(context.Context) ([]access.Policy, error) {
		if !loaded {
			return nil, errors.New("store down")
		}
		return nil, nil
	}, slog.New(slog.DiscardHandler))

	request := func() access.Decision {
		return live.Authorize(access.SharedTokenOwner(), access.ActionRead,
			access.Resource{Kind: access.KindDashboard}, access.Context{})
	}

	if decision := request(); decision.Allowed || !errors.Is(decision.Err, access.ErrChainUnavailable) {
		t.Fatalf("before load = %+v, want the not-loaded refusal", decision)
	}
	if live.Ready() {
		t.Fatal("Ready before load = true, want false")
	}
	if got := live.Problems(); got != nil {
		t.Fatalf("Problems before load = %v, want nil", got)
	}
	if decision := live.AuthorizeDelivery("acme", "sources/x", "10.0.0.1"); decision.Allowed ||
		!errors.Is(decision.Err, access.ErrChainUnavailable) {
		t.Fatalf("delivery before load = %+v, want the not-loaded refusal", decision)
	}

	loaded = true
	if err := live.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !live.Ready() {
		t.Fatal("Ready after load = false, want true")
	}
	if decision := request(); !decision.Allowed {
		t.Fatalf("after load = %+v, want allowed", decision)
	}
}
