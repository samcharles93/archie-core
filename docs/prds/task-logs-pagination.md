# task_logs pagination and bounded retrieval -- decision

**Status:** Approved for vq92 implementation
**Date:** 2026-08-26
**Beads issue:** `archie-core-vq92`
**Compounds with:** `archie-core-p1bl` (errors serialise as `{}`)

## Decision

`task_logs` becomes a *cursor-paginated, filterable* bounded read, not a
one-shot head-of-file window. The reader in `internal/logging` gains a
second entry point (`Page`) that walks the file forward from a byte
offset; the tool schema in `internal/gateway/task_tools.go` surfaces
level, time-range and cursor parameters; the response carries an explicit
"more available" signal so a model can either keep paging or hand the
rest to `send_file` when the operator wants a complete archive.

We do NOT add an unbounded read path. A single response is still capped at
`MaxTailLines` (2000) entries. What changes is that pagination makes the
cap reachable in practice instead of silently terminal.

## Why now

`archie-core-vq92` is the right place to fix this: the chat tool is the
user-visible failure mode, but the same defect propagates to every caller
of `internal/logging.Tail` (`internal/webui/api_logs.go` and the
`chatTaskLogReaderAdapter` in `cmd/archied/main.go`). Patching the
reader and the tool together is one change in two layers, not two
changes; CLAUDE.md's "package owns its format end-to-end" rule means the
reader is the right place for cursor support regardless.

The compound with `p1bl` matters: a truncated window of uninformative
errors leaves an operator with nothing. Cursor pagination lets the model
walk the whole file when a level filter does not narrow enough, and the
response's "more available" signal means it can honestly say "I read N of
M and stopped there" instead of presenting the head as complete.

## Shape of the API

### Reader (`internal/logging`)

Add a second entry point next to `Tail`:

```go
// Page walks the log forward from cursor (a byte offset; 0 starts at
// the beginning of the scanned window), applying the same filters as
// Tail. PageResult.Cursor is the file offset just past the last line
// the walk returned; pass it back as Cursor to continue, and stop when
// MoreAvailable is false.
func Page(path string, q Query, cursor int64) (PageResult, error)
```

`PageResult` carries `Entries`, `Truncated` (the existing meaning --
older entries exist that were not examined within `maxScanBytes`),
`Cursor` (the byte offset the next page should resume from), and
`MoreAvailable` (true iff the scan saw more matching entries beyond the
returned page within the same call).

`Cursor` is a byte offset rather than an entry ID for two reasons.
First, `TaskSink` writes JSONL through `slog.NewJSONHandler` directly --
without routing through `FeedHandler` -- so the on-disk entries carry no
`id` field today, and a per-task attempt monotonic counter would require
either a sink-side change or a reader-side reconstruction that is
brittle against rotation. Second, the byte offset uniquely identifies
"the next unread position" in a sequential scan, which is exactly what
the call pattern needs. A cursor that points at or past the file's
current size is treated as end-of-stream (empty page, `MoreAvailable`
false).

The implementation is `readWindow` (reads the tail-most `maxScanBytes`
into a single in-memory buffer) plus `readLines` (clamps the cursor into
the window, drops a partial first line on the size-cap seek, then
delegates) plus `walkWindow` (walks the buffer line-by-line by exact
slice offset). Offsets are into one contiguous buffer, so they are
trivially correct -- unlike an incremental reader over the file, whose
`bufio.Reader` buffer-ahead would make its underlying file position
unusable as a cursor.

`Tail` is NOT a wrapper over `Page`: it keeps its legacy ring-buffer
semantics (newest `Limit` matches, oldest dropped, `Truncated` set on
ring overflow). They share `readWindow`/`walkWindow` but stay separate
entry points precisely so Tail's established behaviour is unchanged.

### `Query` gains two optional fields

```go
Since time.Time // inclusive lower bound on entry.Time
Until time.Time // inclusive upper bound on entry.Time
```

`Levels`, `Component`, `Contains`, `Limit` are unchanged. The existing
filter logic and tests move unchanged.

### Tool schema (`internal/gateway/task_tools.go`)

`ChatTaskLogQuery` gains three fields:

- `Level []string` (forwarded to `Query.Levels`)
- `Since time.Time` / `Until time.Time` (forwarded as `Query.Since/Until`)
- `AfterID int64` (the cursor; 0 to start at the beginning)

