package telegram

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// recordedAPI captures every Bot API request so a test can assert a reply
// was sent (or, equally, that none was).
type recordedAPI struct {
	mu    sync.Mutex
	calls []string // "path body" lines, one per request
}

func newRecordedAPI(t *testing.T, fileRoute func(w http.ResponseWriter, r *http.Request) bool) (*bot.Bot, *recordedAPI) {
	t.Helper()
	rec := &recordedAPI{}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fileRoute != nil && fileRoute(w, r) {
			return
		}
		body := readBody(r)
		rec.mu.Lock()
		rec.calls = append(rec.calls, r.URL.Path+" "+body)
		rec.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "getFile") {
			_, _ = w.Write([]byte(`{"ok":true,"result":{"file_path":"photos/pic.jpg"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	t.Cleanup(api.Close)

	b, err := bot.New("1:test", bot.WithServerURL(api.URL), bot.WithSkipGetMe())
	if err != nil {
		t.Fatalf("new test bot: %v", err)
	}
	return b, rec
}

func readBody(r *http.Request) string {
	_ = r.ParseMultipartForm(1 << 20)
	if r.PostForm != nil {
		return r.PostForm.Encode()
	}
	return r.URL.RawQuery
}

func (rec *recordedAPI) contains(fragment string) bool {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, call := range rec.calls {
		if strings.Contains(strings.ToLower(call), strings.ToLower(fragment)) {
			return true
		}
	}
	return false
}

func (rec *recordedAPI) callCount() int {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return len(rec.calls)
}

func testMediaGateway(b *bot.Bot) *Gateway {
	g := New("1:test", []int64{42}, slog.New(slog.DiscardHandler))
	g.bot = b
	g.turns = messaging.NewTurns(g.log)
	return g
}

// TestExtractInboundMediaMapsTelegramKinds walks every supported message
// kind. A message's media decides the file the bot must download, so a
// mapping error sends the wrong file or sends nothing at all.
func TestExtractInboundMediaMapsTelegramKinds(t *testing.T) {
	cases := []struct {
		name        string
		msg         *models.Message
		wantType    string
		wantFileID  string
		wantMIME    string
		wantName    string
		wantNote    string
		wantNoMedia bool
	}{
		{
			name: "photo picks the largest size",
			msg: &models.Message{
				Photo: []models.PhotoSize{
					{FileID: "small", Width: 90, Height: 80},
					{FileID: "big", Width: 1280, Height: 960, FileSize: 150000},
				},
			},
			wantType:   "image",
			wantFileID: "big",
			wantMIME:   "image/jpeg",
			wantNote:   "[photo]",
		},
		{
			name: "document",
			msg: &models.Message{
				Document: &models.Document{
					FileID: "doc1", FileName: "report.pdf", MimeType: "application/pdf", FileSize: 2048,
				},
			},
			wantType:   "document",
			wantFileID: "doc1",
			wantMIME:   "application/pdf",
			wantName:   "report.pdf",
			wantNote:   "[document: report.pdf]",
		},
		{
			name: "video",
			msg: &models.Message{
				Video: &models.Video{FileID: "vid1", Duration: 12, MimeType: "video/mp4"},
			},
			wantType:   "video",
			wantFileID: "vid1",
			wantMIME:   "video/mp4",
			wantNote:   "[video]",
		},
		{
			name: "video note",
			msg: &models.Message{
				VideoNote: &models.VideoNote{FileID: "vnote1", Duration: 30},
			},
			wantType:   "video",
			wantFileID: "vnote1",
			wantMIME:   "video/mp4",
			wantNote:   "[video message]",
		},
		{
			name: "audio",
			msg: &models.Message{
				Audio: &models.Audio{FileID: "aud1", FileName: "song.mp3", MimeType: "audio/mpeg", Duration: 200},
			},
			wantType:   "audio",
			wantFileID: "aud1",
			wantMIME:   "audio/mpeg",
			wantName:   "song.mp3",
			wantNote:   "[audio]",
		},
		{
			name: "voice",
			msg: &models.Message{
				Voice: &models.Voice{FileID: "voice1", MimeType: "audio/ogg", Duration: 8},
			},
			wantType:   "audio",
			wantFileID: "voice1",
			wantMIME:   "audio/ogg",
			wantNote:   "[voice message]",
		},
		{
			name: "plain text message carries no media",
			msg:  &models.Message{Text: "hello"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := extractInboundMedia(tc.msg)
			if tc.wantType == "" {
				if ok {
					t.Fatalf("extractInboundMedia(%s) = ok, want none", tc.name)
				}
				return
			}
			if !ok {
				t.Fatalf("extractInboundMedia(%s) missed, want %s", tc.name, tc.wantType)
			}
			att := got.attachment
			if att.Type != tc.wantType {
				t.Errorf("Type = %q, want %q", att.Type, tc.wantType)
			}
			if att.FileID != tc.wantFileID {
				t.Errorf("FileID = %q, want %q", att.FileID, tc.wantFileID)
			}
			if att.MIMEType != tc.wantMIME {
				t.Errorf("MIMEType = %q, want %q", att.MIMEType, tc.wantMIME)
			}
			if att.FileName != tc.wantName {
				t.Errorf("FileName = %q, want %q", att.FileName, tc.wantName)
			}
			if got.note != tc.wantNote {
				t.Errorf("note = %q, want %q", got.note, tc.wantNote)
			}
		})
	}
}

// TestTurnMessageTextKeepsCaptionWithPlaceholder: history is text-only, so
// the stored turn text must name the attachment and keep any caption.
func TestTurnMessageTextKeepsCaptionWithPlaceholder(t *testing.T) {
	media, ok := extractInboundMedia(&models.Message{
		Photo:   []models.PhotoSize{{FileID: "a", Width: 10, Height: 10}},
		Caption: "what is this?",
	})
	if !ok {
		t.Fatal("media missed")
	}
	got := turnMessageText(&models.Message{Text: "", Caption: "what is this?"}, media, ok)
	want := "[photo]\nwhat is this?"
	if got != want {
		t.Errorf("turnMessageText = %q, want %q", got, want)
	}
}

// TestDefaultHandlerSubmitsPhotoTurnWithDownloadedMedia drives the one
// behaviour at the heart of the gap: a photo update reaches the chat turn
// with the file's bytes attached, not as a dropped message.
func TestDefaultHandlerSubmitsPhotoTurnWithDownloadedMedia(t *testing.T) {
	photoBytes := []byte("jpeg-bytes")
	var fileServed bool
	b, rec := newRecordedAPI(t, func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasPrefix(r.URL.Path, "/file/bot1:test/") {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(photoBytes)
			fileServed = true
			return true
		}
		return false
	})

	inboundCh := make(chan messaging.Inbound, 1)
	client := &fakeChatContract{
		streamFunc: func(_ context.Context, in messaging.Inbound) (<-chan messaging.ChatEvent, error) {
			inboundCh <- in
			ch := make(chan messaging.ChatEvent, 2)
			ch <- messaging.ChatEvent{Kind: "started", SessionID: "s1"}
			ch <- messaging.ChatEvent{Kind: "done", Text: "done", SessionID: "s1"}
			close(ch)
			return ch, nil
		},
	}
	g := testMediaGateway(b)

	handler := g.defaultHandler(client)
	handler(context.Background(), b, &models.Update{Message: &models.Message{
		ID:      5,
		Date:    1,
		From:    &models.User{ID: 42, Username: "sam"},
		Chat:    models.Chat{ID: 7, Type: "private"},
		Photo:   []models.PhotoSize{{FileID: "big", Width: 1280, Height: 960, FileSize: 150000}},
		Caption: "look at this",
	}})

	select {
	case in := <-inboundCh:
		if len(in.Media) != 1 {
			t.Fatalf("inbound media = %#v, want one attachment", in.Media)
		}
		att := in.Media[0]
		if att.Type != "image" || att.MIMEType != "image/jpeg" || att.FileID != "big" {
			t.Errorf("attachment = %#v, want image with the photo's file id", att)
		}
		if string(att.Data) != string(photoBytes) {
			t.Errorf("Data = %q, want the downloaded photo bytes", att.Data)
		}
		want := "[photo]\nlook at this"
		if in.Message.Text != want {
			t.Errorf("Text = %q, want %q", in.Message.Text, want)
		}
		if in.Message.SourceID != "5" {
			t.Errorf("SourceID = %q, want the Telegram message id", in.Message.SourceID)
		}
		if !fileServed {
			t.Error("download never fetched the file bytes")
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("photo update never reached the chat turn; api calls were:\n%s",
			strings.Join(rec.calls, "\n"))
	}
}

// TestDefaultHandlerRepliesToUnsupportedMedia keeps the gap from returning
// silently in the other direction: content types Archie cannot read get a
// clear notice rather than the void.
func TestDefaultHandlerRepliesToUnsupportedMedia(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*models.Message)
		reply string
	}{
		{"sticker", func(m *models.Message) { m.Sticker = &models.Sticker{FileID: "s"} }, "sticker"},
		{"location", func(m *models.Message) { m.Location = &models.Location{} }, "location"},
		{"contact", func(m *models.Message) { m.Contact = &models.Contact{} }, "contact"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, rec := newRecordedAPI(t, nil)
			g := testMediaGateway(b)
			msg := &models.Message{
				ID:   6,
				Date: 1,
				From: &models.User{ID: 42, Username: "sam"},
				Chat: models.Chat{ID: 7, Type: "private"},
			}
			tc.setup(msg)
			g.defaultHandler(&fakeChatContract{})(context.Background(), b, &models.Update{Message: msg})
			if !rec.contains(tc.reply) {
				t.Errorf("no reply mentioning %q was sent; calls:\n%s", tc.reply, strings.Join(rec.calls, "\n"))
			}
		})
	}
}

// TestDefaultHandlerSilentForNonContentMessages guards the other update
// kinds a group can carry: no text, no media, no notice -- and so no reply.
func TestDefaultHandlerSilentForNonContentMessages(t *testing.T) {
	b, rec := newRecordedAPI(t, nil)
	g := testMediaGateway(b)
	g.defaultHandler(&fakeChatContract{})(context.Background(), b, &models.Update{Message: &models.Message{
		ID:   7,
		Date: 1,
		From: &models.User{ID: 42},
		Chat: models.Chat{ID: 7, Type: "private"},
	}})
	if n := rec.callCount(); n != 0 {
		t.Errorf("%d API calls for a message with no text, media or notice, want 0", n)
	}
}

// TestDownloadMediaCoversSuccessAndLimits exercises the download the turn
// relies on: the Bot API getFile lookup, the file fetch, and the size gate.
func TestDownloadMediaCoversSuccessAndLimits(t *testing.T) {
	fileBytes := []byte("file-bytes")
	b, rec := newRecordedAPI(t, func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasPrefix(r.URL.Path, "/file/bot1:test/") {
			_, _ = w.Write(fileBytes)
			return true
		}
		return false
	})
	g := testMediaGateway(b)

	t.Run("success", func(t *testing.T) {
		data, err := g.downloadMedia(context.Background(), b,
			messaging.MediaAttachment{Type: "image", FileID: "f1", MIMEType: "image/jpeg"})
		if err != nil {
			t.Fatalf("downloadMedia() error = %v", err)
		}
		if string(data) != string(fileBytes) {
			t.Errorf("downloaded %q, want %q", data, fileBytes)
		}
		if !rec.contains("getfile") {
			t.Error("getFile never resolved the file path")
		}
	})

	t.Run("oversized declared size is refused before download", func(t *testing.T) {
		old := maxInboundDownloadBytes
		maxInboundDownloadBytes = 4
		t.Cleanup(func() { maxInboundDownloadBytes = old })
		size := int64(10)
		if _, err := g.downloadMedia(context.Background(), b,
			messaging.MediaAttachment{Type: "document", FileID: "f2", FileSize: &size}); err == nil {
			t.Error("oversized declared size accepted")
		}
	})

	t.Run("oversized response body is refused", func(t *testing.T) {
		old := maxInboundDownloadBytes
		maxInboundDownloadBytes = 4
		t.Cleanup(func() { maxInboundDownloadBytes = old })
		if _, err := g.downloadMedia(context.Background(), b,
			messaging.MediaAttachment{Type: "document", FileID: "f3"}); err == nil {
			t.Error("response larger than the limit accepted")
		}
	})
}
