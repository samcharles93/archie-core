package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/samcharles93/archie-core/internal/tools"
)

// A 1x1 transparent PNG, base64-encoded  --  small, valid binary fixture.
const tinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

func TestCallToolHandlerReturnsPlainStringForTextOnlyResult(t *testing.T) {
	// Backward compatibility: a pure-text result stays a plain string,
	// not a MultimodalResult  --  existing callers/tests depend on this.
	f := &fakeSender{
		responses: map[string][]byte{
			"tools/list": []byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"echo","inputSchema":{}}]}}`),
			"tools/call": []byte(`{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"hello"}]}}`),
		},
	}
	c := newTestClient(f)
	reg := tools.NewRegistry()
	if _, err := c.RegisterTools(context.Background(), reg); err != nil {
		t.Fatal(err)
	}
	entry, _ := reg.Get("mcp.test-server.echo")

	out, err := entry.Handler(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello" {
		t.Errorf("out = %v (%T), want plain string \"hello\"", out, out)
	}
}

func TestCallToolHandlerWritesImageAndReturnsMultimodalResult(t *testing.T) {
	dir := t.TempDir()
	f := &fakeSender{
		responses: map[string][]byte{
			"tools/list": []byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"screenshot","inputSchema":{}}]}}`),
			"tools/call": []byte(`{"jsonrpc":"2.0","id":2,"result":{"content":[
				{"type":"text","text":"captured the screen"},
				{"type":"image","data":"` + tinyPNGBase64 + `","mimeType":"image/png"}
			]}}`),
		},
	}
	c := NewClient(f, "test-server", WithMediaDir(dir))
	reg := tools.NewRegistry()
	if _, err := c.RegisterTools(context.Background(), reg); err != nil {
		t.Fatal(err)
	}
	entry, _ := reg.Get("mcp.test-server.screenshot")

	out, err := entry.Handler(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	result, ok := out.(tools.MultimodalResult)
	if !ok {
		t.Fatalf("out type = %T, want tools.MultimodalResult", out)
	}
	if !result.IsMultimodal {
		t.Error("IsMultimodal = false, want true")
	}
	if result.Summary != "captured the screen" {
		t.Errorf("Summary = %q", result.Summary)
	}
	if result.SubdirHint == "" {
		t.Fatal("SubdirHint is empty, want a written subdirectory")
	}
	if len(result.Files) != 1 {
		t.Fatalf("Files = %v, want 1 entry", result.Files)
	}

	written, err := os.ReadFile(result.Files[0])
	if err != nil {
		t.Fatalf("written file not readable: %v", err)
	}
	wantBytes, _ := base64.StdEncoding.DecodeString(tinyPNGBase64)
	if string(written) != string(wantBytes) {
		t.Error("written file contents do not match the decoded image data")
	}
	if !filepath.IsAbs(result.Files[0]) {
		t.Error("Files entries should be absolute paths")
	}
}

func TestCallToolHandlerWithoutMediaDirSkipsFilePersistence(t *testing.T) {
	f := &fakeSender{
		responses: map[string][]byte{
			"tools/list": []byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"screenshot","inputSchema":{}}]}}`),
			"tools/call": []byte(`{"jsonrpc":"2.0","id":2,"result":{"content":[
				{"type":"image","data":"` + tinyPNGBase64 + `","mimeType":"image/png"}
			]}}`),
		},
	}
	c := newTestClient(f) // no WithMediaDir
	reg := tools.NewRegistry()
	if _, err := c.RegisterTools(context.Background(), reg); err != nil {
		t.Fatal(err)
	}
	entry, _ := reg.Get("mcp.test-server.screenshot")

	out, err := entry.Handler(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	result, ok := out.(tools.MultimodalResult)
	if !ok {
		t.Fatalf("out type = %T, want tools.MultimodalResult", out)
	}
	if !result.IsMultimodal {
		t.Error("IsMultimodal = false, want true")
	}
	if result.SubdirHint != "" || len(result.Files) != 0 {
		t.Errorf("expected no persisted files without a media dir, got SubdirHint=%q Files=%v", result.SubdirHint, result.Files)
	}
}

func TestCallToolHandlerRejectsMalformedBase64(t *testing.T) {
	dir := t.TempDir()
	f := &fakeSender{
		responses: map[string][]byte{
			"tools/list": []byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"broken_image","inputSchema":{}}]}}`),
			"tools/call": []byte(`{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"image","data":"not-valid-base64!!!","mimeType":"image/png"}]}}`),
		},
	}
	c := NewClient(f, "test-server", WithMediaDir(dir))
	reg := tools.NewRegistry()
	if _, err := c.RegisterTools(context.Background(), reg); err != nil {
		t.Fatal(err)
	}
	entry, _ := reg.Get("mcp.test-server.broken_image")

	_, err := entry.Handler(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error decoding malformed base64 image data")
	}
}

