package archieplaybooks

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
	"gopkg.in/yaml.v3"
)

// Serve runs the language server over rwc until the client disconnects or
// ctx ends. It publishes diagnostics when a playbook file is opened or saved,
// by linting the file's saved directory with the loader the daemon runs for
// it: the whole directory, because a collision is a cross-file finding.
// Unsaved edits are not validated.
func Serve(ctx context.Context, rwc io.ReadWriteCloser) error {
	s := &server{exited: make(chan struct{})}
	_, conn, client := protocol.NewServer(ctx, s, jsonrpc2.NewStream(rwc))
	s.client = client
	select {
	case <-conn.Done():
		return nil
	case <-s.exited:
	case <-ctx.Done():
	}
	return conn.Close()
}

type server struct {
	protocol.UnimplementedServer
	client protocol.Client
	// exited is closed by the exit notification. Serve closes the connection:
	// closing it from inside the handler would wait on that handler.
	exited   chan struct{}
	exitOnce sync.Once
}

func (s *server) Initialize(context.Context, *protocol.InitializeParams) (*protocol.InitializeResult, error) {
	// The server reads files from disk, so it needs opens, closes and saves,
	// not content changes.
	openClose, change := true, protocol.TextDocumentSyncKindNone
	return &protocol.InitializeResult{
		Capabilities: protocol.ServerCapabilities{
			TextDocumentSync: &protocol.TextDocumentSyncOptions{
				OpenClose: &openClose,
				Change:    &change,
				Save:      protocol.Boolean(true),
			},
		},
		ServerInfo: protocol.ServerInfo{Name: "archie-playbooks"},
	}, nil
}

func (s *server) Shutdown(context.Context) error { return nil }

func (s *server) Exit(context.Context) error {
	s.exitOnce.Do(func() { close(s.exited) })
	return nil
}

func (s *server) DidOpen(ctx context.Context, p *protocol.DidOpenTextDocumentParams) error {
	return s.client.PublishDiagnostics(ctx, diagnose(p.TextDocument.URI))
}

func (s *server) DidSave(ctx context.Context, p *protocol.DidSaveTextDocumentParams) error {
	return s.client.PublishDiagnostics(ctx, diagnose(p.TextDocument.URI))
}

func (s *server) DidClose(ctx context.Context, p *protocol.DidCloseTextDocumentParams) error {
	return s.client.PublishDiagnostics(ctx, &protocol.PublishDiagnosticsParams{URI: p.TextDocument.URI, Diagnostics: []protocol.Diagnostic{}})
}

// diagnose lints the directory holding u's file and returns the findings that
// belong to that file: a finding naming another file of the directory belongs
// there, and one naming this file without a line (a file-level error) goes on
// its first line.
func diagnose(u uri.URI) *protocol.PublishDiagnosticsParams {
	out := &protocol.PublishDiagnosticsParams{URI: u, Diagnostics: []protocol.Diagnostic{}}
	if !u.IsFile() {
		return out
	}
	path := u.FsPath()
	var result Result
	if isEDAPlaybook(path) {
		result = LintEDA(filepath.Dir(path), io.Discard)
	} else {
		result = Lint([]string{filepath.Dir(path)}, io.Discard)
	}
	for _, finding := range result.Findings {
		line, ok := findingLine(finding, path)
		if !ok {
			continue
		}
		out.Diagnostics = append(out.Diagnostics, protocol.Diagnostic{
			Range:    protocol.Range{Start: protocol.Position{Line: line}, End: protocol.Position{Line: line + 1}},
			Severity: protocol.DiagnosticSeverityError,
			Source:   protocol.NewOptional("archie-playbooks"),
			Message:  protocol.String(finding),
		})
	}
	return out
}

// isEDAPlaybook reports whether the file is an EDA playbook document (it has
// a top-level `trigger` key) rather than a routing binding file.
func isEDAPlaybook(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var doc map[string]yaml.Node
	if yaml.NewDecoder(bytes.NewReader(data)).Decode(&doc) != nil {
		return false
	}
	_, ok := doc["trigger"]
	return ok
}

// findingLine places a finding on path. A compiler-style `path:line:` prefix
// gives the 0-based line; a finding naming path without a line goes on line
// 0; a finding naming another file is not path's.
func findingLine(finding, path string) (uint32, bool) {
	if m := regexp.MustCompile(regexp.QuoteMeta(path) + `:(\d+):`).FindStringSubmatch(finding); m != nil {
		n, _ := strconv.ParseUint(m[1], 10, 32)
		return uint32(max(n, 1) - 1), true
	}
	return 0, strings.Contains(finding, path)
}
