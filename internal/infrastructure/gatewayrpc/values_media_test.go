package gatewayrpc

import (
	"bytes"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// TestStoredMediaRoundTrip: a message's attachment metadata must survive
// the wire both ways, while the in-process bytes -- turn-scoped by
// contract -- must never be marshalled onto it.
func TestStoredMediaRoundTrip(t *testing.T) {
	size, width := int64(2048), 640
	in := messaging.Message{
		ID:   messaging.MessageID("m1"),
		Role: messaging.RoleUser, Text: "[document: notes.txt]",
		Media: []messaging.MediaAttachment{{
			Type: "document", FileID: "tg-f1", URL: "https://example.invalid/notes",
			Path: "/tmp/never-here", MIMEType: "text/plain", FileName: "notes.txt",
			FileSize: &size, Width: &width, Data: []byte("file bytes"),
		}},
	}
	out := storedValue(storedProto(in))
	if len(out.Media) != 1 {
		t.Fatalf("round-tripped media = %#v, want one attachment", out.Media)
	}
	att := out.Media[0]
	if att.Type != "document" || att.FileName != "notes.txt" || att.MIMEType != "text/plain" {
		t.Errorf("attachment = %#v, want its metadata preserved", att)
	}
	if att.FileID != "tg-f1" {
		t.Errorf("FileID = %q, want tg-f1", att.FileID)
	}
	if att.FileSize == nil || *att.FileSize != size || att.Width == nil || *att.Width != width {
		t.Errorf("numeric metadata = %#v, want size and width preserved", att)
	}
	if att.URL != "https://example.invalid/notes" {
		t.Errorf("URL = %q, want preserved", att.URL)
	}
	if len(att.Data) != 0 {
		t.Errorf("attachment bytes crossed the wire, want them stripped")
	}
}

// TestInboundMediaRoundTrip mirrors the stored direction for the inbound
// transport context, except that the bytes ride along. The channel frontend
// (archie-messaging) downloads an inbound attachment, but the process that
// runs the turn is archie-gateway/archied, which never holds the Telegram
// token -- so the bytes have to cross this request or the model never sees
// the photo or document it was sent.
func TestInboundMediaRoundTrip(t *testing.T) {
	raw := []byte("jpeg bytes")
	in := messaging.Inbound{
		Message: messaging.Message{Text: "hi", Sender: "sam"},
		Media: []messaging.MediaAttachment{
			{Type: "image", FileID: "f1", MIMEType: "image/jpeg", Data: raw},
		},
	}
	out := inboundValue(inboundProto(in))
	if len(out.Media) != 1 || out.Media[0].FileID != "f1" || out.Media[0].Type != "image" {
		t.Fatalf("round-tripped inbound media = %#v, want the sender attachment", out.Media)
	}
	if !bytes.Equal(out.Media[0].Data, raw) {
		t.Errorf("inbound attachment bytes = %q, want %q carried to the turn", out.Media[0].Data, raw)
	}
}
