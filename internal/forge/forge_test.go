package forge

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewDispatchesByForgeType(t *testing.T) {
	// Gitea client construction makes a network call to /api/v1/version, so
	// its happy path is covered separately in TestNewGitea against a fake
	// server rather than here.
	tests := []struct {
		name      string
		forgeType string
		wantErr   bool
	}{
		{name: "github", forgeType: "github"},
		{name: "none", forgeType: "none"},
		{name: "off", forgeType: "off"},
		{name: "disabled", forgeType: "disabled"},
		{name: "empty defaults to noop", forgeType: ""},
		{name: "unsupported type errors", forgeType: "bitbucket", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := New(tt.forgeType, "token", "", nil)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("New(%q) error = nil, want error", tt.forgeType)
				}
				if f != nil {
					t.Fatalf("New(%q) forge = %v, want nil on error", tt.forgeType, f)
				}
				return
			}
			if err != nil {
				t.Fatalf("New(%q) error = %v, want nil", tt.forgeType, err)
			}
			if f == nil {
				t.Fatalf("New(%q) forge = nil, want non-nil", tt.forgeType)
			}
		})
	}
}

func TestNewGitea(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/version" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"version":"1.21.0"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	f, err := New("gitea", "token", srv.URL, nil)
	if err != nil {
		t.Fatalf("New(\"gitea\") error = %v, want nil", err)
	}
	if f == nil {
		t.Fatal("New(\"gitea\") forge = nil, want non-nil")
	}
}

func TestNewGiteaUnreachableHost(t *testing.T) {
	if _, err := New("gitea", "token", "http://127.0.0.1:0", nil); err == nil {
		t.Fatal("New(\"gitea\") with unreachable host error = nil, want error")
	}
}