`ChatTaskLogResult` gains two fields:

- `Cursor int64` -- highest ID in this page; pass back as `AfterID` to continue
- `MoreAvailable bool` -- true iff the scan saw more matching entries that
  did not fit in this page

The tool description is updated to teach the model the pagination
protocol in one sentence: "pass the previous `cursor` back as `after_id`
until `more_available` is false; for an entire archive, prefer
`send_file`."

### Production adapter (`cmd/archied/main.go`)

`chatTaskLogReaderAdapter` passes the new fields through to the reader.
`TaskRegistry.Path` is already attempt-scoped, so paging stays inside
one attempt.

## What we deliberately do NOT do

- **No time-range-only read without a cursor.** Time bounds are a filter,
  not a window. A model that wants only "errors in the last hour" passes
  both `Levels=[ERROR]` and `Since=now-1h`. The page is still the same
  size; the response's `MoreAvailable` tells the caller whether the hour
  fit.
- **No "save the full log" via the tool.** That is what
  `archie-core-absb`'s `send_file` is for. The PRD description for vq92
  raised this as an open question; the answer is "use the existing tool"
  rather than building a parallel archive endpoint.
- **No reversal or random access.** Paging is forward-only on the byte
  offset. Reverse scan would require either an index or a full file
  pass; neither earns its cost given `Tail` already handles "show me
  the end."
- **No bumping `MaxTailLines` past 2000.** A single response must not be
  able to exhaust the model's context. Paging makes the cap *reachable*
  but does not relax it.
- **No raising `maxScanBytes`, and no walking rotated generations.** See
  the ceiling below; lifting it is a separate decision with its own cost.

## The scan-window ceiling, and what it means for "complete"

`maxScanBytes` (8 MiB) bounds the tail of the file any single call will
examine. Paging does **not** lift that bound: every call re-derives the
window from the file's *current* size and clamps the incoming cursor up
into it, so a paging loop walks the window exhaustively and then stops.
For a log larger than 8 MiB the oldest bytes are unreachable through
`task_logs` at any cursor, and rotated generations (`attempt-N.jsonl.1`,
`.2`, ...) are separate files the reader never opens.

This is a deliberate limit, not an oversight, and it is why criterion 1
below says "paging **or** file delivery" rather than "paging". The
honest contract is:

- Within the window: paging reaches every matching entry, in order,
  exactly once.
- Beyond it: `Truncated` is true, and that is the *only* correct thing
  to report. An agent that pages to `more_available == false` while
  `truncated` is true has read the window, not the log, and must say so.

Lifting the ceiling would mean either buffering an unbounded file or
teaching the reader to walk rotation history. Neither is needed to close
vq92 — the issue's own text accepts file delivery as the answer for
"save the full logs" — so both are deliberately out of scope here.

## Acceptance criteria

The issue's criteria, restated against the new shape:

1. An operator can obtain a task's complete logs through the agent: by
   paging when the log fits the scan window, and by file delivery
   (`send_file`) when it does not.
2. A bounded response says so explicitly -- `MoreAvailable` for "more
   matches this call did not return", `Truncated` for "the scan cap hid
   older entries" -- so the agent can report honestly instead of
   implying completeness.
3. `Levels`, `Since`, `Until` filters exist so a bounded call can answer
   a specific question without paging.
4. The cap stays enforced: one response cannot exceed `MaxTailLines`,
   and one call cannot examine more than `maxScanBytes`.
5. Tests cover a log larger than `MaxTailLines` and assert that repeated
   paged calls retrieve every entry without gaps or duplication, plus a
   log larger than `maxScanBytes` asserting the window is walked
   contiguously and `Truncated` is set.

## Files this change touches

- `internal/logging/reader.go` -- add `Page`, expand `Query` with `Since/Until`
- `internal/logging/reader_test.go` -- new tests for `Page`, `Since/Until`, paging without gaps
- `internal/gateway/task_tools.go` -- new `ChatTaskLogQuery` and `ChatTaskLogResult` fields, schema description
- `internal/gateway/task_tools_test.go` -- new tests for the new fields on the tool
- `cmd/archied/main.go` -- adapter passes through the new fields

The webui handler in `internal/webui/api_logs.go` is untouched: it is
already filter-aware (Levels, Component, Contains, Limit) and is not the
failing surface vq92 reports. If the dashboard later wants the same
pagination, it consumes the same reader.
