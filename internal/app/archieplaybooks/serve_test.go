package archieplaybooks

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// diagnosticsClient is the editor side: it collects every
// publishDiagnostics notification the server sends.
type diagnosticsClient struct {
	protocol.UnimplementedClient
	diags chan *protocol.PublishDiagnosticsParams
}

func (c *diagnosticsClient) PublishDiagnostics(_ context.Context, p *protocol.PublishDiagnosticsParams) error {
	c.diags <- p
	return nil
}

// openInServe starts Serve over an in-memory pipe, initializes it, opens
// path, and returns the diagnostics published for it.
func openInServe(t *testing.T, path string) *protocol.PublishDiagnosticsParams {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	serverEnd, clientEnd := net.Pipe()
	go func() { _ = Serve(ctx, serverEnd) }()
	client := &diagnosticsClient{diags: make(chan *protocol.PublishDiagnosticsParams, 8)}
	_, conn, server := protocol.NewClient(ctx, client, jsonrpc2.NewStream(clientEnd))
	t.Cleanup(func() { _ = conn.Close() })

	if _, err := server.Initialize(ctx, &protocol.InitializeParams{}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	u := uri.File(path)
	if err := server.DidOpen(ctx, &protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{URI: u, LanguageID: "yaml", Version: 1}}); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-client.diags:
		if p.URI != u {
			t.Fatalf("diagnostics for %q, want %q", p.URI, u)
		}
		return p
	case <-ctx.Done():
		t.Fatal("no publishDiagnostics received")
	}
	return nil
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
			name:     "eda playbook finding on its action line",
			files:    map[string]string{"pb.yaml": "trigger:\n  kind: bug\nactions:\n  - position: module\n    kind: nope\n"},
			open:     "pb.yaml",
			wantLine: 3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range tt.files {
				writeFile(t, filepath.Join(dir, name), content)
			}
			p := openInServe(t, filepath.Join(dir, tt.open))
			if tt.wantLine < 0 {
				if len(p.Diagnostics) != 0 {
					t.Fatalf("diagnostics = %+v, want none", p.Diagnostics)
				}
				return
			}
			if len(p.Diagnostics) != 1 || int(p.Diagnostics[0].Range.Start.Line) != tt.wantLine {
				t.Fatalf("diagnostics = %+v, want one on line %d", p.Diagnostics, tt.wantLine)
			}
		})
	}
}

// The exit notification ends Serve. Closing the connection from inside the
// exit handler waited on that handler, so an editor quitting left the server
// running.
func TestServeReturnsAfterExit(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	serverEnd, clientEnd := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, serverEnd) }()
	_, conn, server := protocol.NewClient(ctx, &diagnosticsClient{}, jsonrpc2.NewStream(clientEnd))
	defer conn.Close()

	if _, err := server.Initialize(ctx, &protocol.InitializeParams{}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if err := server.Exit(ctx); err != nil {
		t.Fatalf("exit: %v", err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("Serve still running after exit")
	}
}
