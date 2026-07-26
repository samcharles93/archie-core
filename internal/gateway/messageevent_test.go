package gateway

import (
	"encoding/json"
	"testing"
)

func intPtr(v int) *int     { return &v }
func i64Ptr(v int64) *int64 { return &v }

func TestMessageEventToLegacy(t *testing.T) {
	e := MessageEvent{
		Type:      MsgText,
		Text:      "hello world",
		ChannelID: "chat-42",
		Platform:  "telegram",
		SenderID:  "alice",
	}
	m := e.ToLegacy()
	if m.ChannelID != "chat-42" {
		t.Errorf("ChannelID = %q", m.ChannelID)
	}
	if m.From != "alice" {
		t.Errorf("From = %q", m.From)
	}
	if m.Text != "hello world" {
		t.Errorf("Text = %q", m.Text)
	}

	// ToLegacy carries Text for non-text types too  --  callers decide
	// how to interpret it.
	img := MessageEvent{
		Type:      MsgImage,
		Text:      "photo caption",
		ChannelID: "c1",
		Platform:  "tg",
		SenderID:  "u1",
	}
	leg := img.ToLegacy()
	if leg.Text != "photo caption" {
		t.Errorf("image caption = %q", leg.Text)
	}

	cb := MessageEvent{
		Type:         MsgCallback,
		CallbackData: `{"k":"v"}`,
		ChannelID:    "c1",
		Platform:     "tg",
		SenderID:     "u1",
	}
	if cb.ToLegacy().Text != "" {
		t.Errorf("callback legacy Text = %q, want empty", cb.ToLegacy().Text)
	}
}

func TestMessageEventFromID(t *testing.T) {
	e := MessageEvent{SenderID: "bob"}
	if e.FromID() != "bob" {
		t.Errorf("FromID = %q", e.FromID())
	}
}

func TestMessageEventMinimalText(t *testing.T) {
	e := MessageEvent{
		Type:      MsgText,
		Text:      "hi",
		ChannelID: "c1",
		Platform:  "web",
		SenderID:  "u1",
	}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var decoded MessageEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Type != MsgText {
		t.Errorf("Type = %q", decoded.Type)
	}
	if decoded.Text != "hi" {
		t.Errorf("Text = %q", decoded.Text)
	}
	if decoded.Type.IsEphemeral() {
		t.Error("text should not be ephemeral")
	}
}

func TestMessageEventMediaAttachment(t *testing.T) {
	e := MessageEvent{
		Type:      MsgImage,
		Text:      "look at this photo",
		ChannelID: "c1",
		Platform:  "telegram",
		SenderID:  "u1",
		ID:        "msg-1",
		Media: []MediaAttachment{
			{
				Type:     "image",
				FileID:   "file-abc",
				MIMEType: "image/png",
				FileSize: i64Ptr(102400),
				Width:    intPtr(800),
				Height:   intPtr(600),
			},
		},
	}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var decoded MessageEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Media) != 1 {
		t.Fatalf("expected 1 media, got %d", len(decoded.Media))
	}
	m := decoded.Media[0]
	if m.FileID != "file-abc" {
		t.Errorf("FileID = %q", m.FileID)
	}
	if m.Width == nil || *m.Width != 800 {
		t.Errorf("Width = %d", m.Width)
	}
}

func TestMessageEventReplyContext(t *testing.T) {
	e := MessageEvent{
		Type:      MsgText,
		Text:      "yes",
		ChannelID: "c1",
		Platform:  "discord",
		SenderID:  "u2",
		ReplyToID: "msg-99",
		ThreadID:  "thread-1",
	}
	if e.ReplyToID != "msg-99" {
		t.Errorf("ReplyToID = %q", e.ReplyToID)
	}
	if e.ThreadID != "thread-1" {
		t.Errorf("ThreadID = %q", e.ThreadID)
	}
}

func TestMessageEventReaction(t *testing.T) {
	e := MessageEvent{
		Type:        MsgReaction,
		ChannelID:   "c1",
		Platform:    "telegram",
		SenderID:    "u1",
		Reaction:    "👍",
		ReactedToID: "msg-5",
	}
	if !e.Type.IsEphemeral() {
		t.Error("reaction should be ephemeral")
	}
	if e.Reaction != "👍" {
		t.Errorf("Reaction = %q", e.Reaction)
	}
}

func TestMessageEventCallback(t *testing.T) {
	e := MessageEvent{
		Type:         MsgCallback,
		ChannelID:    "c1",
		Platform:     "telegram",
		SenderID:     "u1",
		CallbackData: `{"action":"approve","id":42}`,
	}
	if !e.Type.IsRich() {
		t.Error("callback should be rich")
	}
}

