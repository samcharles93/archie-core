package archieplaybooks

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/sourcegraph/jsonrpc2"
)

// lspClient drives Serve over an in-memory pipe and collects every
// publishDiagnostics notification it receives.
type lspClient struct {
	conn  *jsonrpc2.Conn
	diags chan publishDiagnosticsParams
}

func startServe(t *testing.T) *lspClient {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	server, client := net.Pipe()
	go func() { _ = Serve(ctx, server) }()
	c := &lspClient{diags: make(chan publishDiagnosticsParams, 8)}
	c.conn = jsonrpc2.NewConn(ctx, jsonrpc2.NewBufferedStream(client, jsonrpc2.VSCodeObjectCodec{}),
		jsonrpc2.HandlerWithError(func(_ context.Context, _ *jsonrpc2.Conn, r *jsonrpc2.Request) (any, error) {
			if r.Method == "textDocument/publishDiagnostics" && r.Params != nil {
				var p publishDiagnosticsParams
				if err := json.Unmarshal(*r.Params, &p); err == nil {
					c.diags <- p
				}
			}
			return nil, nil
		}))
	t.Cleanup(func() { _ = c.conn.Close() })
	var init map[string]any
	callCtx, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	if err := c.conn.Call(callCtx, "initialize", map[string]any{}, &init); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	return c
}

func (c *lspClient) open(t *testing.T, path string) publishDiagnosticsParams {
	t.Helper()
	uri := (&url.URL{Scheme: "file", Path: path}).String()
	if err := c.conn.Notify(t.Context(), "textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": uri, "languageId": "yaml", "version": 1, "text": ""},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-c.diags:
		if p.URI != uri {
			t.Fatalf("diagnostics for %q, want %q", p.URI, uri)
		}
		return p
	case <-time.After(5 * time.Second):
		t.Fatal("no publishDiagnostics received")
	}
	return publishDiagnosticsParams{}
}

func TestServePublishesDiagnostics(t *testing.T) {
	tests := []struct {
		name     string
		files    map[string]string
		open     string
		wantLine int // 0-based; -1 means no diagnostics
	}{
		{
			name:     "clean routing file",
			files:    map[string]string{"routes.yaml": "bug: tdd\n"},
			open:     "routes.yaml",
			wantLine: -1,
		},
		{
			name:     "routing finding on its key line",
			files:    map[string]string{"routes.yaml": "security: review\n\ndocs: \"\"\n"},
			open:     "routes.yaml",
			wantLine: 2,
		},
		{
			name:     "cross-file collision reported on the opened file",
			files:    map[string]string{"a.yaml": "security: review\n", "b.yaml": "\nsecurity: other\n"},
			open:     "b.yaml",
			wantLine: 1,
		},
		{
			name:     "eda playbook finding without a line",
			files:    map[string]string{"pb.yaml": "trigger:\n  kind: bug\nactions:\n  - position: module\n    kind: nope\n"},
			open:     "pb.yaml",
			wantLine: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range tt.files {
				writeFile(t, filepath.Join(dir, name), content)
			}
			p := startServe(t).open(t, filepath.Join(dir, tt.open))
			if tt.wantLine < 0 {
				if len(p.Diagnostics) != 0 {
					t.Fatalf("diagnostics = %+v, want none", p.Diagnostics)
				}
				return
			}
			if len(p.Diagnostics) != 1 || p.Diagnostics[0].Range.Start.Line != tt.wantLine {
				t.Fatalf("diagnostics = %+v, want one on line %d", p.Diagnostics, tt.wantLine)
			}
		})
	}
}
