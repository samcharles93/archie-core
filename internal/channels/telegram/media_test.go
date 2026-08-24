package telegram

import (
	"context"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	// files records the multipart file parts an upload sends, by form
	// field name. A fetch-by-URL send has none; an upload must have one,
	// which is the difference these tests exist to prove.
	files map[string]uploadedFile
	body  string
}

// uploadedFile is one multipart file part as the Bot API received it.
type uploadedFile struct {
	name    string
	content string
}

func newMediaAPI() (*mediaAPI, *httptest.Server) {
	api := &mediaAPI{form: map[string]string{}, files: map[string]uploadedFile{}, body: `{"ok":true,"result":{"message_id":77}}`}
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
			recordFileParts(api.files, r)
		}
		body := api.body
		api.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	return api, srv
}

// recordFileParts copies the request's multipart file parts into files,
// keyed by form field. A fetch-by-URL send contributes none.
func recordFileParts(files map[string]uploadedFile, r *http.Request) {
	if r.MultipartForm == nil {
		return
	}
	for field, headers := range r.MultipartForm.File {
		if len(headers) == 0 {
			continue
		}
		f, err := headers[0].Open()
		if err != nil {
			continue
		}
		content, _ := io.ReadAll(f)
		_ = f.Close()
		files[field] = uploadedFile{name: headers[0].Filename, content: string(content)}
	}
}

// uploaded returns the file part sent under field, if any.
func (m *mediaAPI) uploaded(field string) (uploadedFile, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.files[field]
	return f, ok
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

// An attachment with neither a URL nor a local path names nothing to
// send. A FileID does not count: it belongs to the platform it was
// uploaded from.
func TestSendMedia_MissingSourceIsAnError(t *testing.T) {
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

// writeTempFile creates a file of the given content and returns its path.
func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// A local file must be UPLOADED, not handed to Telegram as a URL to
// fetch. This is the defect the Path field exists for: a host path in the
// URL field is not fetchable, so the send did nothing while reporting
// success.
func TestSendMedia_UploadsLocalPath(t *testing.T) {
	tests := []struct {
		name      string
		mediaType string
		file      string
		method    string
		field     string
	}{
		{"document", "document", "transcript.md", "sendDocument", "document"},
		{"image", "image", "shot.png", "sendPhoto", "photo"},
		{"video", "video", "clip.mp4", "sendVideo", "video"},
		{"audio", "audio", "note.mp3", "sendAudio", "audio"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, srv := newMediaAPI()
			defer srv.Close()
			path := writeTempFile(t, tt.file, "file bytes")

			sender := newTestMediaSender(t, srv.URL, 555, 0)
			res, err := sender.SendMedia(context.Background(), gateway.MessageEvent{
				Text:  "a caption",
				Media: []gateway.MediaAttachment{{Type: tt.mediaType, Path: path}},
			})
			if err != nil {
				t.Fatalf("SendMedia: %v", err)
			}
			if !res.Success {
				t.Fatalf("expected success, got %+v", res)
			}

			method, form := api.called()
			if method != tt.method {
				t.Errorf("called %q, want %q", method, tt.method)
			}
			file, ok := api.uploaded(tt.field)
			if !ok {
				t.Fatalf("no file part under %q; form = %v (the path was sent as a URL to fetch, not uploaded)", tt.field, form)
			}
			if file.content != "file bytes" {
				t.Errorf("uploaded content = %q, want the file's bytes", file.content)
			}
			if file.name != tt.file {
				t.Errorf("uploaded filename = %q, want %q", file.name, tt.file)
			}
			if form["caption"] != "a caption" {
				t.Errorf("caption = %q, want it preserved on an upload", form["caption"])
			}
		})
	}
}

// FileName overrides the on-disk basename, so a file written to a
// generated temp name still arrives under a name that means something.
func TestSendMedia_UploadUsesAttachmentFileName(t *testing.T) {
	api, srv := newMediaAPI()
	defer srv.Close()
	path := writeTempFile(t, "tmp-8342.bin", "x")

	sender := newTestMediaSender(t, srv.URL, 555, 0)
	if _, err := sender.SendMedia(context.Background(), gateway.MessageEvent{
		Media: []gateway.MediaAttachment{{Type: "document", Path: path, FileName: "session.log"}},
	}); err != nil {
		t.Fatalf("SendMedia: %v", err)
	}
	file, ok := api.uploaded("document")
	if !ok {
		t.Fatal("no document file part")
	}
	if file.name != "session.log" {
		t.Errorf("uploaded filename = %q, want session.log", file.name)
	}
}

// A file that cannot be uploaded must fail before any request, with an
// error naming the reason. Silent non-delivery is the defect; a 400 whose
// text nobody reads is the same defect wearing a status code.
func TestSendMedia_LocalPathFailures(t *testing.T) {
	oversize := writeTempFile(t, "big.bin", "")
	if err := os.Truncate(oversize, maxFileUploadBytes+1); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	overPhoto := writeTempFile(t, "big.png", "")
	if err := os.Truncate(overPhoto, maxPhotoUploadBytes+1); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	tests := []struct {
		name      string
		mediaType string
		path      string
		wantSub   string
	}{
		{"missing file", "document", filepath.Join(t.TempDir(), "gone.txt"), "gone.txt"},
		{"directory", "document", t.TempDir(), "not a regular file"},
		{"over the document limit", "document", oversize, "exceeds"},
		{"over the photo limit", "image", overPhoto, "exceeds"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, srv := newMediaAPI()
			defer srv.Close()

			sender := newTestMediaSender(t, srv.URL, 555, 0)
			res, err := sender.SendMedia(context.Background(), gateway.MessageEvent{
				Media: []gateway.MediaAttachment{{Type: tt.mediaType, Path: tt.path}},
			})
			if err == nil {
				t.Fatal("expected an error")
			}
			if res.Success {
				t.Errorf("Success = true for an undeliverable file: %+v", res)
			}
			if res.Retryable {
				t.Error("Retryable = true; none of these get better on a retry")
			}
			if res.ErrorCode != "invalid_message" {
				t.Errorf("ErrorCode = %q, want invalid_message", res.ErrorCode)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("err = %v, want it to mention %q", err, tt.wantSub)
			}
			if method, _ := api.called(); method != "" {
				t.Errorf("called the API (%q) despite an undeliverable file", method)
			}
		})
	}
}

// A photo just under the photo ceiling must still be accepted -- the
// per-type limits must not collapse into one conservative number.
func TestSendMedia_PhotoUnderLimitIsSent(t *testing.T) {
	api, srv := newMediaAPI()
	defer srv.Close()
	path := writeTempFile(t, "ok.png", "small")

	sender := newTestMediaSender(t, srv.URL, 555, 0)
	if _, err := sender.SendMedia(context.Background(), gateway.MessageEvent{
		Media: []gateway.MediaAttachment{{Type: "image", Path: path}},
	}); err != nil {
		t.Fatalf("SendMedia: %v", err)
	}
	if method, _ := api.called(); method != "sendPhoto" {
		t.Errorf("called %q, want sendPhoto", method)
	}
}
