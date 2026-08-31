package messaging

import "testing"

func TestConversationIsBranch(t *testing.T) {
	tests := []struct {
		name string
		conv Conversation
		want bool
	}{
		{
			name: "root conversation",
			conv: Conversation{ID: ConversationID{ChannelID: "chan-1"}},
			want: false,
		},
		{
			name: "forked conversation",
			conv: Conversation{
				ID:                   ConversationID{ChannelID: "chan-1", ThreadID: "fork-1"},
				ParentConversationID: ConversationID{ChannelID: "chan-1"},
				ForkMessageID:        "msg-42",
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.conv.IsBranch(); got != tt.want {
				t.Fatalf("IsBranch() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestConversationIDDistinguishesThreads(t *testing.T) {
	a := ConversationID{ChannelID: "chan-1", ThreadID: "t1"}
	b := ConversationID{ChannelID: "chan-1", ThreadID: "t2"}
	if a == b {
		t.Fatal("distinct ThreadIDs must produce distinct ConversationIDs")
	}
}
