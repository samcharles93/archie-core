package telegram

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"

	"github.com/samcharles93/archie-core/internal/releaseupdate"
)

// contextAwareUpdateStub embeds the shared updateStub so the interface's
// Check/Defer/CanInstall come for free, and overrides only Install to
// capture the context the gateway hands to the installer. Capturing a
// context in a test double is the accepted exception to "don't store
// contexts": the regression test needs to observe which context reached
// Install.
type contextAwareUpdateStub struct {
	updateStub
	started    chan struct{}
	installCtx context.Context //nolint:containedctx // test double must observe which context reached Install
}

func (s *contextAwareUpdateStub) Install(ctx context.Context, _ releaseupdate.Snapshot, _ releaseupdate.InstallMeta, _ func(string)) (releaseupdate.Result, error) {
	s.installCtx = ctx
	close(s.started)
	return releaseupdate.Result{}, nil
}

func TestInstallUpdateDetachesInstallerFromCallbackContext(t *testing.T) {
	g := New("1:test", "", "", []int64{42}, slog.Default())
	stub := &contextAwareUpdateStub{started: make(chan struct{})}
	g.Updates = stub
	b, _ := newTelegramTestBot(t)
	// t.Context() is the framework-owned parent: it auto-cancels at test
	// teardown, so a failure before cancel() can't orphan the goroutine's
	// context. We still derive a cancellable child because the test must
	// cancel the callback context itself -- mirroring Telegram's handler
	// lifetime ending -- while the installer is mid-flight.
	callbackCtx, cancel := context.WithCancel(t.Context())
	message := &models.Message{Chat: models.Chat{ID: 7}, ID: 100}

	done := make(chan struct{})
	go func() {
		g.installUpdate(callbackCtx, b, &models.CallbackQuery{
			From:    models.User{ID: 42},
			Message: models.MaybeInaccessibleMessage{Message: message},
		}, releaseupdate.Snapshot{})
		close(done)
	}()
	<-stub.started
	cancel()

	select {
	case <-stub.installCtx.Done():
		t.Fatal("installer context was cancelled with the callback context")
	case <-time.After(20 * time.Millisecond):
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("installUpdate did not finish")
	}
}
