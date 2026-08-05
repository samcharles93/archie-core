package webfetch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// testConfig is the base every case starts from. Private networks are allowed
// because httptest binds loopback; the cases that exercise the SSRF control
// override it explicitly.
func testConfig() Config {
	return Config{
		Enabled:              true,
		Timeout:              5 * time.Second,
		MaxBytes:             1 << 20,
		AllowPrivateNetworks: true,
	}
}

func TestFetchExtractsContent(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		raw         bool
		wantContain []string
		wantAbsent  []string
	}{
		{
			name:        "html is reduced to readable text",
			contentType: "text/html; charset=utf-8",
			body: `<html><head><title>T</title>
				<style>body{color:red}</style>
				<script>var x = "should not appear";</script></head>
				<body><h1>Heading</h1><p>Body text here.</p></body></html>`,
			wantContain: []string{"Heading", "Body text here."},
			wantAbsent:  []string{"should not appear", "color:red", "<p>"},
		},
		{
			name:        "raw returns the html unmodified",
			contentType: "text/html",
			body:        `<p>markup</p>`,
			raw:         true,
			wantContain: []string{"<p>markup</p>"},
		},
		{
			name:        "json passes through untouched",
			contentType: "application/json",
			body:        `{"key":"value"}`,
			wantContain: []string{`{"key":"value"}`},
		},
		{
			name:        "plain text passes through untouched",
			contentType: "text/plain",
			body:        "just words",
			wantContain: []string{"just words"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()

			got, err := New(testConfig()).Fetch(context.Background(), srv.URL, tc.raw)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.wantContain {
				if !strings.Contains(got, want) {
					t.Errorf("result %q does not contain %q", got, want)
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("result %q should not contain %q", got, absent)
				}
			}
		})
	}
}

func TestFetchRejectsUnsupportedContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		fmt.Fprint(w, "\x89PNG\r\n\x1a\n")
	}))
	defer srv.Close()

	_, err := New(testConfig()).Fetch(context.Background(), srv.URL, false)
	if err == nil {
		t.Fatal("Fetch() error = nil, want a refusal naming the content type")
	}
	if !strings.Contains(err.Error(), "image/png") {
		t.Errorf("error = %v, want it to name the content type", err)
	}
}

func TestFetchCapsBodySize(t *testing.T) {
	const limit = 512
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, strings.Repeat("a", limit*10))
	}))
	defer srv.Close()

	cfg := testConfig()
	cfg.MaxBytes = limit
	got, err := New(cfg).Fetch(context.Background(), srv.URL, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > limit*2 { // body plus a truncation notice, nowhere near 10x
		t.Errorf("result is %d bytes, want it bounded near the %d-byte limit", len(got), limit)
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("result %q should say it was truncated", got)
	}
}

func TestFetchReportsHTTPStatus(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "not found", status: http.StatusNotFound, body: "no such page"},
		{name: "forbidden", status: http.StatusForbidden, body: "denied"},
		{name: "rate limited", status: http.StatusTooManyRequests, body: "slow down"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()

			_, err := New(testConfig()).Fetch(context.Background(), srv.URL, false)
			if err == nil {
				t.Fatalf("Fetch() error = nil, want the %d reported", tc.status)
			}
			// The model has to tell 404 from 403 from a rate limit to decide
			// whether retrying or rewording the URL is worth it.
			if !strings.Contains(err.Error(), fmt.Sprint(tc.status)) {
				t.Errorf("error = %v, want it to carry status %d", err, tc.status)
			}
			if !strings.Contains(err.Error(), tc.body) {
				t.Errorf("error = %v, want it to carry a body excerpt", err)
			}
		})
	}
}

func TestFetchCapsRedirects(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL+"/next", http.StatusFound)
	}))
	defer srv.Close()

	_, err := New(testConfig()).Fetch(context.Background(), srv.URL, false)
	if err == nil {
		t.Fatal("Fetch() error = nil, want a redirect-limit refusal")
	}
	if !strings.Contains(err.Error(), "redirect") {
		t.Errorf("error = %v, want it to name the redirect limit", err)
	}
}

func TestFetchRejectsUnsupportedSchemes(t *testing.T) {
	tests := []string{
		"file:///etc/passwd",
		"ftp://example.com/x",
		"gopher://example.com",
		"data:text/plain,hello",
		"not-a-url",
	}

	for _, target := range tests {
		t.Run(target, func(t *testing.T) {
			_, err := New(testConfig()).Fetch(context.Background(), target, false)
			if err == nil {
				t.Fatalf("Fetch(%q) error = nil, want a scheme refusal", target)
			}
		})
	}
}

// TestFetchBlocksPrivateAddresses is the SSRF control. archied runs on a host
// where the Docker API, NATS and its own dashboard are reachable on private
// addresses, so a model that can be talked into fetching one of those is a
// real hole. httptest binds loopback, which makes it exactly the case to
// assert against.
func TestFetchBlocksPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "internal service")
	}))
	defer srv.Close()

	t.Run("blocked by default", func(t *testing.T) {
		cfg := testConfig()
		cfg.AllowPrivateNetworks = false
		_, err := New(cfg).Fetch(context.Background(), srv.URL, false)
		if err == nil {
			t.Fatal("Fetch() reached a loopback address, want a refusal")
		}
		if !strings.Contains(err.Error(), "private") && !strings.Contains(err.Error(), "loopback") {
			t.Errorf("error = %v, want it to explain the address was refused", err)
		}
	})

	t.Run("allowed when configured", func(t *testing.T) {
		cfg := testConfig() // AllowPrivateNetworks is true
		got, err := New(cfg).Fetch(context.Background(), srv.URL, false)
		if err != nil {
			t.Fatalf("Fetch() with private networks allowed: %v", err)
		}
		if !strings.Contains(got, "internal service") {
			t.Errorf("result = %q, want the body", got)
		}
	})
}

// TestFetchBlocksAtDialTime proves the check is on the resolved address rather
// than on the URL string. A hostname that looks public but resolves to a
// private address must still be refused, which a pre-flight URL check cannot
// do and which is how DNS-rebinding defeats one.
func TestFetchBlocksAtDialTime(t *testing.T) {
	cfg := testConfig()
	cfg.AllowPrivateNetworks = false

	// localtest.me and its subdomains are a public DNS name resolving to
	// 127.0.0.1. Resolution may be unavailable in a sandbox, so a lookup
	// failure is not a test failure -- but a success must be a refusal.
	_, err := New(cfg).Fetch(context.Background(), "http://localtest.me/", false)
	if err == nil {
		t.Fatal("Fetch() reached a public name resolving to loopback, want a refusal")
	}
	if strings.Contains(err.Error(), "scheme") {
		t.Errorf("error = %v, want an address refusal rather than a scheme refusal", err)
	}
}

func TestToolDisabledReturnsNil(t *testing.T) {
	cfg := testConfig()
	cfg.Enabled = false
	if Tool(cfg) != nil {
		t.Error("Tool() returned an entry while disabled, want nil so it is not advertised")
	}
}

func TestToolRejectsMissingURL(t *testing.T) {
	entry := Tool(testConfig())
	if entry == nil {
		t.Fatal("Tool() = nil, want an entry when enabled")
	}
	if _, err := entry.Handler(context.Background(), map[string]any{}); err == nil {
		t.Error("Handler() error = nil, want a refusal when url is absent")
	}
}
