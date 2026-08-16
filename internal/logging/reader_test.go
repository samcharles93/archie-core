package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeLog(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "archied.log")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func line(level, msg string, extra ...string) string {
	var s strings.Builder
	fmt.Fprintf(&s, `{"time":"2026-08-04T01:00:00Z","level":%q,"msg":%q`, level, msg)
	for _, e := range extra {
		s.WriteString("," + e)
	}
	return s.String() + "}"
}

func TestTailFilters(t *testing.T) {
	path := writeLog(
		t,
		line("INFO", "started", `"component":"daemon"`),
		line("WARN", "telegram rate limited", `"component":"gateway-telegram"`),
		line("ERROR", "pull failed", `"component":"container"`),
		line("DEBUG", "noisy detail", `"component":"daemon"`),
	)

	tests := []struct {
		name  string
		query Query
		want  []string
	}{
		{"no filter returns everything", Query{}, []string{"started", "telegram rate limited", "pull failed", "noisy detail"}},
		{"single level", Query{Levels: []string{"ERROR"}}, []string{"pull failed"}},
		{"several levels", Query{Levels: []string{"WARN", "ERROR"}}, []string{"telegram rate limited", "pull failed"}},
		{"level match is case-insensitive", Query{Levels: []string{"error"}}, []string{"pull failed"}},
		{"component", Query{Component: "daemon"}, []string{"started", "noisy detail"}},
		{"text matches the message", Query{Contains: "rate"}, []string{"telegram rate limited"}},
		{"text matches a field value", Query{Contains: "container"}, []string{"pull failed"}},
		{"text is case-insensitive", Query{Contains: "TELEGRAM"}, []string{"telegram rate limited"}},
		{"filters combine", Query{Levels: []string{"DEBUG"}, Component: "daemon"}, []string{"noisy detail"}},
		{"no match is empty, not an error", Query{Contains: "nothing here"}, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Tail(path, tc.query)
			if err != nil {
				t.Fatalf("Tail: %v", err)
			}
			got := make([]string, 0, len(res.Entries))
			for _, e := range res.Entries {
				got = append(got, e.Message)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("got %v, want %v", got, tc.want)
					break
				}
			}
		})
	}
}

// TestTailOrderIsOldestFirst pins the ordering the UI depends on: history is
// rendered then live lines are appended, so history must not arrive reversed.
func TestTailOrderIsOldestFirst(t *testing.T) {
	path := writeLog(t, line("INFO", "first"), line("INFO", "second"), line("INFO", "third"))
	res, err := Tail(path, Query{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"first", "second", "third"}
	for i, e := range res.Entries {
		if e.Message != want[i] {
			t.Fatalf("entry %d = %q, want %q", i, e.Message, want[i])
		}
	}
}

// TestTailKeepsNewestWhenLimited pins that the limit drops the OLDEST entries.
// Truncating from the other end would show a stale window and hide whatever
// just went wrong, which is the reason anyone opens this view.
func TestTailKeepsNewestWhenLimited(t *testing.T) {
	lines := make([]string, 0, 10)
	for i := range 10 {
		lines = append(lines, line("INFO", fmt.Sprintf("m%d", i)))
	}
	res, err := Tail(writeLog(t, lines...), Query{Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(res.Entries))
	}
	for i, want := range []string{"m7", "m8", "m9"} {
		if res.Entries[i].Message != want {
			t.Errorf("entry %d = %q, want %q", i, res.Entries[i].Message, want)
		}
	}
	if !res.Truncated {
		t.Error("Truncated = false, want true when older entries were dropped")
	}
}

func TestTailLimitIsClamped(t *testing.T) {
	res, err := Tail(writeLog(t, line("INFO", "one")), Query{Limit: MaxTailLines * 10})
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if len(res.Entries) != 1 {
		t.Errorf("got %d entries, want 1", len(res.Entries))
	}
}

// TestTailTolerates pins that non-JSON lines and a missing file are survivable:
// a log may carry subprocess output, and file logging is optional.
func TestTailTolerates(t *testing.T) {
	t.Run("non-JSON lines are skipped", func(t *testing.T) {
		path := writeLog(t, "not json at all", line("INFO", "real entry"), "}{")
		res, err := Tail(path, Query{})
		if err != nil {
			t.Fatalf("Tail: %v", err)
		}
		if len(res.Entries) != 1 || res.Entries[0].Message != "real entry" {
			t.Errorf("got %+v, want just the parseable entry", res.Entries)
		}
	})

	t.Run("missing file is empty, not an error", func(t *testing.T) {
		res, err := Tail(filepath.Join(t.TempDir(), "absent.log"), Query{})
		if err != nil {
			t.Errorf("Tail on a missing file: %v, want nil", err)
		}
		if len(res.Entries) != 0 {
			t.Errorf("got %d entries, want 0", len(res.Entries))
		}
	})

	t.Run("empty path is empty, not an error", func(t *testing.T) {
		if _, err := Tail("", Query{}); err != nil {
			t.Errorf("Tail(\"\"): %v, want nil", err)
		}
	})
}

func TestDecodeKeepsStructuredFields(t *testing.T) {
	path := writeLog(t, line("ERROR", "pull failed", `"component":"container"`, `"err":"unauthorized"`))
	res, err := Tail(path, Query{})
	if err != nil {
		t.Fatal(err)
	}
	e := res.Entries[0]
	if e.Level != "ERROR" || e.Message != "pull failed" {
		t.Fatalf("got level=%q msg=%q", e.Level, e.Message)
	}
	if e.Fields["component"] != "container" || e.Fields["err"] != "unauthorized" {
		t.Errorf("fields = %v, want component and err preserved", e.Fields)
	}
	if e.Time.IsZero() {
		t.Error("Time is zero; the timestamp was not parsed")
	}
}

func TestComponents(t *testing.T) {
	path := writeLog(
		t,
		line("INFO", "a", `"component":"daemon"`),
		line("INFO", "b", `"component":"gateway-telegram"`),
		line("INFO", "c", `"component":"daemon"`),
		line("INFO", "d"),
	)
	got, err := Components(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"daemon", "gateway-telegram"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("got %v, want %v (sorted, deduplicated)", got, want)
		}
	}
}
