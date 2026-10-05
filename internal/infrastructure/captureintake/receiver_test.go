package captureintake

import (
	"net/url"
	"testing"
)

func TestFormPayload(t *testing.T) {
	object := `{"ref":"refs/heads/main"}`
	tests := []struct {
		name, contentType, body, want string
		ok                            bool
	}{
		{"json field nests", "application/x-www-form-urlencoded", "payload=" + url.QueryEscape(object), `{"payload":{"ref":"refs/heads/main"}}`, true},
		{"charset parameter", "application/x-www-form-urlencoded; charset=utf-8", "a=b", `{"a":"b"}`, true},
		{"first value per name", "application/x-www-form-urlencoded", "a=b&a=c&n=1", `{"a":"b","n":"1"}`, true},
		{"invalid json stays text", "application/x-www-form-urlencoded", "a=%7Bnope", `{"a":"{nope"}`, true},
		{"json passes through", "application/json", object, "", false},
		{"malformed form", "application/x-www-form-urlencoded", "a=%zz", "", false},
	}
	for _, tt := range tests {
		got, ok := formPayload(tt.contentType, []byte(tt.body))
		if ok != tt.ok || string(got) != tt.want {
			t.Errorf("%s: got %q %v, want %q %v", tt.name, got, ok, tt.want, tt.ok)
		}
	}
}
