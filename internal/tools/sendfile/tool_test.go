package sendfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/tools"
	"github.com/samcharles93/archie-core/internal/tools/builtin"
)

// writeFile creates a file of size bytes under dir and returns its path.
func writeFile(t *testing.T, dir, name string, size int) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// A prepared send must name the file as a local Path, never as a URL:
// upload is the whole point, and a path in the URL field is exactly the
// silent non-delivery this tool exists to end.
func TestPrepareReturnsLocalPathRef(t *testing.T) {
	tests := []struct {
		name     string
		file     string
		wantType string
	}{
		{"markdown is a document", "transcript.md", "document"},
		{"unknown extension is a document", "dump.bin", "document"},
		{"png is an image", "shot.png", "image"},
		{"mp4 is a video", "clip.mp4", "video"},
		{"mp3 is audio", "note.mp3", "audio"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			path := writeFile(t, ws, tc.file, 16)

			// Relative, to prove resolution happens against the workspace.
			res, err := prepare(ws, tc.file, "here you go")
			if err != nil {
				t.Fatalf("prepare: %v", err)
			}
			if !res.IsMultimodal {
				t.Error("IsMultimodal = false, want true")
			}
			if len(res.URLs) != 1 {
				t.Fatalf("refs = %d, want 1", len(res.URLs))
			}
			ref := res.URLs[0]
			if ref.Path != path {
				t.Errorf("Path = %q, want %q", ref.Path, path)
			}
			if ref.URL != "" {
				t.Errorf("URL = %q, want empty: a local file has no URL", ref.URL)
			}
			if ref.FileName != tc.file {
				t.Errorf("FileName = %q, want %q", ref.FileName, tc.file)
			}
			if ref.Type != tc.wantType {
				t.Errorf("Type = %q, want %q", ref.Type, tc.wantType)
			}
			if !strings.Contains(res.Summary, "here you go") {
				t.Errorf("Summary = %q, want it to carry the caption", res.Summary)
			}
		})
	}
}

// Every failure must be an error the model sees. Reporting a
// MultimodalResult for a file that cannot be delivered would put the
// caller back where it started: unable to tell delivery from silence.
func TestPrepareFailuresAreErrors(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()
	writeFile(t, outside, "secret.txt", 8)
	big := writeFile(t, ws, "big.bin", 0)
	if err := os.Truncate(big, MaxUploadBytes+1); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	tests := []struct {
		name    string
		path    string
		wantErr error
		wantSub string
	}{
		{name: "empty path", path: "   ", wantSub: "path is required"},
		{name: "missing file", path: "nope.txt", wantErr: os.ErrNotExist},
		{name: "directory", path: ".", wantSub: "not a regular file"},
		{name: "over the upload limit", path: "big.bin", wantSub: "upload limit"},
		{
			name:    "outside the workspace",
			path:    filepath.Join(outside, "secret.txt"),
			wantErr: builtin.ErrPathNotAllowed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := prepare(ws, tc.path, "")
			if err == nil {
				t.Fatalf("prepare succeeded, want an error (got %+v)", res)
			}
			if len(res.URLs) != 0 || res.IsMultimodal {
				t.Errorf("failed prepare returned deliverable media: %+v", res)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want it to wrap %v", err, tc.wantErr)
			}
			if tc.wantSub != "" && !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("err = %v, want it to mention %q", err, tc.wantSub)
			}
		})
	}
}

// Confinement is a deployment posture: with the jail lifted, a path
// outside the workspace must be sendable, exactly as the read tool can
// read it.
func TestPrepareRespectsUnrestrictedFilesystem(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()
	path := writeFile(t, outside, "unit.service", 8)

	if _, err := prepare(ws, path, ""); !errors.Is(err, builtin.ErrPathNotAllowed) {
		t.Fatalf("err = %v, want ErrPathNotAllowed while confined", err)
	}

	builtin.SetPathConfinement(false)
	t.Cleanup(func() { builtin.SetPathConfinement(true) })

	res, err := prepare(ws, path, "")
	if err != nil {
		t.Fatalf("prepare with confinement lifted: %v", err)
	}
	if len(res.URLs) != 1 || res.URLs[0].Path != path {
		t.Fatalf("refs = %+v, want the outside path", res.URLs)
	}
}

// The entry withdraws entirely without a workspace: there is no root to
// resolve against and no policy to apply.
func TestToolRequiresWorkspace(t *testing.T) {
	if entry := Tool(""); entry != nil {
		t.Fatalf("Tool(\"\") = %+v, want nil", entry)
	}
	entry := Tool(t.TempDir())
	if entry == nil {
		t.Fatal("Tool(workspace) = nil, want an entry")
	}
	if entry.Name != ToolName {
		t.Errorf("Name = %q, want %q", entry.Name, ToolName)
	}
	if entry.Handler == nil {
		t.Error("Handler is nil")
	}
}

// The handler must return the same result shape the channel decodes, so a
// wiring change cannot quietly stop producing media refs.
func TestHandlerReturnsMultimodalResult(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, ws, "log.txt", 4)
	entry := Tool(ws)

	out, err := entry.Handler(t.Context(), map[string]any{"path": "log.txt"})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	res, ok := out.(tools.MultimodalResult)
	if !ok {
		t.Fatalf("out is %T, want tools.MultimodalResult", out)
	}
	if len(res.URLs) != 1 {
		t.Fatalf("refs = %d, want 1", len(res.URLs))
	}
}