func TestCallToolHandlerHandlesResourceBlobAsMedia(t *testing.T) {
	dir := t.TempDir()
	f := &fakeSender{
		responses: map[string][]byte{
			"tools/list": []byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"export_pdf","inputSchema":{}}]}}`),
			"tools/call": []byte(`{"jsonrpc":"2.0","id":2,"result":{"content":[
				{"type":"resource","resource":{"uri":"file:///report.pdf","mimeType":"application/pdf","blob":"` + tinyPNGBase64 + `"}}
			]}}`),
		},
	}
	c := NewClient(f, "test-server", WithMediaDir(dir))
	reg := tools.NewRegistry()
	if _, err := c.RegisterTools(context.Background(), reg); err != nil {
		t.Fatal(err)
	}
	entry, _ := reg.Get("mcp.test-server.export_pdf")

	out, err := entry.Handler(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	result, ok := out.(tools.MultimodalResult)
	if !ok {
		t.Fatalf("out type = %T, want tools.MultimodalResult", out)
	}
	if len(result.Files) != 1 {
		t.Fatalf("Files = %v, want 1 entry for the resource blob", result.Files)
	}
}

func TestCallToolHandlerTreatsResourceTextAsPlainText(t *testing.T) {
	// A resource block with inline text (not a blob) is not media  --  it
	// should fold into Summary text, not produce a written file.
	f := &fakeSender{
		responses: map[string][]byte{
			"tools/list": []byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"read_config","inputSchema":{}}]}}`),
			"tools/call": []byte(`{"jsonrpc":"2.0","id":2,"result":{"content":[
				{"type":"resource","resource":{"uri":"file:///config.yaml","mimeType":"text/yaml","text":"key: value"}}
			]}}`),
		},
	}
	c := newTestClient(f)
	reg := tools.NewRegistry()
	if _, err := c.RegisterTools(context.Background(), reg); err != nil {
		t.Fatal(err)
	}
	entry, _ := reg.Get("mcp.test-server.read_config")

	out, err := entry.Handler(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != "key: value" {
		t.Errorf("out = %v (%T), want plain text \"key: value\"", out, out)
	}
}

