package telegram

import "testing"

func TestSuccessfulPollResponse(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "successful empty poll", body: `{"ok":true,"result":[]}`, want: true},
		{name: "successful poll with update", body: `{"ok":true,"result":[{"update_id":1}]}`, want: true},
		{name: "Telegram API error", body: `{"ok":false,"error_code":409}`, want: false},
		{name: "malformed JSON", body: `{`, want: false},
		{name: "malformed result", body: `{"ok":true,"result":{}}`, want: false},
		{name: "missing result", body: `{"ok":true}`, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := successfulPollResponse([]byte(test.body)); got != test.want {
				t.Fatalf("successfulPollResponse() = %v, want %v", got, test.want)
			}
		})
	}
}
