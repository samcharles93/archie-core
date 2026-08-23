package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCapPayloadPerResultOnly(t *testing.T) {
	payload := strings.Repeat("x", 500)
	tests := []struct {
		name     string
		limit    int
		spillDir func(*testing.T) string
		want     string
	}{
		{name: "disabled", limit: 0, want: payload},
		{name: "inline truncation", limit: 20, want: "result truncated"},
		{name: "spill", limit: 20, spillDir: func(t *testing.T) string { return t.TempDir() }, want: "result spilled"},
		{name: "failed spill falls back", limit: 20, spillDir: func(t *testing.T) string {
			f, err := os.CreateTemp(t.TempDir(), "blocker")
			if err != nil {
				t.Fatal(err)
			}
			_ = f.Close()
			return filepath.Join(f.Name(), "child")
		}, want: "result truncated"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := ""
			if tc.spillDir != nil {
				dir = tc.spillDir(t)
			}
			got := CapPayload("read", payload, tc.limit, dir)
			if tc.want == payload {
				if got != payload {
					t.Fatalf("got capped payload with disabled limit: %q", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) || strings.Contains(got, strings.Repeat("x", 100)) {
				t.Fatalf("CapPayload() = %q, want compact %q notice", got, tc.want)
			}
		})
	}
}

func TestCapPayloadNeverSplitsUTF8(t *testing.T) {
	payload := strings.Repeat("é", 100)
	for limit := 1; limit <= 20; limit++ {
		if got := CapPayload("read", payload, limit, ""); !utf8.ValidString(got) {
			t.Errorf("limit=%d returned invalid UTF-8: %q", limit, got)
		}
	}
}

func TestCapPayloadSpillIsPrivateAndUnique(t *testing.T) {
	dir := t.TempDir()
	first := CapPayload("../../read", strings.Repeat("a", 100), 10, dir)
	second := CapPayload("../../read", strings.Repeat("b", 100), 10, dir)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("spill entries = %v, err = %v", entries, err)
	}
	if entries[0].Name() == entries[1].Name() {
		t.Fatal("spill paths collided")
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 || strings.Contains(entry.Name(), "..") {
			t.Fatalf("unsafe spill entry %q mode %o", entry.Name(), info.Mode().Perm())
		}
	}
	if !strings.Contains(first, dir) || !strings.Contains(second, dir) {
		t.Fatalf("spill references do not name their paths: %q / %q", first, second)
	}
}
