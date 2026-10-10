package readiness

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/health"
	infraaccess "github.com/samcharles93/archie-core/internal/infrastructure/access"
)

// TestProblemProbeFollowsTheChain pins that the access_policies probe reads
// the chain on every check: degraded while the policies have not loaded, ok
// once they do, without rebuilding the probe.
func TestProblemProbeFollowsTheChain(t *testing.T) {
	up := false
	chain := infraaccess.NewPending(func(context.Context) ([]access.Policy, error) {
		if !up {
			return nil, errors.New("store down")
		}
		return nil, nil
	}, slog.New(slog.DiscardHandler))
	probe := NewProblemProbe("access_policies", chain)

	if got := probe.Check(t.Context()); got.Status != health.StatusDegraded {
		t.Fatalf("before load = %+v, want degraded", got)
	}
	up = true
	if err := chain.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := probe.Check(t.Context()); got.Status != health.StatusOK {
		t.Fatalf("after load = %+v, want ok", got)
	}
}
