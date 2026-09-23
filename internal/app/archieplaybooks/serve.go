package archieplaybooks

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/sourcegraph/jsonrpc2"
	"gopkg.in/yaml.v3"
)

// Serve runs the language server over rwc until the client disconnects or
// ctx ends. It publishes diagnostics when a playbook file is opened or saved,
// by linting the file's saved directory with the loader the daemon runs for
// it: the whole directory, because a collision is a cross-file finding.
// Unsaved edits are not validated.
func Serve(ctx context.Context, rwc io.ReadWriteCloser) error {
	conn := jsonrpc2.NewConn(ctx, jsonrpc2.NewBufferedStream(rwc, jsonrpc2.VSCodeObjectCodec{}), jsonrpc2.HandlerWithError(handle))
	select {
	case <-conn.DisconnectNotify():
	case <-ctx.Done():
		_ = conn.Close()
	}
	return nil
}

type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start position `json:"start"`
	End   position `json:"end"`
}

type diagnostic struct {
	Range    lspRange `json:"range"`
	Severity int      `json:"severity"`
	Source   string   `json:"source"`
	Message  string   `json:"message"`
}

type publishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Diagnostics []diagnostic `json:"diagnostics"`
}

type textDocumentParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
}

const severityError = 1

func handle(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (any, error) {
	switch req.Method {
	case "initialize":
		// Full sync with save notifications: the server reads files from
		// disk, so it needs to hear about opens and saves only.
		return map[string]any{"capabilities": map[string]any{
			"textDocumentSync": map[string]any{"openClose": true, "change": 0, "save": true},
		}}, nil
	case "shutdown":
		return nil, nil
	case "exit":
		return nil, conn.Close()
	case "textDocument/didOpen", "textDocument/didSave":
		if uri, ok := documentURI(req); ok {
			return nil, conn.Notify(ctx, "textDocument/publishDiagnostics", diagnose(uri))
		}
	case "textDocument/didClose":
		if uri, ok := documentURI(req); ok {
			return nil, conn.Notify(ctx, "textDocument/publishDiagnostics", publishDiagnosticsParams{URI: uri, Diagnostics: []diagnostic{}})
		}
	}
	if req.Notif {
		return nil, nil
	}
	return nil, &jsonrpc2.Error{Code: jsonrpc2.CodeMethodNotFound, Message: "method not supported: " + req.Method}
}

// documentURI reads a text-document notification's URI. A notification has
// no response to carry an error, so malformed params are ignored.
func documentURI(req *jsonrpc2.Request) (string, bool) {
	var p textDocumentParams
	if req.Params == nil || json.Unmarshal(*req.Params, &p) != nil || p.TextDocument.URI == "" {
		return "", false
	}
	return p.TextDocument.URI, true
}

// diagnose lints the directory holding uri's file and returns the findings
// that belong to that file: a finding naming another file of the directory
// belongs there, and one naming this file without a line (an EDA load error)
// goes on its first line.
func diagnose(uri string) publishDiagnosticsParams {
	out := publishDiagnosticsParams{URI: uri, Diagnostics: []diagnostic{}}
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return out
	}
	path := u.Path
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
		out.Diagnostics = append(out.Diagnostics, diagnostic{
			Range:    lspRange{Start: position{Line: line}, End: position{Line: line, Character: 1 << 16}},
			Severity: severityError,
			Source:   "archie-playbooks",
			Message:  finding,
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
func findingLine(finding, path string) (int, bool) {
	if m := regexp.MustCompile(regexp.QuoteMeta(path) + `:(\d+):`).FindStringSubmatch(finding); m != nil {
		n, _ := strconv.Atoi(m[1])
		return max(n-1, 0), true
	}
	if strings.Contains(finding, path) {
		return 0, true
	}
	return 0, false
}
