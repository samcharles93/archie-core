package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDefaultTurnBudgetChars(t *testing.T) {
	if DefaultTurnBudgetChars != 200_000 {
		t.Errorf("DefaultTurnBudgetChars = %d, want 200000", DefaultTurnBudgetChars)
	}
}

func TestTurnBudgetConsume(t *testing.T) {
	b := NewTurnBudget(1000, "")

	// Consume within budget.
	if !b.Consume(500) {
		t.Error("expected consume to succeed")
	}
	if b.Used() != 500 {
		t.Errorf("Used = %d, want 500", b.Used())
	}
	if b.Remaining() != 500 {
		t.Errorf("Remaining = %d, want 500", b.Remaining())
	}
	if b.Exceeded() {
		t.Error("should not be exceeded")
	}

	// Consume more within budget.
	if !b.Consume(400) {
		t.Error("expected consume to succeed")
	}

	// This exceeds the budget.
	if b.Consume(200) {
		t.Error("expected consume to fail")
	}
	if !b.Exceeded() {
		t.Error("should be exceeded")
	}
	if b.Remaining() != 0 {
		t.Errorf("Remaining = %d, want 0", b.Remaining())
	}

	// Subsequent consumes always fail.
	if b.Consume(1) {
		t.Error("consume should fail after exceeded")
	}
}

func TestTurnBudgetExactBoundary(t *testing.T) {
	// At exactly MaxChars, should succeed. Exceeded only above MaxChars.
	b := NewTurnBudget(100, "")
	if !b.Consume(99) {
		t.Error("99/100 should succeed")
	}
	if !b.Consume(1) {
		t.Error("100/100 should succeed (exactly at limit)")
	}
	if b.Exceeded() {
		t.Error("should not be exceeded at exactly the limit")
	}
	// One more char triggers exceeded.
	if b.Consume(1) {
		t.Error("101/100 should be exceeded")
	}
	if !b.Exceeded() {
		t.Error("should be exceeded above the limit")
	}
}