func TestCallToolHandlerRejectsPathTraversalToolName(t *testing.T) {
	dir := t.TempDir()
	malicious := "../../../tmp/archie-mcp-traversal-test"
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1,
		"result": map[string]any{"tools": []map[string]any{{"name": malicious, "inputSchema": map[string]any{}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSender{
		responses: map[string][]byte{
			"tools/list": body,
			"tools/call": []byte(`{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"image","data":"` + tinyPNGBase64 + `","mimeType":"image/png"}]}}`),
		},
	}
	c := NewClient(f, "test-server", WithMediaDir(dir))
	reg := tools.NewRegistry()
	if _, err := c.RegisterTools(context.Background(), reg); err != nil {
		t.Fatal(err)
	}
	entry, ok := reg.Get("mcp." + "test-server" + "." + malicious)
	if !ok {
		t.Fatal("expected the tool to be registered even with a malicious name (rejection happens at call time)")
	}

	_, err = entry.Handler(context.Background(), nil)
	if err == nil {
		t.Fatal("expected an error rejecting the path-traversal tool name, media would have escaped mediaDir")
	}
	if !strings.Contains(err.Error(), "not safe as a path component") {
		t.Errorf("error = %v, want a path-traversal rejection message", err)
	}
}

func TestWriteMultimodalResultRejectsDotAndDotDotToolNames(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{".", "..", "a/b", `a\b`} {
		c := NewClient(&fakeSender{}, "test-server", WithMediaDir(dir))
		_, err := c.writeMultimodalResult(name, "", []ContentBlock{{Type: "image", Data: tinyPNGBase64, MimeType: "image/png"}})
		if err == nil {
			t.Errorf("toolName %q: expected rejection, got no error", name)
		}
	}
}

func TestWriteMultimodalResultRejectsOversizedMediaBlock(t *testing.T) {
	dir := t.TempDir()
	c := NewClient(&fakeSender{}, "test-server", WithMediaDir(dir))
	oversized := strings.Repeat("A", maxMediaBlockBase64Bytes+1)

	_, err := c.writeMultimodalResult("big_tool", "", []ContentBlock{{Type: "image", Data: oversized, MimeType: "image/png"}})
	if err == nil {
		t.Fatal("expected an error for a media block exceeding the size cap")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("error = %v, want a size-limit message", err)
	}

	// The per-call subdirectory may have been created (MkdirAll happens
	// before the size check), but no file should have been written
	// anywhere under dir.
	var foundFile string
	if err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			foundFile = path
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if foundFile != "" {
		t.Errorf("expected no files written under %s, found %s", dir, foundFile)
	}
}

func TestWriteMultimodalResultConcurrentCallsDoNotClobberFiles(t *testing.T) {
	dir := t.TempDir()
	f := &fakeSender{
		responses: map[string][]byte{
			"tools/list": []byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"snap","inputSchema":{}}]}}`),
			"tools/call": []byte(`{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"image","data":"` + tinyPNGBase64 + `","mimeType":"image/png"}]}}`),
		},
	}
	c := NewClient(f, "test-server", WithMediaDir(dir))
	reg := tools.NewRegistry()
	if _, err := c.RegisterTools(context.Background(), reg); err != nil {
		t.Fatal(err)
	}
	entry, _ := reg.Get("mcp.test-server.snap")

	const n = 25
	var wg sync.WaitGroup
	results := make([]tools.MultimodalResult, n)
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out, err := entry.Handler(context.Background(), nil)
			if err != nil {
				errs[i] = err
				return
			}
			results[i] = out.(tools.MultimodalResult)
		}(i)
	}
	wg.Wait()

	seen := make(map[string]bool, n)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if len(results[i].Files) != 1 {
			t.Fatalf("call %d: Files = %v, want 1 entry", i, results[i].Files)
		}
		path := results[i].Files[0]
		if seen[path] {
			t.Fatalf("concurrent calls both wrote to %q  --  a file was clobbered", path)
		}
		seen[path] = true
	}
	if len(seen) != n {
		t.Fatalf("expected %d distinct file paths, got %d", n, len(seen))
	}
}

func TestCallToolHandlerMultipleImagesGetDistinctFiles(t *testing.T) {
	dir := t.TempDir()
	f := &fakeSender{
		responses: map[string][]byte{
			"tools/list": []byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"multi_shot","inputSchema":{}}]}}`),
			"tools/call": []byte(`{"jsonrpc":"2.0","id":2,"result":{"content":[
				{"type":"image","data":"` + tinyPNGBase64 + `","mimeType":"image/png"},
				{"type":"image","data":"` + tinyPNGBase64 + `","mimeType":"image/png"}
			]}}`),
		},
	}
	c := NewClient(f, "test-server", WithMediaDir(dir))
	reg := tools.NewRegistry()
	if _, err := c.RegisterTools(context.Background(), reg); err != nil {
		t.Fatal(err)
	}
	entry, _ := reg.Get("mcp.test-server.multi_shot")

	out, err := entry.Handler(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	result := out.(tools.MultimodalResult)
	if len(result.Files) != 2 {
		t.Fatalf("Files = %v, want 2 distinct files", result.Files)
	}
	if result.Files[0] == result.Files[1] {
		t.Error("both images wrote to the same file path")
	}
}
