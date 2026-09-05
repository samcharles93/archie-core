package readiness

import (
	"context"
	"errors"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/health"
)

type fakeCounter struct {
	err error
}

func (f fakeCounter) StatusCounts(context.Context) (map[string]int, error) {
	return map[string]int{}, f.err
}

func TestStoreProbe_ReadyOnCleanQuery(t *testing.T) {
	p := NewStoreProbe(fakeCounter{})
	if got := p.Check(context.Background()); got.Status != health.StatusOK {
		t.Fatalf("status = %q, want ok", got.Status)
	}
}

func TestStoreProbe_DegradedOnError(t *testing.T) {
	p := NewStoreProbe(fakeCounter{err: errors.New("database is locked")})
	got := p.Check(context.Background())
	if got.Status != health.StatusDegraded {
		t.Fatalf("status = %q, want degraded", got.Status)
	}
	if got.Detail == "" {
		t.Fatalf("detail should explain the failure")
	}
}

func TestStoreProbe_DegradedWhenUnwired(t *testing.T) {
	p := &StoreProbe{}
	if got := p.Check(context.Background()); got.Status != health.StatusDegraded {
		t.Fatalf("status = %q, want degraded", got.Status)
	}
}

func TestStoreProbe_Name(t *testing.T) {
	if got := NewStoreProbe(fakeCounter{}).Name(); got != "state_db" {
		t.Fatalf("name = %q, want state_db", got)
	}
}

func TestConfigProbe_ReadyOnValidConfig(t *testing.T) {
	p := NewConfigProbe(func() config.Config { return config.Config{BotUser: "b"} }, func(*config.Config) error { return nil })
	if got := p.Check(context.Background()); got.Status != health.StatusOK {
		t.Fatalf("status = %q, want ok", got.Status)
	}
}

func TestConfigProbe_DegradedOnInvalidConfig(t *testing.T) {
	p := NewConfigProbe(func() config.Config { return config.Config{} }, func(*config.Config) error { return errors.New("bot_user required") })
	got := p.Check(context.Background())
	if got.Status != health.StatusDegraded {
		t.Fatalf("status = %q, want degraded", got.Status)
	}
	if got.Detail == "" {
		t.Fatalf("detail should explain the validation failure")
	}
}

func TestConfigProbe_DegradedWhenGetIsNil(t *testing.T) {
	p := NewConfigProbe(nil, func(*config.Config) error { return nil })
	if got := p.Check(context.Background()); got.Status != health.StatusDegraded {
		t.Fatalf("status = %q, want degraded", got.Status)
	}
}

func TestConfigProbe_DegradedWhenValidatorIsNil(t *testing.T) {
	p := NewConfigProbe(func() config.Config { return config.Config{} }, nil)
	if got := p.Check(context.Background()); got.Status != health.StatusDegraded {
		t.Fatalf("status = %q, want degraded", got.Status)
	}
}

func TestDiskProbe_Name(t *testing.T) {
	if got := NewDiskProbe(".").Name(); got != "disk" {
		t.Fatalf("name = %q, want disk", got)
	}
}

func TestDiskProbe_DegradedOnEmptyPath(t *testing.T) {
	p := NewDiskProbe("")
	if got := p.Check(context.Background()); got.Status != health.StatusDegraded {
		t.Fatalf("status = %q, want degraded", got.Status)
	}
}

func TestDiskProbe_DegradedOnBogusPath(t *testing.T) {
	p := NewDiskProbe("/this/path/does/not/exist/zzz")
	if got := p.Check(context.Background()); got.Status != health.StatusDegraded {
		t.Fatalf("status = %q, want degraded", got.Status)
	}
}

func TestDiskProbe_ReadyOnRealFilesystem(t *testing.T) {
	p := NewDiskProbe(t.TempDir())
	if got := p.Check(context.Background()); got.Status != health.StatusOK {
		t.Fatalf("status = %q, want ok (temp dir should be nearly empty): %s", got.Status, got.Detail)
	}
}