func TestTurnBudgetSpillToDisk(t *testing.T) {
	dir := t.TempDir()
	b := NewTurnBudget(100, dir)

	// Spill some content.
	content := []byte(strings.Repeat("x", 200))
	ref := b.Spill("my-tool", content)

	if ref.SizeChars != 200 {
		t.Errorf("SizeChars = %d, want 200", ref.SizeChars)
	}
	if ref.Path == "" {
		t.Error("spill path should not be empty")
	}
	if !strings.HasPrefix(ref.Path, dir) {
		t.Errorf("spill path %q should be under %q", ref.Path, dir)
	}

	// Verify file exists and has correct content.
	got, err := os.ReadFile(ref.Path)
	if err != nil {
		t.Fatalf("read spill file: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("spill content mismatch: got %q, want %q", string(got), string(content))
	}

	// Budget should be exceeded after spill.
	if !b.Exceeded() {
		t.Error("budget should be exceeded after spilling 200 chars into 100 budget")
	}
}

func TestTurnBudgetSpillNoDir(t *testing.T) {
	b := NewTurnBudget(100, "")

	ref := b.Spill("tool", []byte("hello"))
	if ref.Path != "" {
		t.Error("spill path should be empty when spillDir is empty")
	}
	if ref.SizeChars != 5 {
		t.Errorf("SizeChars = %d, want 5", ref.SizeChars)
	}
}

func TestTurnBudgetSpilled(t *testing.T) {
	b := NewTurnBudget(1000, "")

	b.Spill("a", []byte("aa"))
	b.Spill("b", []byte("bb"))

	spilled := b.Spilled()
	if len(spilled) != 2 {
		t.Errorf("expected 2 spills, got %d", len(spilled))
	}
	if spilled[0].SizeChars != 2 {
		t.Errorf("first spill size = %d, want 2", spilled[0].SizeChars)
	}
	if spilled[1].SizeChars != 2 {
		t.Errorf("second spill size = %d, want 2", spilled[1].SizeChars)
	}
}

func TestTurnBudgetReset(t *testing.T) {
	b := NewTurnBudget(100, "")
	b.Consume(99)
	b.Spill("tool", []byte("overflow"))

	if !b.Exceeded() {
		t.Fatal("expected exceeded")
	}

	b.Reset()

	if b.Exceeded() {
		t.Error("after reset, should not be exceeded")
	}
	if b.Used() != 0 {
		t.Errorf("Used = %d, want 0", b.Used())
	}
	if b.Remaining() != 100 {
		t.Errorf("Remaining = %d, want 100", b.Remaining())
	}
	if len(b.Spilled()) != 0 {
		t.Errorf("Spilled = %d, want 0", len(b.Spilled()))
	}
}

func TestTurnBudgetConcurrent(t *testing.T) {
	b := NewTurnBudget(100_000, "")

	done := make(chan struct{})
	for range 10 {
		go func() {
			for range 100 {
				b.Consume(10)
				b.Used()
				b.Remaining()
				b.Exceeded()
			}
			done <- struct{}{}
		}()
	}

	for range 10 {
		<-done
	}

	// No panic and budget was consumed.
	if b.Used() < 1000 {
		t.Errorf("Used = %d, expected at least 1000", b.Used())
	}
}

func TestTurnBudgetSpillFileNaming(t *testing.T) {
	dir := t.TempDir()
	b := NewTurnBudget(100_000, dir)

	b.Spill("my-awesome-tool", []byte("data"))

	spilled := b.Spilled()
	if len(spilled) != 1 {
		t.Fatalf("expected 1 spill, got %d", len(spilled))
	}

	// File name should contain the tool name.
	base := filepath.Base(spilled[0].Path)
	if !strings.Contains(base, "my-awesome-tool") {
		t.Errorf("spill file name %q should contain tool name", base)
	}
}

func TestCapPayload(t *testing.T) {
	const (
		toolName = "skill_activate"
		short    = "abcdefghij" // 10 bytes
	)
	long := strings.Repeat("x", 500)
	// oversize begins with short, so a truncation at len(short) is
	// recognisable in the result rather than being an opaque prefix.
	oversize := short + long

	tests := []struct {
		name string
		// budget selects how the TurnBudget is built: "none" passes nil,
		// "spill" gives it a usable directory, "nospill" gives it none,
		// "badspill" gives it a directory that cannot be written.
		budget      string
		payload     string
		max         int
		wantPayload string   // exact result, when the payload passes through
		wantContain []string // substrings the capped result must carry
	}{
		{
			name:        "no cap when max is zero",
			budget:      "none",
			payload:     long,
			max:         0,
			wantPayload: long,
		},
		{
			name:        "no cap when max is negative",
			budget:      "none",
			payload:     long,
			max:         -1,
			wantPayload: long,
		},
		{
			name:        "under the cap passes through",
			budget:      "none",
			payload:     short,
			max:         len(short) + 1,
			wantPayload: short,
		},
		{
			name:        "exactly at the cap passes through",
			budget:      "none",
			payload:     short,
			max:         len(short),
			wantPayload: short,
		},
		{
			name:        "over the cap without a budget truncates and names the tool",
			budget:      "none",
			payload:     oversize,
			max:         len(short),
			wantContain: []string{short, toolName, "510"},
		},
		{
			name:        "over the cap with no spill directory truncates",
			budget:      "nospill",
			payload:     oversize,
			max:         len(short),
			wantContain: []string{short, toolName, "510"},
		},
		{
			name:        "over the cap with a spill directory reports the path",
			budget:      "spill",
			payload:     oversize,
			max:         len(short),
			wantContain: []string{toolName, "spill-"},
		},
		{
			name:        "spill write failure falls back to truncation",
			budget:      "badspill",
			payload:     oversize,
			max:         len(short),
			wantContain: []string{short, toolName, "510"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var budget *TurnBudget
			switch tc.budget {
			case "spill":
				budget = NewTurnBudget(1_000_000, t.TempDir())
			case "nospill":
				budget = NewTurnBudget(1_000_000, "")
			case "badspill":
				f, err := os.CreateTemp(t.TempDir(), "spill-blocker")
				if err != nil {
					t.Fatal(err)
				}
				f.Close()
				budget = NewTurnBudget(1_000_000, filepath.Join(f.Name(), "sub"))
			}

			got := CapPayload(toolName, tc.payload, tc.max, budget)

			if tc.wantPayload != "" {
				if got != tc.wantPayload {
					t.Errorf("CapPayload() = %q, want it returned unchanged", got)
				}
				return
			}
			for _, want := range tc.wantContain {
				if !strings.Contains(got, want) {
					t.Errorf("CapPayload() = %q, want it to contain %q", got, want)
				}
			}
			if len(got) >= len(tc.payload) {
				t.Errorf("CapPayload() returned %d bytes, want fewer than the %d-byte payload", len(got), len(tc.payload))
			}
		})
	}
}

// TestCapPayloadDoesNotChargeBudget pins the accounting boundary.
//
// CapPayload displaces an oversized result to disk; it must not also bill for
// it. Spill used to charge the FULL uncapped length while the caller charged
// the capped length on top, so a turn was billed for far more than it was ever
// shown and ran out of budget early.
func TestCapPayloadDoesNotChargeBudget(t *testing.T) {
	tests := []struct {
		name     string
		spillDir bool
	}{
		{name: "spilled to disk", spillDir: true},
		{name: "truncated inline", spillDir: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := ""
			if tc.spillDir {
				dir = t.TempDir()
			}
			budget := NewTurnBudget(1_000_000, dir)

			CapPayload("big", strings.Repeat("x", 5000), 100, budget)

			if used := budget.Used(); used != 0 {
				t.Errorf("Used() = %d after CapPayload, want 0: accounting belongs to the caller", used)
			}
			if budget.Exceeded() {
				t.Error("budget reports exceeded after a single capped result")
			}
		})
	}
}

