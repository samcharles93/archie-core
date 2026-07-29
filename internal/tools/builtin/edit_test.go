package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyEdits(t *testing.T) {
	cases := []struct {
		name    string
		content string
		edits   []EditAction
		want    string
		wantErr string
	}{
		{
			name:    "single edit",
			content: "func a() {}\nfunc b() {}\n",
			edits:   []EditAction{{OldText: "func a()", NewText: "func alpha()"}},
			want:    "func alpha() {}\nfunc b() {}\n",
		},
		{
			name:    "multiple disjoint edits applied against original",
			content: "one\ntwo\nthree\n",
			edits: []EditAction{
				{OldText: "three", NewText: "3"},
				{OldText: "one", NewText: "1"},
			},
			want: "1\ntwo\n3\n",
		},
		{
			name:    "replace all",
			content: "x = a; y = a; z = a\n",
			edits:   []EditAction{{OldText: "a", NewText: "b", ReplaceAll: true}},
			want:    "x = b; y = b; z = b\n",
		},
		{
			name:    "not found",
			content: "hello\n",
			edits:   []EditAction{{OldText: "goodbye", NewText: "farewell"}},
			wantErr: "old_text not found",
		},
		{
			name:    "ambiguous without replace_all",
			content: "dup\ndup\ndup\n",
			edits:   []EditAction{{OldText: "dup", NewText: "uniq"}},
			wantErr: "appears 3 times",
		},
		{
			name:    "empty old_text",
			content: "hello\n",
			edits:   []EditAction{{OldText: "", NewText: "x"}},
			wantErr: "must not be empty",
		},
		{
			name:    "identical old and new",
			content: "hello\n",
			edits:   []EditAction{{OldText: "hello", NewText: "hello"}},
			wantErr: "identical",
		},
		{
			name:    "overlapping edits rejected",
			content: "abcdef\n",
			edits: []EditAction{
				{OldText: "abcd", NewText: "x"},
				{OldText: "cdef", NewText: "y"},
			},
			wantErr: "overlap",
		},
		{
			name:    "fuzzy match forgives trailing whitespace in file",
			content: "line one  \nline two\n", // trailing spaces on line one
			edits:   []EditAction{{OldText: "line one\nline two", NewText: "replaced"}},
			want:    "replaced\n",
		},
		{
			name:    "fuzzy match forgives trailing whitespace in old_text",
			content: "line one\nline two\n",
			edits:   []EditAction{{OldText: "line one   \nline two", NewText: "replaced"}},
			want:    "replaced\n",
		},
		{
			name:    "atomic: second edit failing means no content returned",
			content: "one\ntwo\n",
			edits: []EditAction{
				{OldText: "one", NewText: "1"},
				{OldText: "missing", NewText: "x"},
			},
			wantErr: "edit 2: old_text not found",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := applyEdits(tc.content, tc.edits)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got result:\n%s", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got: %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("result mismatch:\ngot:  %q\nwant: %q", got, tc.want)
			}
		})
	}
}

func TestParseEditParams_Quirks(t *testing.T) {
	t.Run("edits as JSON-encoded string", func(t *testing.T) {
		params := `{"path": "f.txt", "edits": "[{\"old_text\": \"a\", \"new_text\": \"b\"}]"}`
		p, err := parseEditParams(json.RawMessage(params))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(p.Edits) != 1 || p.Edits[0].OldText != "a" || p.Edits[0].NewText != "b" {
			t.Fatalf("unexpected edits: %+v", p.Edits)
		}
	})

	t.Run("flat old_text and new_text", func(t *testing.T) {
		params := `{"path": "f.txt", "old_text": "a", "new_text": "b", "replace_all": true}`
		p, err := parseEditParams(json.RawMessage(params))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(p.Edits) != 1 || p.Edits[0].OldText != "a" || !p.Edits[0].ReplaceAll {
			t.Fatalf("unexpected edits: %+v", p.Edits)
		}
	})

	t.Run("canonical form unchanged", func(t *testing.T) {
		params := `{"path": "f.txt", "edits": [{"old_text": "a", "new_text": "b"}, {"old_text": "c", "new_text": "d"}]}`
		p, err := parseEditParams(json.RawMessage(params))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(p.Edits) != 2 {
			t.Fatalf("expected 2 edits, got %+v", p.Edits)
		}
	})

	t.Run("invalid edits payload errors", func(t *testing.T) {
		params := `{"path": "f.txt", "edits": "not json"}`
		if _, err := parseEditParams(json.RawMessage(params)); err == nil {
			t.Fatal("expected error for undecodable edits")
		}
	})
}

