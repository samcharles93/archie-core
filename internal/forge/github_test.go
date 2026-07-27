package forge

import (
	"log/slog"
	"testing"
)

func TestNewConfiguresHost(t *testing.T) {
	tests := []struct {
		name       string
		host       string
		wantBase   string
		wantUpload string
	}{
		{
			name:       "github.com",
			host:       "https://github.com",
			wantBase:   "https://api.github.com/",
			wantUpload: "https://uploads.github.com/",
		},
		{
			name:       "enterprise",
			host:       "https://git.example.com",
			wantBase:   "https://git.example.com/api/v3/",
			wantUpload: "https://git.example.com/api/uploads/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := New("github", "token", tt.host, slog.New(slog.DiscardHandler))
			if err != nil {
				t.Fatal(err)
			}
			client, ok := f.(*GitHubClient)
			if !ok {
				t.Fatalf("f is %T, want *GitHubClient", f)
			}
			if got := client.gh.BaseURL.String(); got != tt.wantBase {
				t.Errorf("BaseURL = %q, want %q", got, tt.wantBase)
			}
			if got := client.gh.UploadURL.String(); got != tt.wantUpload {
				t.Errorf("UploadURL = %q, want %q", got, tt.wantUpload)
			}
		})
	}
}
