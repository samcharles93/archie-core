package webui

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

func TestAuthorizeTaskMutationOrigin(t *testing.T) {
	tests := []struct {
		name                 string
		trustForwardedDirect bool
		trustForwardedCfg    *bool
		forwardedProto       string
		forwardedHost        string
		requestHost          string
		tls                  bool
		origin               string
		csrfHeader           string
		contentType          string
		wantStatus           int
	}{
		{
			name:                 "1. proxied https: trust enabled directly, X-Forwarded-Proto https, plain HTTP, Origin https",
			trustForwardedDirect: true,
			forwardedProto:       "https",
			requestHost:          "archie.catlow.cloud",
			tls:                  false,
			origin:               "https://archie.catlow.cloud",
			csrfHeader:           "1",
			contentType:          "application/json",
			wantStatus:           http.StatusOK,
		},
		{
			name:              "1b. proxied https: trust enabled via Cfg holder, X-Forwarded-Proto https, plain HTTP, Origin https",
			trustForwardedCfg: new(true),
			forwardedProto:    "https",
			requestHost:       "archie.catlow.cloud",
			tls:               false,
			origin:            "https://archie.catlow.cloud",
			csrfHeader:        "1",
			contentType:       "application/json",
			wantStatus:        http.StatusOK,
		},
		{
			name:                 "1c. proxied https: comma-separated X-Forwarded-Proto list",
			trustForwardedDirect: true,
			forwardedProto:       "https, http",
			requestHost:          "archie.catlow.cloud",
			tls:                  false,
			origin:               "https://archie.catlow.cloud",
			csrfHeader:           "1",
			contentType:          "application/json",
			wantStatus:           http.StatusOK,
		},
		{
			name:                 "1d. proxied https: X-Forwarded-Host trusted and matched",
			trustForwardedDirect: true,
			forwardedProto:       "https",
			forwardedHost:        "archie.catlow.cloud",
			requestHost:          "10.36.25.188:8484",
			tls:                  false,
			origin:               "https://archie.catlow.cloud",
			csrfHeader:           "1",
			contentType:          "application/json",
			wantStatus:           http.StatusOK,
		},
		{
			name:                 "2. direct http: trust disabled, no forwarded header, Origin http",
			trustForwardedDirect: false,
			requestHost:          "localhost:8484",
			tls:                  false,
			origin:               "http://localhost:8484",
			csrfHeader:           "1",
			contentType:          "application/json",
			wantStatus:           http.StatusOK,
		},
		{
			name:                 "3. direct https: trust disabled, r.TLS set, Origin https",
			trustForwardedDirect: false,
			requestHost:          "localhost:8484",
			tls:                  true,
			origin:               "https://localhost:8484",
			csrfHeader:           "1",
			contentType:          "application/json",
			wantStatus:           http.StatusOK,
		},
		{
			name:                 "4. SPOOFED: trust disabled, attacker sends X-Forwarded-Proto https, plain HTTP, Origin https",
			trustForwardedDirect: false,
			forwardedProto:       "https",
			requestHost:          "localhost:8484",
			tls:                  false,
			origin:               "https://localhost:8484",
			csrfHeader:           "1",
			contentType:          "application/json",
			wantStatus:           http.StatusForbidden,
		},
		{
			name:                 "4b. SPOOFED HOST: trust disabled, attacker sends X-Forwarded-Host, plain HTTP",
			trustForwardedDirect: false,
			forwardedHost:        "target.example.com",
			requestHost:          "localhost:8484",
			tls:                  false,
			origin:               "http://target.example.com",
			csrfHeader:           "1",
			contentType:          "application/json",
			wantStatus:           http.StatusForbidden,
		},
		{
			name:                 "5. missing CSRF header: request rejected",
			trustForwardedDirect: true,
			forwardedProto:       "https",
			requestHost:          "archie.catlow.cloud",
			tls:                  false,
			origin:               "https://archie.catlow.cloud",
			csrfHeader:           "",
			contentType:          "application/json",
			wantStatus:           http.StatusForbidden,
		},
		{
			name:                 "6. genuinely cross-origin request: request rejected",
			trustForwardedDirect: true,
			forwardedProto:       "https",
			requestHost:          "archie.catlow.cloud",
			tls:                  false,
			origin:               "https://evil.example.com",
			csrfHeader:           "1",
			contentType:          "application/json",
			wantStatus:           http.StatusForbidden,
		},
		{
			name:                 "7. invalid Origin URI: containing userinfo",
			trustForwardedDirect: true,
			forwardedProto:       "https",
			requestHost:          "archie.catlow.cloud",
			tls:                  false,
			origin:               "https://user:pass@archie.catlow.cloud",
			csrfHeader:           "1",
			contentType:          "application/json",
			wantStatus:           http.StatusForbidden,
		},
		{
			name:                 "8. invalid Origin URI: containing path",
			trustForwardedDirect: true,
			forwardedProto:       "https",
			requestHost:          "archie.catlow.cloud",
			tls:                  false,
			origin:               "https://archie.catlow.cloud/path",
			csrfHeader:           "1",
			contentType:          "application/json",
			wantStatus:           http.StatusForbidden,
		},
		{
			name:                 "9. unsupported media type: non-JSON body",
			trustForwardedDirect: true,
			forwardedProto:       "https",
			requestHost:          "archie.catlow.cloud",
			tls:                  false,
			origin:               "https://archie.catlow.cloud",
			csrfHeader:           "1",
			contentType:          "text/plain",
			wantStatus:           http.StatusUnsupportedMediaType,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t)
			ctx := t.Context()
			if _, err := srv.Store.EnqueueIssue(ctx, "acme", "widget", 1, "queued", "", "", ""); err != nil {
				t.Fatal(err)
			}
			task, err := srv.Store.TaskByIssue(ctx, "acme", "widget", 1)
			if err != nil || task == nil {
				t.Fatalf("failed to fetch enqueued task: %v", err)
			}

			srv.TrustForwardedHeaders = tc.trustForwardedDirect
			if tc.trustForwardedCfg != nil {
				srv.Cfg = config.NewHolder(config.Config{
					Web: config.Web{
						TrustForwardedHeaders: *tc.trustForwardedCfg,
					},
				})
			}

			path := "/api/tasks/" + strconv.FormatInt(task.ID, 10) + "/action"
			req := httptest.NewRequestWithContext(ctx, http.MethodPost, path, strings.NewReader(`{"action":"cancel"}`))

			if tc.requestHost != "" {
				req.Host = tc.requestHost
			}
			if tc.tls {
				req.TLS = &tls.ConnectionState{}
			}
			if tc.forwardedProto != "" {
				req.Header.Set("X-Forwarded-Proto", tc.forwardedProto)
			}
			if tc.forwardedHost != "" {
				req.Header.Set("X-Forwarded-Host", tc.forwardedHost)
			}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.csrfHeader != "" {
				req.Header.Set("X-Archie-CSRF", tc.csrfHeader)
			}
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}

			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", w.Code, tc.wantStatus, w.Body.String())
			}
			if tc.wantStatus >= http.StatusBadRequest {
				if got := w.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
					t.Errorf("Content-Type = %q, want text/plain error response", got)
				}
				if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
					t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
				}
			}
		})
	}
}

func TestWorkRequestOriginProxy(t *testing.T) {
	srv := newTestServer(t)
	creator := &recordingWorkRequestCreator{}
	srv.WorkRequests = creator
	srv.Workflows = []workflow.Definition{{ID: "implement", Name: "Implement", Enabled: true}}
	srv.TrustForwardedHeaders = true

	body := `{"identity":"archie","repository":"acme/widget","workflow":"implement","title":"Fix login","instructions":"Reproduce and fix it."}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/work-requests", strings.NewReader(body))
	req.Host = "archie.catlow.cloud"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Archie-CSRF", "1")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Origin", "https://archie.catlow.cloud")

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %q)", w.Code, w.Body.String())
	}
}
