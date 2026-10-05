package captureintake

import (
	"net/url"
	"testing"
)

func TestFormPayload(t *testing.T) {
	json := `{"ref":"refs/heads/main"}`
	tests := []struct {
		name, contentType, body, want string
		ok                            bool
	}{
		{"github form", "application/x-www-form-urlencoded", "payload=" + url.QueryEscape(json), json, true},
		{"json passes through", "application/json", json, "", false},
		{"form without payload", "application/x-www-form-urlencoded", "a=b", "", false},
		{"payload not json", "application/x-www-form-urlencoded", "payload=nope", "", false},
	}
	for _, tt := range tests {
		got, ok := formPayload(tt.contentType, []byte(tt.body))
		if ok != tt.ok || string(got) != tt.want {
			t.Errorf("%s: got %q %v, want %q %v", tt.name, got, ok, tt.want, tt.ok)
		}
	}
}
