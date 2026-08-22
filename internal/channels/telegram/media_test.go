package telegram

import (
	"context"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-telegram/bot"

	"github.com/samcharles93/archie-core/internal/gateway"
)

// mediaAPI stands in for the Bot API, recording which sendX method was
// called and the form values it received.
type mediaAPI struct {
	mu     sync.Mutex
	method string
	form   map[string]string
	body   string
}

func newMediaAPI() (*mediaAPI, *httptest.Server) {
	api := &mediaAPI{form: map[string]string{}, body: `{"ok":true,"result":{"message_id":77}}`}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.mu.Lock()
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		api.method = parts[len(parts)-1]
		if err := r.ParseMultipartForm(1 << 20); err == nil {
			for k, v := range r.Form {
				if len(v) > 0 {
					api.form[k] = v[0]
				}
			}
		}
		body := api.body
		api.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	return api, srv
}

func (m *mediaAPI) called() (string, map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	form := make(map[string]string, len(m.form))
	maps.Copy(form, m.form)
	return m.method, form
}

func newTestMediaSender(t *testing.T, serverURL string, chatID int64, threadID int) gateway.MediaSender {
	t.Helper()
	b, err := bot.New("1:test", bot.WithServerURL(serverURL), bot.WithSkipGetMe())
	if err != nil {
		t.Fatalf("new test bot: %v", err)
	}
	g := New("1:test", "", "", []int64{42}, slog.New(slog.DiscardHandler))
	return g.NewMediaSender(b, chatID, threadID)
}

// Each media kind must reach its own Bot API method  --  a video sent via
// sendPhoto is rejected by Telegram, and a document fallback would silently
// downgrade playback.
func TestSendMedia_RoutesByMediaType(t *testing.T) {
	tests := []struct {
		name       string
		mediaType  string
		msgType    gateway.MessageType
		wantMethod string
		wantField  string
	}{
		{"video", "video", gateway.MsgVideo, "sendVideo", "video"},
		{"image", "image", gateway.MsgImage, "sendPhoto", "photo"},
		{"audio", "audio", gateway.MsgAudio, "sendAudio", "audio"},
		{"document", "document", gateway.MsgDocument, "sendDocument", "document"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, srv := newMediaAPI()
			defer srv.Close()

			sender := newTestMediaSender(t, srv.URL, 555, 0)
			res, err := sender.SendMedia(context.Background(), gateway.MessageEvent{
				Type: tt.msgType,
				Text: "a caption",
				Media: []gateway.MediaAttachment{{
					Type: tt.mediaType,
					URL:  "https://example.com/asset",
				}},
			})
			if err != nil {
				t.Fatalf("SendMedia: %v", err)
			}
			if !res.Success {
				t.Fatalf("expected success, got %+v", res)
			}

			method, form := api.called()
			if method != tt.wantMethod {
				t.Errorf("called %q, want %q", method, tt.wantMethod)
			}
			if form[tt.wantField] != "https://example.com/asset" {
				t.Errorf("field %q = %q, want the asset URL", tt.wantField, form[tt.wantField])
			}
			if form["caption"] != "a caption" {
				t.Errorf("caption = %q, want %q", form["caption"], "a caption")
			}
			if form["chat_id"] != "555" {
				t.Errorf("chat_id = %q, want the bound chat", form["chat_id"])
			}
		})
	}
}

// The sender is bound to a chat at construction, matching telegramApprover.
// A thread ID must survive to the API or replies land outside the topic.
func TestSendMedia_UsesBoundThread(t *testing.T) {
	api, srv := newMediaAPI()
	defer srv.Close()

	sender := newTestMediaSender(t, srv.URL, 555, 99)
	if _, err := sender.SendMedia(context.Background(), gateway.MessageEvent{
		Type:  gateway.MsgVideo,
		Media: []gateway.MediaAttachment{{Type: "video", URL: "https://example.com/v.mp4"}},
	}); err != nil {
		t.Fatalf("SendMedia: %v", err)
	}

	_, form := api.called()
	if form["message_thread_id"] != "99" {
		t.Errorf("message_thread_id = %q, want 99", form["message_thread_id"])
	}
}

