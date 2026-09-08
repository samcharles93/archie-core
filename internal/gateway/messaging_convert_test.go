package gateway

import (
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// TestStoredMessageRoundTrip pins the store boundary contract: converting a
// gateway message to its canonical stored form and back preserves every
// field the store persists today (identity, conversation address, source
// correlation, sender, role, text, timestamp). Page is transport-only and
// never enters the record. The gateway view back leaves ChannelID/ThreadID
// empty, exactly as scanMessages does today -- the conversation address
// lives on the stored record, repopulated from the session on read.
func TestStoredMessageRoundTrip(t *testing.T) {
	at := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		msg     Message
		botUser string
		want    messaging.Message
	}{
		{
			name:    "user message keeps sender and role",
			msg:     Message{MessageID: "m-1", SourceID: "tg-1", From: "alice", Text: "hello", At: at},
			botUser: "archie",
			want: messaging.Message{
				ID: messaging.MessageID("m-1"), SourceID: "tg-1",
				Role: messaging.RoleUser, Sender: "alice", Text: "hello", At: at,
			},
		},
		{
			name:    "assistant message derives role from bot identity",
			msg:     Message{MessageID: "m-2", From: "archie", Text: "hi", At: at},
			botUser: "archie",
			want: messaging.Message{
				ID:   messaging.MessageID("m-2"),
				Role: messaging.RoleAssistant, Sender: "archie", Text: "hi", At: at,
			},
		},
		{
			name:    "empty sender stays user role",
			msg:     Message{MessageID: "m-3", Text: "anon", At: at},
			botUser: "archie",
			want: messaging.Message{
				ID:   messaging.MessageID("m-3"),
				Role: messaging.RoleUser, Text: "anon", At: at,
			},
		},
		{
			name:    "channel and thread address the conversation; page does not persist",
			msg:     Message{MessageID: "m-4", ChannelID: "chat-1", ThreadID: "t-1", Page: "/tasks", From: "alice", Text: "x", At: at},
			botUser: "archie",
			want: messaging.Message{
				ID:             messaging.MessageID("m-4"),
				ConversationID: messaging.ConversationID{ChannelID: "chat-1", ThreadID: "t-1"},
				Role:           messaging.RoleUser, Sender: "alice", Text: "x", At: at,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToStoredMessage(tt.msg, tt.botUser)
			if got != tt.want {
				t.Fatalf("ToStoredMessage() = %+v, want %+v", got, tt.want)
			}
			back := FromStoredMessage(got)
			if back.MessageID != tt.msg.MessageID || back.SourceID != tt.msg.SourceID ||
				back.From != tt.msg.From || back.Text != tt.msg.Text || !back.At.Equal(tt.msg.At) {
				t.Fatalf("FromStoredMessage() = %+v, want stored fields of %+v", back, tt.msg)
			}
			if back.ChannelID != "" || back.ThreadID != "" || back.Page != "" {
				t.Fatalf("FromStoredMessage() carries transport fields: %+v", back)
			}
		})
	}
}
