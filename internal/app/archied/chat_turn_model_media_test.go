package archied

import (
	"testing"

	"github.com/samcharles93/ai-sdk/chat"

	"github.com/samcharles93/archie-core/internal/gateway"
)

// TestBuildTurnMessagesAttachesMediaPartsToTheFinalUserMessage: inbound
// media only reaches the model when the turn's request is turned into
// message parts; the attachment belongs on the message the user just sent.
func TestBuildTurnMessagesAttachesMediaPartsToTheFinalUserMessage(t *testing.T) {
	request := gateway.TurnModelRequest{
		Messages: []gateway.CompressedMessage{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "what is this?"},
		},
		Media: []gateway.MediaAttachment{
			{Type: "image", MIMEType: "image/png", Data: []byte("png-bytes")},
			{Type: "image", URL: "https://example.invalid/pic.jpg"},
			{Type: "document", MIMEType: "application/pdf", FileName: "report.pdf", Data: []byte("pdf-bytes")},
		},
	}

	messages := buildTurnMessages(request)
	if len(messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(messages))
	}
	if messages[0].Content != "sys" || len(messages[0].Parts) != 0 {
		t.Errorf("system message = %#v, want plain content and no parts", messages[0])
	}
	if messages[1].Content != "" {
		t.Errorf("user content = %q, want it moved into a text part", messages[1].Content)
	}
	parts := messages[1].Parts
	if len(parts) != 4 {
		t.Fatalf("user parts = %d, want text plus three media parts: %#v", len(parts), parts)
	}
	if text, ok := parts[0].(chat.TextPart); !ok || text.Text != "what is this?" {
		t.Errorf("part 0 = %#v, want the caption text", parts[0])
	}
	image, ok := parts[1].(chat.ImagePart)
	if !ok || string(image.Data) != "png-bytes" || image.MediaType != "image/png" {
		t.Errorf("part 1 = %#v, want an image part carrying the png bytes", parts[1])
	}
	linked, ok := parts[2].(chat.ImagePart)
	if !ok || linked.URL != "https://example.invalid/pic.jpg" {
		t.Errorf("part 2 = %#v, want an image part pointing at the URL", parts[2])
	}
	file, ok := parts[3].(chat.FilePart)
	if !ok {
		t.Fatalf("part 3 = %#v, want a file part", parts[3])
	}
	if string(file.Data) != "pdf-bytes" || file.MediaType != "application/pdf" || file.Name != "report.pdf" {
		t.Errorf("file part = %#v, want the pdf bytes with type and name", file)
	}
}

// TestBuildTurnMessagesSkipsUndeliverableMedia: an attachment carrying
// neither bytes nor a URL names nothing the model can consume, so it must
// be dropped rather than sent as an empty part.
func TestBuildTurnMessagesSkipsUndeliverableMedia(t *testing.T) {
	request := gateway.TurnModelRequest{
		Messages: []gateway.CompressedMessage{
			{Role: "user", Content: "look"},
		},
		Media: []gateway.MediaAttachment{
			{Type: "video"},
			{Type: "image", MIMEType: "image/png", Data: []byte("png")},
		},
	}
	messages := buildTurnMessages(request)
	fileSeen := false
	for _, p := range messages[0].Parts {
		if _, ok := p.(chat.FilePart); ok {
			fileSeen = true
		}
	}
	if fileSeen {
		t.Errorf("parts = %#v, want the empty attachment dropped", messages[0].Parts)
	}
	if len(messages[0].Parts) != 2 {
		t.Fatalf("user parts = %d, want text plus the one deliverable image", len(messages[0].Parts))
	}
	if text, ok := messages[0].Parts[0].(chat.TextPart); !ok || text.Text != "look" {
		t.Errorf("part 0 = %#v, want the text part", messages[0].Parts[0])
	}
}

// TestBuildTurnMessagesWithoutMediaKeepsPlainContent is the regression
// guard for the overwhelmingly common case: text-only turns stay exactly as
// they were, with Content and no parts.
func TestBuildTurnMessagesWithoutMediaKeepsPlainContent(t *testing.T) {
	request := gateway.TurnModelRequest{
		Messages: []gateway.CompressedMessage{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "plain"},
			{Role: "assistant", Content: "reply"},
		},
	}
	messages := buildTurnMessages(request)
	for i, want := range []struct {
		role    chat.Role
		content string
	}{
		{chat.RoleSystem, "sys"},
		{chat.RoleUser, "plain"},
		{chat.RoleAssistant, "reply"},
	} {
		if messages[i].Content != want.content || messages[i].Parts != nil {
			t.Errorf("message %d = %#v, want content %q and no parts", i, messages[i], want.content)
		}
	}
}
