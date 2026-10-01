package telegram

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot"

	"github.com/samcharles93/archie-core/internal/channels"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

func newTestDeleter(t *testing.T, serverURL string, chatID int64) messaging.MessageDeleter {
	t.Helper()
	b, err := bot.New("1:test", bot.WithServerURL(serverURL), bot.WithSkipGetMe())
	if err != nil {
		t.Fatalf("new test bot: %v", err)
	}
	return New("1:test", []int64{42}, slog.New(slog.DiscardHandler)).NewMessageDeleter(b, chatID)
}

// The delete capability must be reported, so an ephemeral reply's retraction
// path can be chosen without attempting a doomed call.
func TestMessageDeleterReportsDeleteCapability(t *testing.T) {
	_, srv := newMediaAPI()
	defer srv.Close()

	deleter := newTestDeleter(t, srv.URL, 555)
	if caps := messaging.CapabilitiesOf(deleter); !caps.Delete {
		t.Error("telegram message deleter does not report the Delete capability")
	}
}

// A retraction must reach the Bot API's deleteMessage method with the chat and
// the message the send returned.
func TestMessageDeleterCallsDeleteMessage(t *testing.T) {
	api, srv := newMediaAPI()
	api.body = `{"ok":true,"result":true}`
	defer srv.Close()

	deleter := newTestDeleter(t, srv.URL, 555)
	if err := deleter.DeleteMessage(t.Context(), messaging.MessageEvent{ID: "77"}); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}

	method, form := api.called()
	if method != "deleteMessage" {
		t.Fatalf("Bot API method = %q, want deleteMessage", method)
	}
	if form["message_id"] != "77" {
		t.Errorf("message_id = %q, want 77", form["message_id"])
	}
	if form["chat_id"] != "555" {
		t.Errorf("chat_id = %q, want the bound chat 555", form["chat_id"])
	}
}

// A message ID the platform never issued cannot address a message, so it is
// reported before any request rather than sent as a doomed one.
func TestMessageDeleterRejectsNonNumericID(t *testing.T) {
	api, srv := newMediaAPI()
	api.body = `{"ok":true,"result":true}`
	defer srv.Close()

	deleter := newTestDeleter(t, srv.URL, 555)
	if err := deleter.DeleteMessage(t.Context(), messaging.MessageEvent{ID: "not-a-number"}); err == nil {
		t.Fatal("DeleteMessage accepted a non-numeric message ID")
	}
	if method, _ := api.called(); method != "" {
		t.Fatalf("Bot API method = %q, want no request for an undeliverable ID", method)
	}
}

// An API failure must surface as an error so the caller can log it; a failed
// retraction is never silent.
func TestMessageDeleterReportsAPIError(t *testing.T) {
	api, srv := newMediaAPI()
	api.body = `{"ok":false,"error_code":400,"description":"message to delete not found"}`
	defer srv.Close()

	deleter := newTestDeleter(t, srv.URL, 555)
	if err := deleter.DeleteMessage(t.Context(), messaging.MessageEvent{ID: "404"}); err == nil {
		t.Fatal("DeleteMessage reported success for a rejected request")
	}
}

// sendEphemeral is the transport honouring an ephemeral reply end to end: it
// sends the status notice, then retracts the message the send returned once
// the injected clock's TTL fires. The injected sender keeps the test off a
// real eight-second wait.
func TestSendEphemeralRetractsStatusNotice(t *testing.T) {
	type call struct {
		method string
		form   map[string]string
	}
	calls := make(chan call, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		method := parts[len(parts)-1]
		_ = r.ParseMultipartForm(1 << 20)
		form := map[string]string{}
		for k, v := range r.Form {
			if len(v) > 0 {
				form[k] = v[0]
			}
		}
		calls <- call{method: method, form: form}

		w.Header().Set("Content-Type", "application/json")
		if method == "deleteMessage" {
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":321}}`))
	}))
	defer srv.Close()

	b, err := bot.New("1:test", bot.WithServerURL(srv.URL), bot.WithSkipGetMe())
	if err != nil {
		t.Fatalf("new test bot: %v", err)
	}

	g := New("1:test", []int64{42}, slog.New(slog.DiscardHandler))
	g.newEphemeralSender = func() *channels.EphemeralSender {
		return channels.NewEphemeralSender(g.log, func(time.Duration) <-chan time.Time {
			ch := make(chan time.Time)
			close(ch)
			return ch
		})
	}

	g.sendEphemeral(t.Context(), b, 555, 0, "✅ Archie reloaded.")

	var sent, retracted *call
	deadline := time.After(2 * time.Second)
	for sent == nil || retracted == nil {
		select {
		case c := <-calls:
			switch c.method {
			case "sendRichMessage", "sendMessage":
				sent = &c
			case "deleteMessage":
				retracted = &c
			}
		case <-deadline:
			t.Fatalf("timed out; send=%v retraction=%v", sent, retracted)
		}
	}

	if retracted.form["message_id"] != "321" {
		t.Errorf("retracted message_id = %q, want the sent message 321", retracted.form["message_id"])
	}
	if retracted.form["chat_id"] != "555" {
		t.Errorf("retraction chat_id = %q, want the chat the notice was sent to", retracted.form["chat_id"])
	}
}