// TestSpillChargesBudget is the other half: the batch-dispatch path calls Spill
// directly and relies on it charging, so that behaviour must not drift.
func TestSpillChargesBudget(t *testing.T) {
	budget := NewTurnBudget(1_000_000, t.TempDir())
	content := []byte(strings.Repeat("y", 300))

	budget.Spill("tool", content)

	if used := budget.Used(); used != len(content) {
		t.Errorf("Used() = %d, want %d", used, len(content))
	}
}

// TestWriteSpillUniquePaths guards a collision the previous naming scheme hid:
// the file name was derived from the budget's consumed count, so two spills
// that did not change it would overwrite each other.
func TestWriteSpillUniquePaths(t *testing.T) {
	budget := NewTurnBudget(1_000_000, t.TempDir())

	first := budget.WriteSpill("tool", []byte("first"))
	second := budget.WriteSpill("tool", []byte("second"))

	if first.Path == "" || second.Path == "" {
		t.Fatalf("spill paths empty: %q %q", first.Path, second.Path)
	}
	if first.Path == second.Path {
		t.Fatalf("both spills wrote to %q; the second overwrote the first", first.Path)
	}
	for _, want := range []struct {
		ref  SpillRef
		body string
	}{{first, "first"}, {second, "second"}} {
		got, err := os.ReadFile(want.ref.Path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want.body {
			t.Errorf("%s holds %q, want %q", want.ref.Path, got, want.body)
		}
	}
}

// TestWriteSpillUniqueAcrossBudgets is the collision that matters in practice.
// A TurnBudget is minted per chat turn and per agent stage while every one of
// them shares a single spill directory, so a name derived from within-budget
// state restarts each turn and the second turn silently overwrites the first --
// leaving one session holding a reference to another session's output.
func TestWriteSpillUniqueAcrossBudgets(t *testing.T) {
	dir := t.TempDir()

	firstTurn := NewTurnBudget(1_000_000, dir)
	secondTurn := NewTurnBudget(1_000_000, dir)

	a := firstTurn.WriteSpill("read", []byte("first turn output"))
	b := secondTurn.WriteSpill("read", []byte("second turn output"))

	if a.Path == "" || b.Path == "" {
		t.Fatalf("spill paths empty: %q %q", a.Path, b.Path)
	}
	if a.Path == b.Path {
		t.Fatalf("both turns wrote to %q", a.Path)
	}
	got, err := os.ReadFile(a.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "first turn output" {
		t.Errorf("first turn's spill holds %q; a later turn overwrote it", got)
	}
}

// TestWriteSpillFilePermissions checks the mode on the first place archie
// persists verbatim tool output. It can hold anything the tools could read --
// file contents, shell stdout, MCP responses -- so it must not be world- or
// group-readable.
func TestWriteSpillFilePermissions(t *testing.T) {
	budget := NewTurnBudget(1_000_000, t.TempDir())

	ref := budget.WriteSpill("shell", []byte("secret output"))
	if ref.Path == "" {
		t.Fatal("spill was not written")
	}
	info, err := os.Stat(ref.Path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("spill file mode = %04o, want 0600", perm)
	}
}

// TestWriteSpillSanitisesToolName covers a name reaching the filesystem. MCP
// tool names come from a third-party server, so a name carrying path
// separators is reachable input rather than a hypothetical.
func TestWriteSpillSanitisesToolName(t *testing.T) {
	dir := t.TempDir()
	budget := NewTurnBudget(1_000_000, dir)

	ref := budget.WriteSpill("../../etc/passwd", []byte("payload"))
	if ref.Path == "" {
		t.Fatal("spill was not written")
	}
	if filepath.Dir(ref.Path) != dir {
		t.Errorf("spill escaped to %q, want it inside %q", ref.Path, dir)
	}
}

// TestCapPayloadKeepsRunesIntact guards the truncation boundary: cutting a
// multi-byte rune in half hands the model invalid UTF-8, which some providers
// reject outright and others render as a replacement character mid-word.
func TestCapPayloadKeepsRunesIntact(t *testing.T) {
	payload := strings.Repeat("é", 100) // 2 bytes per rune

	for max := 1; max <= 20; max++ {
		got := CapPayload("read", payload, max, nil)
		if !utf8.ValidString(got) {
			t.Errorf("max=%d: CapPayload() returned invalid UTF-8: %q", max, got)
		}
	}
}

func TestTurnBudgetSpillDirPermissions(t *testing.T) {
	// Create a regular file and use a sub-path as the spill directory.
	// Even root cannot write a file when a path component is a regular
	// file, making this test reliable regardless of privileges.
	f, err := os.CreateTemp(t.TempDir(), "archie-test-spill-blocker")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	// spillDir is a sub-path of the regular file — WriteFile must fail.
	spillDir := filepath.Join(f.Name(), "sub")

	b := NewTurnBudget(100_000, spillDir)
	ref := b.Spill("tool", []byte("data"))

	// Spill should handle write failure gracefully.
	if ref.Path != "" {
		t.Errorf("spill path should be empty on write failure, got %q", ref.Path)
	}
	if ref.SizeChars != 4 {
		t.Errorf("SizeChars should still be recorded: got %d", ref.SizeChars)
	}
}