func TestDiskProbe_ThresholdLogic(t *testing.T) {
	// A path that exists is a real filesystem; the threshold is the only
	// thing we can meaningfully vary here without mocking statfs. Assert that
	// a generous threshold (well above 100%) always passes, which it does
	// for any real used fraction.
	p := &DiskProbe{Path: t.TempDir(), Threshold: 2.0}
	if got := p.Check(context.Background()); got.Status != health.StatusOK {
		t.Fatalf("status = %q, want ok with threshold > 100%%", got.Status)
	}
}

func TestModelProbe_DegradedWhenNoModel(t *testing.T) {
	p := NewModelProbe(func() string { return "" }, func() []string { return nil }, nil)
	if got := p.Check(context.Background()); got.Status != health.StatusDegraded {
		t.Fatalf("status = %q, want degraded", got.Status)
	}
}

func TestModelProbe_ReadyOnConfiguredNoReachCheck(t *testing.T) {
	p := NewModelProbe(func() string { return "openai/gpt-4o" }, func() []string { return []string{"openai/gpt-4o"} }, nil)
	got := p.Check(context.Background())
	if got.Status != health.StatusOK {
		t.Fatalf("status = %q, want ok", got.Status)
	}
	if got.Detail == "" {
		t.Fatalf("detail should name the active model")
	}
}

func TestModelProbe_ReachSuccess(t *testing.T) {
	p := NewModelProbe(func() string { return "openai/gpt-4o" }, func() []string { return []string{"openai/gpt-4o"} }, func(context.Context) error { return nil })
	if got := p.Check(context.Background()); got.Status != health.StatusOK {
		t.Fatalf("status = %q, want ok", got.Status)
	}
}

func TestModelProbe_ReachFailure(t *testing.T) {
	p := NewModelProbe(func() string { return "openai/gpt-4o" }, func() []string { return []string{"openai/gpt-4o"} }, func(context.Context) error { return errors.New("connection refused") })
	got := p.Check(context.Background())
	if got.Status != health.StatusDegraded {
		t.Fatalf("status = %q, want degraded", got.Status)
	}
	if got.Detail == "" {
		t.Fatalf("detail should explain the reachability failure")
	}
}

func TestGatewayProbe_ReadyWithRunningChannels(t *testing.T) {
	p := NewGatewayProbe(func() []ChannelState {
		return []ChannelState{{ID: "telegram", Configured: true, State: "running"}}
	}, func(context.Context) int { return 3 })
	got := p.Check(context.Background())
	if got.Status != health.StatusOK {
		t.Fatalf("status = %q, want ok", got.Status)
	}
	if got.Detail == "" {
		t.Fatalf("detail should report channel and session counts")
	}
}

func TestGatewayProbe_DegradedWhenNoChannelsConfigured(t *testing.T) {
	p := NewGatewayProbe(func() []ChannelState {
		return []ChannelState{{ID: "telegram", Configured: false, State: "stopped"}}
	}, func(context.Context) int { return 0 })
	if got := p.Check(context.Background()); got.Status != health.StatusDegraded {
		t.Fatalf("status = %q, want degraded", got.Status)
	}
}

func TestGatewayProbe_DegradedWhenChannelFailed(t *testing.T) {
	p := NewGatewayProbe(func() []ChannelState {
		return []ChannelState{
			{ID: "telegram", Configured: true, State: "failed"},
			{ID: "email", Configured: true, State: "running"},
		}
	}, func(context.Context) int { return 5 })
	if got := p.Check(context.Background()); got.Status != health.StatusDegraded {
		t.Fatalf("status = %q, want degraded", got.Status)
	}
}

func TestGatewayProbe_DegradedWhenNoChannelRunning(t *testing.T) {
	p := NewGatewayProbe(func() []ChannelState {
		return []ChannelState{{ID: "telegram", Configured: true, State: "stopped"}}
	}, func(context.Context) int { return 0 })
	if got := p.Check(context.Background()); got.Status != health.StatusDegraded {
		t.Fatalf("status = %q, want degraded", got.Status)
	}
}
