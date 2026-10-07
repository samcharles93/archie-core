package webui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestChatFileDownload: only a file a turn offered is served, and only until
// its token expires.
func TestChatFileDownload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(path, []byte("contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	srv := &Server{}
	srv.chatFiles.now = func() time.Time { return now }
	token := srv.chatFiles.offer(path, "")

	tests := []struct {
		name  string
		token string
		after time.Duration
		want  int
	}{
		{name: "an offered file is served", token: token, want: http.StatusOK},
		{name: "a token nobody was given is refused", token: "0123456789abcdef0123456789abcdef", want: http.StatusNotFound},
		{name: "a path is not a token", token: "..%2f..%2fetc%2fpasswd", want: http.StatusNotFound},
		{name: "an expired token is refused", token: token, after: chatFileTTL + time.Second, want: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv.chatFiles.now = func() time.Time { return now.Add(tt.after) }
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/chat/file/x", nil)
			req.SetPathValue("token", tt.token)
			rec := httptest.NewRecorder()
			srv.handleChatFile(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
			if tt.want == http.StatusOK && rec.Body.String() != "contents" {
				t.Fatalf("body = %q", rec.Body.String())
			}
		})
	}
}