// An event with no attachment must not reach the API at all: Telegram
// rejects an empty media field, and reporting success would leave the
// caller believing a video was delivered.
func TestSendMedia_NoAttachmentIsAnError(t *testing.T) {
	api, srv := newMediaAPI()
	defer srv.Close()

	sender := newTestMediaSender(t, srv.URL, 555, 0)
	res, err := sender.SendMedia(context.Background(), gateway.MessageEvent{Type: gateway.MsgVideo})
	if err == nil {
		t.Fatal("expected an error for an event carrying no media")
	}
	if res.Success {
		t.Error("expected Success=false")
	}
	if res.Retryable {
		t.Error("a missing attachment is not retryable")
	}
	if res.ErrorCode != "invalid_message" {
		t.Errorf("ErrorCode = %q, want invalid_message", res.ErrorCode)
	}
	if method, _ := api.called(); method != "" {
		t.Errorf("called the API (%q) despite having nothing to send", method)
	}
}

// An attachment with no URL cannot be sent: this sender is URL-only, and
// a FileID belongs to the platform it was uploaded from.
func TestSendMedia_MissingURLIsAnError(t *testing.T) {
	_, srv := newMediaAPI()
	defer srv.Close()

	sender := newTestMediaSender(t, srv.URL, 555, 0)
	res, err := sender.SendMedia(context.Background(), gateway.MessageEvent{
		Type:  gateway.MsgVideo,
		Media: []gateway.MediaAttachment{{Type: "video"}},
	})
	if err == nil {
		t.Fatal("expected an error for an attachment with no URL")
	}
	if res.ErrorCode != "invalid_message" {
		t.Errorf("ErrorCode = %q, want invalid_message", res.ErrorCode)
	}
}

// An unroutable media type must be reported, not guessed at.
func TestSendMedia_UnknownTypeIsAnError(t *testing.T) {
	_, srv := newMediaAPI()
	defer srv.Close()

	sender := newTestMediaSender(t, srv.URL, 555, 0)
	res, err := sender.SendMedia(context.Background(), gateway.MessageEvent{
		Type:  gateway.MsgVideo,
		Media: []gateway.MediaAttachment{{Type: "hologram", URL: "https://example.com/h"}},
	})
	if err == nil {
		t.Fatal("expected an error for an unsupported media type")
	}
	if res.ErrorCode != "invalid_message" {
		t.Errorf("ErrorCode = %q, want invalid_message", res.ErrorCode)
	}
}

// Telegram failures must be classified so callers can decide on retry,
// rather than every failure looking alike.
//
// The bot library branches on the JSON \`error_code\` field of the API
// response, not the HTTP status, so each stub has to carry the matching
// integer.
func TestSendMedia_ClassifiesAPIFailures(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		wantCode      string
		wantRetryable bool
	}{
		{"rate limited", `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":1}}`, "rate_limited", true},
		{"auth", `{"ok":false,"error_code":401,"description":"Unauthorized"}`, "auth", false},
		{"bad request", `{"ok":false,"error_code":400,"description":"wrong file identifier"}`, "invalid_message", false},
		{"server error", `{"ok":false,"error_code":500,"description":"boom"}`, "network", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, srv := newMediaAPI()
			defer srv.Close()
			api.mu.Lock()
			api.body = tt.body
			api.mu.Unlock()

			sender := newTestMediaSender(t, srv.URL, 555, 0)
			res, err := sender.SendMedia(context.Background(), gateway.MessageEvent{
				Type:  gateway.MsgVideo,
				Media: []gateway.MediaAttachment{{Type: "video", URL: "https://example.com/v.mp4"}},
			})
			if err == nil {
				t.Fatal("expected an error")
			}
			if res.Success {
				t.Error("expected Success=false")
			}
			if res.ErrorCode != tt.wantCode {
				t.Errorf("ErrorCode = %q, want %q", res.ErrorCode, tt.wantCode)
			}
			if res.Retryable != tt.wantRetryable {
				t.Errorf("Retryable = %v, want %v", res.Retryable, tt.wantRetryable)
			}
		})
	}
}

// The capability must be reported, so a caller can check support without
// attempting a doomed send.
func TestMediaSender_ReportsMediaCapability(t *testing.T) {
	_, srv := newMediaAPI()
	defer srv.Close()

	sender := newTestMediaSender(t, srv.URL, 555, 0)
	if caps := gateway.CapabilitiesOf(sender); !caps.Media {
		t.Error("telegram media sender does not report the Media capability")
	}
}