func TestEditTool_PreservesCRLFAndBOM(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "f.txt")
	if err := os.WriteFile(path, []byte("\ufefffoo\r\nbar\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditTool(tmp, NewMutationQueue(), nil)
	// old_text uses LF: matching must succeed against the CRLF file.
	params := `{"path": "f.txt", "edits": [{"old_text": "foo\nbar", "new_text": "baz\nqux"}]}`
	res, err := tool.Execute(context.Background(), json.RawMessage(params), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "\ufeffbaz\r\nqux\r\n" {
		t.Fatalf("BOM or CRLF not preserved, got: %q", string(got))
	}
}

func TestEditTool_AtomicOnDisk(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "f.txt")
	original := "one\ntwo\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditTool(tmp, NewMutationQueue(), nil)
	// First edit is valid, second fails: the file must be left untouched.
	params := `{"path": "f.txt", "edits": [{"old_text": "one", "new_text": "1"}, {"old_text": "missing", "new_text": "x"}]}`
	res, err := tool.Execute(context.Background(), json.RawMessage(params), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected tool error, got: %s", res.Content)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatalf("file was partially modified on failure: %q", string(got))
	}
}

func TestEditTool_PopulatesDiffDetails(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "f.txt")
	original := "one\ntwo\nthree\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditTool(tmp, NewMutationQueue(), nil)
	params := `{"path": "f.txt", "edits": [{"old_text": "two", "new_text": "TWO"}]}`
	res, err := tool.Execute(context.Background(), json.RawMessage(params), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content)
	}

	details, ok := res.Details.(DiffDetails)
	if !ok {
		t.Fatalf("expected Details to be a DiffDetails, got %T", res.Details)
	}
	if details.OldContent != original {
		t.Fatalf("OldContent mismatch:\ngot:  %q\nwant: %q", details.OldContent, original)
	}
	wantNew := "one\nTWO\nthree\n"
	if details.NewContent != wantNew {
		t.Fatalf("NewContent mismatch:\ngot:  %q\nwant: %q", details.NewContent, wantNew)
	}
	if details.Path != path {
		t.Fatalf("Path mismatch: got %q want %q", details.Path, path)
	}
}

func TestApplyEdits_ExactMatchPreservesTrailingWhitespace(t *testing.T) {
	// A single-line old_text is an exact substring match even when the file
	// line has trailing whitespace, which is preserved around the edit.
	content := "keep\ntarget  \nkeep\n"
	got, err := applyEdits(content, []EditAction{{OldText: "target", NewText: "T"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "keep\nT  \nkeep\n" {
		t.Fatalf("result mismatch: %q", got)
	}
}

// The dominant stale_edit cause is indentation drift: the model reproduces the
// right code with the wrong leading whitespace. Naming the line and showing the
// file's actual text lets the retry succeed without re-reading the file.
func TestEdit_NotFoundReportsNearestMatch(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "f.go")
	content := "package p\n\nfunc handleCancel() {\n\t\tdoWork()\n}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	rt := NewReadTracker()
	rt.MarkRead(tmp, path)
	tool := NewEditTool(tmp, NewMutationQueue(), rt)

	// Same code, wrong indentation on the body line.
	res, err := tool.Execute(context.Background(), json.RawMessage(
		`{"path":"f.go","edits":[{"old_text":"func handleCancel() {\n\tdoWork()\n}","new_text":"func handleCancel() { return }"}]}`), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected the edit to fail")
	}
	if !strings.Contains(res.Content, "line 3") {
		t.Fatalf("error should name the nearest line, got:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "doWork()") {
		t.Fatalf("error should show the file's actual text, got:\n%s", res.Content)
	}

	// The file must be untouched.
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != content {
		t.Fatalf("a failed edit must not write; file is now:\n%s", after)
	}
}

func TestEdit_NotFoundWithNoPlausibleMatchStaysGeneric(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "f.go")
	if err := os.WriteFile(path, []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rt := NewReadTracker()
	rt.MarkRead(tmp, path)
	tool := NewEditTool(tmp, NewMutationQueue(), rt)

	res, err := tool.Execute(context.Background(), json.RawMessage(
		`{"path":"f.go","edits":[{"old_text":"nothing like this exists","new_text":"x"}]}`), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected the edit to fail")
	}
	if strings.Contains(res.Content, "nearest") {
		t.Fatalf("no plausible match should mean no nearest-match hint, got:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "old_text not found") {
		t.Fatalf("expected the generic error, got:\n%s", res.Content)
	}
}