func TestMessageEventEditDelete(t *testing.T) {
	e := MessageEvent{
		Type:       MsgEdited,
		ChannelID:  "c1",
		Platform:   "telegram",
		SenderID:   "u1",
		ID:         "msg-1",
		EditedFrom: "original text",
		Text:       "updated text",
	}
	if e.EditedFrom != "original text" {
		t.Errorf("EditedFrom = %q", e.EditedFrom)
	}
	if e.Text != "updated text" {
		t.Errorf("Text = %q", e.Text)
	}

	del := MessageEvent{
		Type:      MsgDeleted,
		ChannelID: "c1",
		Platform:  "telegram",
		SenderID:  "u1",
		ID:        "msg-1",
	}
	if !del.Type.IsEphemeral() {
		t.Error("deleted should be ephemeral")
	}
}

func TestMessageEventTimestamp(t *testing.T) {
	e := MessageEvent{
		Type:      MsgText,
		ChannelID: "c1",
		Platform:  "telegram",
		SenderID:  "u1",
		Timestamp: 1234567890000,
	}
	if e.Timestamp != 1234567890000 {
		t.Errorf("Timestamp = %d", e.Timestamp)
	}
}

func TestMessageEventRawPayload(t *testing.T) {
	e := MessageEvent{
		Type:      MsgSystem,
		ChannelID: "c1",
		Platform:  "telegram",
		SenderID:  "",
		Raw: map[string]any{
			"action": "member_joined",
			"user":   "newguy",
		},
	}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var decoded MessageEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Raw["action"] != "member_joined" {
		t.Errorf("Raw[action] = %v", decoded.Raw["action"])
	}
}

func TestMessageEventMediaAttachmentZeroValue(t *testing.T) {
	var m MediaAttachment
	if m.FileID != "" {
		t.Error("zero MediaAttachment should have empty FileID")
	}
	if m.Type != "" {
		t.Error("zero MediaAttachment should have empty Type")
	}
}

func TestMessageEventToLegacyThreadID(t *testing.T) {
	// ThreadID must round-trip through ToLegacy for thread-aware routing.
	e := MessageEvent{
		Type:      MsgText,
		Text:      "message in a thread",
		ChannelID: "-100123",
		ThreadID:  "5",
		Platform:  "telegram",
		SenderID:  "user1",
	}
	m := e.ToLegacy()
	if m.ThreadID != "5" {
		t.Errorf("legacy ThreadID = %q, want 5", m.ThreadID)
	}
	if m.ChannelID != "-100123" {
		t.Errorf("legacy ChannelID = %q", m.ChannelID)
	}

	// Empty ThreadID preserves emptiness.
	e2 := MessageEvent{
		Type:      MsgText,
		Text:      "flat chat message",
		ChannelID: "-100456",
		Platform:  "telegram",
		SenderID:  "user2",
	}
	m2 := e2.ToLegacy()
	if m2.ThreadID != "" {
		t.Errorf("expected empty ThreadID, got %q", m2.ThreadID)
	}
}

func TestMessageEventDocExample(t *testing.T) {
	// Exercise: full photo message as an adapter would construct it.
	e := MessageEvent{
		Type:       MsgImage,
		Text:       "check out this screenshot",
		ID:         "1734567890123",
		ChannelID:  "-1001234567890",
		Platform:   "telegram",
		SenderID:   "987654321",
		SenderName: "Alice",
		ReplyToID:  "1734567890000",
		Media: []MediaAttachment{{
			Type:     "image",
			FileID:   "AgACAgIAAxkBAA...",
			MIMEType: "image/png",
			FileSize: i64Ptr(245760),
			Width:    intPtr(1920),
			Height:   intPtr(1080),
		}},
		Timestamp: 1734567890123,
	}

	if e.Type != MsgImage {
		t.Error("expected image type")
	}
	if !e.Type.IsMedia() {
		t.Error("image should be media")
	}
	if e.Type.IsEphemeral() {
		t.Error("image should not be ephemeral")
	}
	if e.SenderName != "Alice" {
		t.Errorf("SenderName = %q", e.SenderName)
	}

	// Backward compat.
	legacy := e.ToLegacy()
	if legacy.From != "987654321" {
		t.Errorf("legacy From = %q", legacy.From)
	}
	if legacy.Text != "check out this screenshot" {
		t.Errorf("legacy Text = %q", legacy.Text)
	}
}
