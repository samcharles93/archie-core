package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPTransportSendReturnsResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":"1","result":{"status":"ok"}}`)
	}))
	defer srv.Close()

	tr := NewHTTPTransport(HTTPTransportConfig{Endpoint: srv.URL})
	resp, err := tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":"1","method":"test","params":{}}`))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	var msg Message
	if err := json.Unmarshal(resp, &msg); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if string(msg.Result) != `{"status":"ok"}` {
		t.Errorf("result = %s, want {\"status\":\"ok\"}", msg.Result)
	}
}

func TestHTTPTransportSendReturnsErrorOnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "boom")
	}))
	defer srv.Close()

	tr := NewHTTPTransport(HTTPTransportConfig{Endpoint: srv.URL})
	_, err := tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":"1","method":"test"}`))
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %v, want it to mention status 500", err)
	}
}

func TestHTTPTransportSendReturnsErrorOnConnectionRefused(t *testing.T) {
	tr := NewHTTPTransport(HTTPTransportConfig{Endpoint: "http://127.0.0.1:1"}) // nothing listening
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := tr.Send(ctx, []byte(`{"jsonrpc":"2.0","id":"1","method":"test"}`))
	if err == nil {
		t.Fatal("expected connection error")
	}
}

func TestHTTPTransportSendHonorsContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()

	tr := NewHTTPTransport(HTTPTransportConfig{Endpoint: srv.URL})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := tr.Send(ctx, []byte(`{"jsonrpc":"2.0","id":"1","method":"test"}`))
	if err == nil {
		t.Fatal("expected context deadline error")
	}
}

func TestHTTPTransportNotifyWritesWithoutWaitingForResponse(t *testing.T) {
	notified := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(notified)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	tr := NewHTTPTransport(HTTPTransportConfig{Endpoint: srv.URL})
	err := tr.Notify(context.Background(), []byte(`{"jsonrpc":"2.0","method":"initialized"}`))
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	select {
	case <-notified:
		// expected
	case <-time.After(time.Second):
		t.Fatal("notification was not delivered")
	}
}

func TestHTTPTransportSendWithCustomHeaders(t *testing.T) {
	var receivedHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":"1","result":{}}`)
	}))
	defer srv.Close()

	tr := NewHTTPTransport(HTTPTransportConfig{
		Endpoint: srv.URL,
		Headers:  map[string]string{"Authorization": "Bearer secret"},
	})
	_, err := tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":"1","method":"test"}`))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if receivedHeader != "Bearer secret" {
		t.Errorf("Authorization header = %q, want Bearer secret", receivedHeader)
	}
}

func TestHTTPTransportConfigDefaults(t *testing.T) {
	cfg := HTTPTransportConfig{}
	timeout := cfg.effectiveTimeout()
	if timeout != 30*time.Second {
		t.Errorf("default timeout = %v, want 30s", timeout)
	}
}
