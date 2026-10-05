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
		{"github form with charset", "application/x-www-form-urlencoded; charset=utf-8", "payload=" + url.QueryEscape(json), json, true},
		{"plain form", "application/x-www-form-urlencoded", "a=b&a=c&n=1", `{"a":"b","n":"1"}`, true},
		{"payload not json", "application/x-www-form-urlencoded", "payload=nope", `{"payload":"nope"}`, true},
		{"malformed form", "application/x-www-form-urlencoded", "a=%zz", "", false},
	}
	for _, tt := range tests {
		got, ok := formPayload(tt.contentType, []byte(tt.body))
		if ok != tt.ok || string(got) != tt.want {
			t.Errorf("%s: got %q %v, want %q %v", tt.name, got, ok, tt.want, tt.ok)
		}
	}
}
