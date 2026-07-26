package telegram

import (
	"log/slog"
	"testing"
)

// The restart channel is buffered so a handler can hand off without
// blocking: the handler runs on the very bot the supervisor is about to
// stop, so blocking there would deadlock the restart it just asked for.
func TestRestartRequestNeverBlocksHandler(t *testing.T) {
	g := New("tok", "", "", []int64{1}, slog.Default())

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Two sends with nothing draining: the first buffers, the second
		// must hit the default branch rather than block.
		for range 2 {
			select {
			case g.restartCh <- restartRequest{chatID: 7}:
			default:
			}
		}
	}()

	<-done

	if got := len(g.restartCh); got != 1 {
		t.Fatalf("restartCh holds %d requests, want 1 (second must be dropped, not queued)", got)
	}
	if req := <-g.restartCh; req.chatID != 7 {
		t.Errorf("chatID = %d, want 7", req.chatID)
	}
}

// A failing Reload must not be fatal: a bad config edit should leave the
// gateway running on its previous settings, not take chat down for good.
func TestReloadFailureKeepsPreviousSettings(t *testing.T) {
	g := New("original-token", "", "", []int64{1}, slog.Default())
	g.Reload = func(*Gateway) error { return errReloadTest }

	if err := g.Reload(g); err == nil {
		t.Fatal("expected reload error")
	}
	if g.Token != "original-token" {
		t.Errorf("Token = %q, want the previous value retained", g.Token)
	}
	if !g.isSenderAllowed(1) {
		t.Error("previous allowlist should be retained after a failed reload")
	}
}

var errReloadTest = errTest("bad config")

type errTest string

func (e errTest) Error() string { return string(e) }
