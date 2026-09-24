package config

import "testing"

// TestChatConfigFrontEnds is the single definition of "a chat front-end is
// configured". Two surfaces read it -- the daemon's channel status manager and
// the configuration projection published for the extracted UI process -- so a
// front-end counted by one and missed by the other is a dashboard that
// contradicts itself (GitHub #821).
func TestChatConfigFrontEnds(t *testing.T) {
	tests := []struct {
		name string
		chat ChatConfig
		want map[string]bool
	}{
		{
			name: "nothing configured",
			chat: ChatConfig{},
			want: map[string]bool{"telegram": false, "email": false, "webhook": false},
		},
		{
			name: "telegram bot token through the secret engine",
			chat: ChatConfig{Telegram: TelegramConfig{Token: SecretRef{Engine: "builtin", Key: "telegram"}}},
			want: map[string]bool{"telegram": true, "email": false, "webhook": false},
		},
		{
			name: "inbound mail gateway",
			chat: ChatConfig{Email: EmailConfig{ListenAddr: "127.0.0.1:2525"}},
			want: map[string]bool{"telegram": false, "email": true, "webhook": false},
		},
		{
			name: "webhook gateway",
			chat: ChatConfig{WebhookAddr: "127.0.0.1:9099"},
			want: map[string]bool{"telegram": false, "email": false, "webhook": true},
		},
		{
			name: "every front-end at once",
			chat: ChatConfig{
				Telegram:    TelegramConfig{Token: SecretRef{Engine: "env", Key: "TELEGRAM_TOKEN"}},
				Email:       EmailConfig{ListenAddr: "127.0.0.1:2525"},
				WebhookAddr: "127.0.0.1:9099",
			},
			want: map[string]bool{"telegram": true, "email": true, "webhook": true},
		},
		{
			// Whitespace is not a value: " " cannot be listened on.
			name: "whitespace-only values are not configured",
			chat: ChatConfig{
				Email:       EmailConfig{ListenAddr: " "},
				WebhookAddr: "\t",
			},
			want: map[string]bool{"telegram": false, "email": false, "webhook": false},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := map[string]bool{}
			for _, frontEnd := range tc.chat.FrontEnds() {
				if frontEnd.ID == "" || frontEnd.Name == "" {
					t.Errorf("front-end %+v is missing an identifier or a name", frontEnd)
				}
				if _, seen := got[frontEnd.ID]; seen {
					t.Errorf("front-end %q listed twice", frontEnd.ID)
				}
				got[frontEnd.ID] = frontEnd.Configured
			}
			if len(got) != len(tc.want) {
				t.Fatalf("FrontEnds() = %+v, want one entry per front-end %+v", got, tc.want)
			}
			for id, want := range tc.want {
				if got[id] != want {
					t.Errorf("front-end %q configured = %v, want %v", id, got[id], want)
				}
			}

			var anyConfigured bool
			for _, configured := range got {
				anyConfigured = anyConfigured || configured
			}
			if got, want := tc.chat.AnyFrontEndConfigured(), anyConfigured; got != want {
				t.Errorf("AnyFrontEndConfigured() = %v, want %v", got, want)
			}
		})
	}
}
