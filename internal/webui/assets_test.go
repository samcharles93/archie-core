package webui

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAssetsFallbackStopsAtAPI(t *testing.T) {
	tests := []struct {
		path string
		want int
	}{
		{"/api/control-plane/unknown", http.StatusNotFound},
		{"/tasks/12", http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			new(Server).assets().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.path, nil))
			if rec.Code != tt.want {
				t.Fatalf("status %d, want %d", rec.Code, tt.want)
			}
		})
	}
}
