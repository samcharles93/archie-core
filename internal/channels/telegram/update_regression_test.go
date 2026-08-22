package telegram

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"

	"github.com/samcharles93/archie-core/internal/releaseupdate"
)

type contextAwareUpdateStub struct {
	started chan struct{}
	seen    context.Context
}

func (s *contextAwareUpdateStub) Check(context.Context, int64) (releaseupdate.Snapshot, error) {
	return releaseupdate.Snapshot{}, nil
}

func (s *contextAwareUpdateStub) Defer(context.Context, int64, releaseupdate.Snapshot) error {
	return nil
}
func (s *contextAwareUpdateStub) CanInstall() bool { return true }
func (s *contextAwareUpdateStub) Install(ctx context.Context, _ releaseupdate.InstallMeta, _ func(string)) (releaseupdate.Result, error) {
	s.seen = ctx
	close(s.started)
	return releaseupdate.Result{}, nil
}

func TestInstallUpdateDetachesInstallerFromCallbackContext(t *testing.T) {
	g := New("1:test", "", "", []int64{42}, slog.Default())
	stub := &contextAwareUpdateStub{started: make(chan struct{})}
	g.Updates = stub
	b, _ := newTelegramTestBot(t)
	callbackCtx, cancel := context.WithCancel(context.Background())
	message := &models.Message{Chat: models.Chat{ID: 7}, ID: 100}

	done := make(chan struct{})
	go func() {
		g.installUpdate(callbackCtx, b, &models.CallbackQuery{
			From:    models.User{ID: 42},
			Message: models.MaybeInaccessibleMessage{Message: message},
		})
		close(done)
	}()
	<-stub.started
	cancel()

	select {
	case <-stub.seen.Done():
		t.Fatal("installer context was cancelled with the callback context")
	case <-time.After(20 * time.Millisecond):
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("installUpdate did not finish")
	}
}
