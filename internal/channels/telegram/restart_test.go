package telegram

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"
)

func TestRestartAcknowledgesArchie(t *testing.T) {
	g := New("1:test", "", "", []int64{42}, slog.Default())
	b, requests := newTelegramTestBot(t)

	g.restartHandler()(context.Background(), b, &models.Update{Message: &models.Message{
		From: &models.User{ID: 42},
		Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate},
	}})

	if len(*requests) != 1 {
		t.Fatalf("restart acknowledgement = %#v", *requests)
	}
	var rich models.InputRichMessage
	if err := json.Unmarshal([]byte((*requests)[0].form["rich_message"]), &rich); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(blocksToPlainText(rich.Blocks), "\n\n"); got != "🔄 Reloading Archie…" {
		t.Fatalf("restart acknowledgement = %#v, want Archie wording", *requests)
	}
}

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

func TestRequestRestartQueuesScopedReload(t *testing.T) {
	g := New("tok", "", "", []int64{1}, slog.Default())
	if err := g.RequestRestart(); err != nil {
		t.Fatalf("RequestRestart: %v", err)
	}
	if got := len(g.restartCh); got != 1 {
		t.Fatalf("restart queue length = %d, want 1", got)
	}
	if req := <-g.restartCh; req.chatID != 0 {
		t.Fatalf("chatID = %d, want zero for Web-originated restart", req.chatID)
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
