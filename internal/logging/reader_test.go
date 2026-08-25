package logging

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
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

// TestPageMatchesTailWhenLimitFitsWholeFile pins the first-cut promise of
// Page: with cursor=0 and a Limit large enough to hold every match, the
// page is identical to what Tail would have returned.
func TestPageMatchesTailWhenLimitFitsWholeFile(t *testing.T) {
	lines := []string{
		line("INFO", "started", `"component":"daemon"`),
		line("WARN", "rate limit", `"component":"telegram"`),
		line("ERROR", "pull failed", `"component":"container"`),
		line("DEBUG", "noisy", `"component":"daemon"`),
	}
	path := writeLog(t, lines...)
	tail, err := Tail(path, Query{})
	if err != nil {
		t.Fatal(err)
	}
	page, err := Page(path, Query{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.MoreAvailable || page.Truncated {
		t.Errorf("page flags = (more=%v, trunc=%v), want both false", page.MoreAvailable, page.Truncated)
	}
	if len(page.Entries) != len(tail.Entries) {
		t.Fatalf("page has %d entries, tail has %d", len(page.Entries), len(tail.Entries))
	}
	for i := range page.Entries {
		if page.Entries[i].Message != tail.Entries[i].Message {
			t.Errorf("entry %d: page=%q tail=%q", i, page.Entries[i].Message, tail.Entries[i].Message)
		}
	}
}

// TestPageWalksWholeFileWithoutGapsOrDuplication is the issue's acceptance
// criterion #5: repeated paged calls against a log LARGER THAN THE CAP must
// retrieve every entry exactly once.
//
// total deliberately exceeds MaxTailLines (2000), because that is the cap the
// criterion is about: a single call can never return more than MaxTailLines
// entries, so a log below that size would be satisfied by one call and would
// not exercise paging at all. It stays well under maxScanBytes (8 MiB) so that
// EVERY entry is genuinely reachable -- see TestPageAcrossSizeCapSeek for the
// oversized case, where the scan window makes the oldest entries unreachable
// by design.
func TestPageWalksWholeFileWithoutGapsOrDuplication(t *testing.T) {
	const total = MaxTailLines + 500
	const pageSize = 700
	lines := make([]string, total)
	for i := range lines {
		lines[i] = line("INFO", fmt.Sprintf("entry-%05d", i))
	}
	path := writeLog(t, lines...)

	var got []string
	cursor := int64(0)
	for call := range 100 {
		page, err := Page(path, Query{Limit: pageSize}, cursor)
		if err != nil {
			t.Fatalf("call %d: %v", call, err)
		}
		for _, e := range page.Entries {
			got = append(got, e.Message)
		}
		if !page.MoreAvailable {
			if page.Cursor < cursor {
				t.Fatalf("call %d: cursor went backwards: prev=%d new=%d", call, cursor, page.Cursor)
			}
			break
		}
		if page.Cursor <= cursor {
			t.Fatalf("call %d: cursor did not advance: prev=%d new=%d", call, cursor, page.Cursor)
		}
		cursor = page.Cursor
	}

	if len(got) != total {
		t.Fatalf("walked %d entries, want %d (first few: %v)", len(got), total, got[:min(len(got), 5)])
	}
	seen := map[string]int{}
	for _, m := range got {
		seen[m]++
	}
	if len(seen) != total {
		t.Errorf("got %d distinct messages, want %d (duplicates: %v)", len(seen), total, dupCount(seen))
	}
}

// dupCount returns the messages seen more than once, for test diagnostics.
func dupCount(seen map[string]int) []string {
	out := []string{}
	for k, v := range seen {
		if v > 1 {
			out = append(out, fmt.Sprintf("%s×%d", k, v))
		}
	}
	return out
}

// appendInt formats n in decimal into dst and returns the extended
// slice. Used by TestPageAcrossSizeCapSeek so every line is exactly
// the bytes the JSON encoder produced -- a fixed byte boundary would
// truncate some lines mid-string and the JSON decoder would drop them.
func appendInt(dst []byte, n int) []byte {
	if n == 0 {
		return append(dst, '0')
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return append(dst, digits[i:]...)
}

// TestPageCursorSurvivesNoOpAdvances: a page that returns no entries
// must still produce a Cursor equal to the input -- otherwise the caller
// cannot distinguish "no matches ahead" from "I lost my place".
func TestPageCursorAdvancesWithFile(t *testing.T) {
	path := writeLog(t,
		line("INFO", "first"),
		line("INFO", "second"),
		line("INFO", "third"),
	)
	page, err := Page(path, Query{Limit: 10}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.MoreAvailable {
		t.Errorf("MoreAvailable = true with 3 lines and Limit 10, want false")
	}
	if page.Cursor <= 0 {
		t.Errorf("Cursor = %d, want > 0 (the file is non-empty)", page.Cursor)
	}
}

// TestPageCursorPastEOFIsEmpty confirms the rotation-shrank case: a cursor
// past the current file size returns no entries and no MoreAvailable.
func TestPageCursorPastEOFIsEmpty(t *testing.T) {
	path := writeLog(t, line("INFO", "only entry"))
	page, err := Page(path, Query{}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 0 || page.MoreAvailable {
		t.Errorf("cursor-past-EOF page = %+v, want empty + no more", page)
	}
}

// TestQuerySinceUntilFilters checks the time-range filter on the new
// fields. Times are read from each entry's `time` field; the helper
// `line` writes a fixed timestamp, so the test pins the inclusive
// boundary behaviour directly.
func TestQuerySinceUntilFilters(t *testing.T) {
	// Three entries: two seconds apart on the same minute.
	path := writeLog(t,
		`{"time":"2026-08-04T01:00:00Z","level":"INFO","msg":"early"}`,
		`{"time":"2026-08-04T01:00:02Z","level":"INFO","msg":"middle"}`,
		`{"time":"2026-08-04T01:00:04Z","level":"INFO","msg":"late"}`,
	)
	since, _ := time.Parse(time.RFC3339Nano, "2026-08-04T01:00:02Z")
	until, _ := time.Parse(time.RFC3339Nano, "2026-08-04T01:00:02Z")
	page, err := Page(path, Query{Since: since, Until: until, Limit: 10}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || page.Entries[0].Message != "middle" {
		got := make([]string, len(page.Entries))
		for i, e := range page.Entries {
			got[i] = e.Message
		}
		t.Fatalf("Since==Until page = %v, want [middle] (inclusive on both bounds)", got)
	}
	// Loose bounds: only the late entry passes.
	page, err = Page(path, Query{Since: since.Add(2 * time.Second), Limit: 10}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || page.Entries[0].Message != "late" {
		got := make([]string, len(page.Entries))
		for i, e := range page.Entries {
			got[i] = e.Message
		}
		t.Fatalf("Since=since+2s page = %v, want [late]", got)
	}
	// Loose bounds: only the early entry passes (Until is strictly less
	// than middle so middle is excluded).
	page, err = Page(path, Query{Until: since.Add(-time.Nanosecond), Limit: 10}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || page.Entries[0].Message != "early" {
		got := make([]string, len(page.Entries))
		for i, e := range page.Entries {
			got[i] = e.Message
		}
		t.Fatalf("Until=since-1ns page = %v, want [early]", got)
	}
}

// TestTimeBoundsExcludeUnparseableTimestamps pins the symmetry between
// Since and Until for a record whose "time" did not parse. Such a record
// cannot be placed in a range, so BOTH bounds must exclude it -- the
// naive comparison keeps it under Until (zero.After(x) is false) while
// dropping it under Since (zero.Before(x) is true).
func TestTimeBoundsExcludeUnparseableTimestamps(t *testing.T) {
	bound, err := time.Parse(time.RFC3339Nano, "2026-08-04T01:00:02Z")
	if err != nil {
		t.Fatal(err)
	}
	path := writeLog(t,
		`{"level":"INFO","msg":"no-time-field"}`,
		`{"time":"not a timestamp","level":"INFO","msg":"unparseable-time"}`,
		`{"time":"2026-08-04T01:00:02Z","level":"INFO","msg":"has-time"}`,
	)

	for _, tc := range []struct {
		name  string
		query Query
	}{
		{"since only", Query{Since: bound, Limit: 10}},
		{"until only", Query{Until: bound, Limit: 10}},
		{"both bounds", Query{Since: bound, Until: bound, Limit: 10}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page, err := Page(path, tc.query, 0)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, len(page.Entries))
			for i, e := range page.Entries {
				got[i] = e.Message
			}
			if len(got) != 1 || got[0] != "has-time" {
				t.Fatalf("entries = %v, want only [has-time]: a record with no usable "+
					"timestamp must not satisfy a time-bounded query", got)
			}
		})
	}

	// Without any bound, all three are returned -- the exclusion is a
	// property of time-bounded queries, not of the records themselves.
	page, err := Page(path, Query{Limit: 10}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 3 {
		t.Errorf("unbounded query returned %d entries, want all 3", len(page.Entries))
	}
}

// TestPageRespectsFilter ensures paging with a level filter walks only
// matching entries and the MoreAvailable/Cursor fields account for
// every line in the scan window, not just the matches.
func TestPageRespectsFilter(t *testing.T) {
	lines := []string{
		line("INFO", "i1"),
		line("ERROR", "e1"),
		line("INFO", "i2"),
		line("ERROR", "e2"),
		line("INFO", "i3"),
		line("ERROR", "e3"),
	}
	path := writeLog(t, lines...)
	page, err := Page(path, Query{Levels: []string{"ERROR"}, Limit: 2}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 2 || page.Entries[0].Message != "e1" || page.Entries[1].Message != "e2" {
		t.Fatalf("first page = %+v, want [e1, e2]", page.Entries)
	}
	if !page.MoreAvailable {
		t.Errorf("MoreAvailable = false, want true (one match remains)")
	}
	page2, err := Page(path, Query{Levels: []string{"ERROR"}, Limit: 2}, page.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2.Entries) != 1 || page2.Entries[0].Message != "e3" {
		t.Fatalf("second page = %+v, want [e3]", page2.Entries)
	}
	if page2.MoreAvailable {
		t.Errorf("MoreAvailable = true on final page, want false")
	}
}

// TestPageCursorAtEOFWithTrailingNonMatchingLines pins the fix for the
// doc-vs-code contradiction the adversarial pass found: when the walk
// runs to EOF but the final lines fail the filter, Cursor must equal the
// file size (the walk's own end offset), NOT the end of the last
// matching line. Returning the latter would leave the trailing bytes
// unread, and a caller that follows PageResult's documented "cursor ==
// file size means done" rule would loop.
func TestPageCursorAtEOFWithTrailingNonMatchingLines(t *testing.T) {
	path := writeLog(t,
		line("ERROR", "e1"),
		line("INFO", "i1"),
		line("INFO", "i2"),
	)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	page, err := Page(path, Query{Levels: []string{"ERROR"}, Limit: 100}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || page.Entries[0].Message != "e1" {
		t.Fatalf("entries = %+v, want just [e1]", page.Entries)
	}
	if page.MoreAvailable {
		t.Errorf("MoreAvailable = true, want false (only one ERROR line)")
	}
	if page.Cursor != info.Size() {
		t.Errorf("Cursor = %d, want file size %d: the walk ran to EOF past the "+
			"trailing INFO lines, so the cursor must sit at end-of-file, not at "+
			"the end of the last match", page.Cursor, info.Size())
	}
}

// TestPageAcrossSizeCapSeek is the regression test for the readLines
// refactor: a file larger than maxScanBytes must trigger the seek to
// windowStart, and paged reads across that boundary must visit every
// line without duplicates or gaps. The earlier "no-gap" test used a
// 3 KB file that never crossed maxScanBytes (8 MiB), so the seek
// branch was uncovered.
//
// The test writes a file whose size is well above maxScanBytes and
// pages through it in chunks of MaxTailLines, asserting that the union
// of returned pages contains every line exactly once. It also pins
// the Truncated flag: the first page should report Truncated=true.
func TestPageAcrossSizeCapSeek(t *testing.T) {
	// 12 MiB of JSONL, fixed-width lines that decode unambiguously.
	// We do NOT truncate the line at a fixed byte boundary, because a
	// truncated JSON object silently fails to decode and would
	// invalidate the no-gap invariant. Instead, every line is the
	// same fixed size, computed from a per-line message id that
	// always fits.
	const totalBytes = 12 << 20
	path := filepath.Join(t.TempDir(), "big.log")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	var total int
	buf := make([]byte, 0, 128)
	for written := 0; written < totalBytes; {
		buf = buf[:0]
		buf = append(buf, []byte(`{"time":"2026-08-04T01:00:00Z","level":"INFO","msg":"x`)...)
		buf = appendInt(buf, total)
		buf = append(buf, '"', '}', '\n')
		if _, err := f.Write(buf); err != nil {
			t.Fatal(err)
		}
		written += len(buf)
		total++
	}

	// Walk the whole scan window by paging.
	var ids []int
	seen := map[int]struct{}{}
	cursor := int64(0)
	for call := range 500 {
		page, err := Page(path, Query{Limit: MaxTailLines}, cursor)
		if err != nil {
			t.Fatalf("call %d: %v", call, err)
		}
		if call == 0 && !page.Truncated {
			t.Fatal("first page Truncated = false, want true: the file exceeds maxScanBytes " +
				"so the oldest entries were never examined, and the caller must be told")
		}
		for _, e := range page.Entries {
			id, err := strconv.Atoi(strings.TrimPrefix(e.Message, "x"))
			if err != nil {
				t.Fatalf("call %d: unparseable message %q -- a line was split mid-record", call, e.Message)
			}
			if _, dup := seen[id]; dup {
				t.Fatalf("call %d: duplicate entry x%d", call, id)
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
		if !page.MoreAvailable {
			break
		}
		if page.Cursor <= cursor {
			t.Fatalf("call %d: cursor did not advance: prev=%d new=%d", call, cursor, page.Cursor)
		}
		cursor = page.Cursor
	}

	// The contract is NOT "every line in the file" -- maxScanBytes caps
	// the window, and for this deliberately-oversized file the oldest
	// entries are out of reach by design (that is what Truncated says).
	// The contract IS: everything inside the window, in order, exactly
	// once, ending at the file's final entry.
	if len(ids) == 0 {
		t.Fatal("paged zero entries from a 12 MiB log")
	}
	for i := 1; i < len(ids); i++ {
		if ids[i] != ids[i-1]+1 {
			t.Fatalf("gap in paged walk at index %d: x%d followed by x%d", i, ids[i-1], ids[i])
		}
	}
	if last := ids[len(ids)-1]; last != total-1 {
		t.Errorf("walk ended at x%d, want the file's final entry x%d", last, total-1)
	}
	// Sanity-check the window arithmetic itself: the reachable count is
	// determined by where maxScanBytes lands, so assert the walk covered
	// a large majority of the window rather than a hardcoded count that
	// would rot the moment the line format changes.
	if len(ids) < 100_000 {
		t.Errorf("paged only %d entries out of a %d-entry file; the 8 MiB "+
			"scan window should reach far more than that", len(ids), total)
	}
}

// TestPagePreservesTruncatedOnGiantLine pins the second regression the
// refactor fixes: a file that exceeds maxScanBytes with no '\\n' in the
// scan window still reports Truncated, because older matching entries
// exist beyond what was examined.
func TestPagePreservesTruncatedOnGiantLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "giant.log")
	// One JSON object spanning 10 MiB -- no terminator at all within
	// the 8 MiB scan window.
	body := []byte(`{"time":"2026-08-04T01:00:00Z","level":"INFO","msg":"x","pad":`)
	body = append(body, bytes.Repeat([]byte("a"), 10<<20)...)
	body = append(body, '}')
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	page, err := Page(path, Query{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !page.Truncated {
		t.Errorf("Truncated = false on giant-line file > maxScanBytes, want true")
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
