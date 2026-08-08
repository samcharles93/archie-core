package channels

import "testing"

func TestManagerTracksConfiguredLifecycleAndReloadCapability(t *testing.T) {
	m := NewManager([]Descriptor{
		{ID: "telegram", Name: "Telegram", Configured: true, ReloadSupported: true},
		{ID: "email", Name: "Email", Configured: false},
	})

	if got := m.Snapshot()[0].State; got != StateConfigured {
		t.Fatalf("initial state = %q, want %q", got, StateConfigured)
	}
	m.MarkStarting("telegram")
	m.MarkRunning("telegram")
	m.MarkFailed("telegram", "token rejected")

	views := m.Snapshot()
	if got := views[0]; got.State != StateFailed || got.Detail != "token rejected" || !got.ReloadSupported {
		t.Errorf("telegram = %#v", got)
	}
	if got := views[1]; got.State != StateStopped || got.Configured {
		t.Errorf("email = %#v", got)
	}
}
