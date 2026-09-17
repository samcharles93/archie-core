package staterpc

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/logging"
	"github.com/samcharles93/archie-core/internal/store"
)

// TestTaskLogContract drives the task-log read group through both adapters,
// matching the §11 conformance requirement the rest of this package already
// satisfies: the same battery runs against the local reader (a
// *logging.TaskRegistry over this process's own state directory) and against
// the remote *Client.
//
// The two answers this pins as DIFFERENT are the point of the contract. A
// process with no reader reports unavailability; an attempt with no log file
// reports found=false with no error. A caller that cannot tell those apart
// tells an operator task logging is switched off when it is not.
func TestTaskLogContract(t *testing.T) {
	for _, mode := range []string{"local", "grpc"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			baseDir := filepath.Join(t.TempDir(), "logs", "tasks")
			reader := logging.NewTaskRegistry(baseDir, logging.NewFeed(10), logging.TaskSinkOptions{})

			const taskID = 7
			if err := reader.Open(taskID, 0); err != nil {
				t.Fatalf("open sink: %v", err)
			}
			reader.Write(ctx, taskID, logging.Entry{Level: "INFO", Message: "starting", Fields: map[string]any{"component": "daemon"}})
			reader.Write(ctx, taskID, logging.Entry{Level: "ERROR", Message: "gate failed", Fields: map[string]any{"component": "gate"}})
			if err := reader.Close(taskID); err != nil {
				t.Fatalf("close sink: %v", err)
			}

			var c taskLogContract = reader
			if mode == "grpc" {
				c = remoteTaskStore(t, store.OpenTest(t), reader)
			}

			t.Run("a page carries the decoded entries and their fields", func(t *testing.T) {
				page, err := c.TaskLog(ctx, taskID, 0, logging.Query{})
				if err != nil {
					t.Fatalf("TaskLog: %v", err)
				}
				if !page.Found {
					t.Fatal("Found = false for an attempt whose log exists")
				}
				if len(page.Entries) != 2 {
					t.Fatalf("entries = %d, want 2", len(page.Entries))
				}
				if page.Entries[1].Message != "gate failed" || page.Entries[1].Level != "ERROR" {
					t.Errorf("entry = %+v, want the ERROR line the sink wrote", page.Entries[1])
				}
				if got, _ := page.Entries[0].Fields["component"].(string); got != "daemon" {
					t.Errorf("fields = %+v, want the structured component to survive the hop", page.Entries[0].Fields)
				}
				if !strings.HasSuffix(page.File, "attempt-0.jsonl") {
					t.Errorf("file = %q, want the attempt's own path", page.File)
				}
			})

			t.Run("filters and the limit cross the wire", func(t *testing.T) {
				page, err := c.TaskLog(ctx, taskID, 0, logging.Query{Levels: []string{"ERROR"}})
				if err != nil {
					t.Fatalf("TaskLog: %v", err)
				}
				if len(page.Entries) != 1 || page.Entries[0].Message != "gate failed" {
					t.Fatalf("level filter = %+v, want just the ERROR line", page.Entries)
				}
				page, err = c.TaskLog(ctx, taskID, 0, logging.Query{Component: "daemon"})
				if err != nil {
					t.Fatalf("TaskLog: %v", err)
				}
				if len(page.Entries) != 1 || page.Entries[0].Message != "starting" {
					t.Fatalf("component filter = %+v, want just the daemon line", page.Entries)
				}
				page, err = c.TaskLog(ctx, taskID, 0, logging.Query{Contains: "gate"})
				if err != nil {
					t.Fatalf("TaskLog: %v", err)
				}
				if len(page.Entries) != 1 || page.Entries[0].Message != "gate failed" {
					t.Fatalf("contains filter = %+v, want just the gate line", page.Entries)
				}
				page, err = c.TaskLog(ctx, taskID, 0, logging.Query{Limit: 1})
				if err != nil {
					t.Fatalf("TaskLog: %v", err)
				}
				if len(page.Entries) != 1 || !page.Truncated {
					t.Fatalf("limit = 1 gave %d entries (truncated %v), want one and a truncated marker", len(page.Entries), page.Truncated)
				}
			})

			t.Run("components list what the log actually contains", func(t *testing.T) {
				page, err := c.TaskLog(ctx, taskID, 0, logging.Query{})
				if err != nil {
					t.Fatalf("TaskLog: %v", err)
				}
				if len(page.Components) != 2 || page.Components[0] != "daemon" || page.Components[1] != "gate" {
					t.Fatalf("components = %v, want the sorted distinct set", page.Components)
				}
			})

			t.Run("an attempt with no log is found=false, not an error", func(t *testing.T) {
				page, err := c.TaskLog(ctx, taskID, 99, logging.Query{})
				if err != nil {
					t.Fatalf("TaskLog for a missing attempt = %v, want no error", err)
				}
				if page.Found {
					t.Error("Found = true for an attempt with no log file")
				}
				if len(page.Entries) != 0 {
					t.Errorf("entries = %+v, want none", page.Entries)
				}
			})

			t.Run("content is the file verbatim, and found is false when there is none", func(t *testing.T) {
				var buf bytes.Buffer
				found, err := c.TaskLogContent(ctx, taskID, 0, &buf)
				if err != nil {
					t.Fatalf("TaskLogContent: %v", err)
				}
				if !found {
					t.Fatal("found = false for an attempt whose log exists")
				}
				body := buf.String()
				if !strings.Contains(body, "gate failed") || !strings.Contains(body, "starting") {
					t.Fatalf("content = %q, want the raw log lines", body)
				}
				// Byte-for-byte: the contract transports the log, it does not
				// re-encode it. Every line must still parse as the package's
				// own JSONL.
				for line := range strings.Lines(strings.TrimSpace(body)) {
					if _, ok := loggingTailDecodes(t, line); !ok {
						t.Fatalf("line %q does not decode as the logging package's own format", line)
					}
				}

				buf.Reset()
				found, err = c.TaskLogContent(ctx, taskID, 99, &buf)
				if err != nil {
					t.Fatalf("TaskLogContent for a missing attempt = %v, want no error", err)
				}
				if found || buf.Len() != 0 {
					t.Errorf("found = %v with %d bytes, want false and nothing", found, buf.Len())
				}
			})
		})
	}
}

// TestTaskLogContractWithoutAReaderIsUnavailable pins the other half of the
// distinction, and the one the dashboard's misleading message came from: a
// service with no task-log reader at all must say UNAVAILABLE rather than
// answer an empty page. An empty page is indistinguishable from an attempt
// that has no log, and the dashboard rendered that as "task logging is
// optional and was not enabled for this run".
func TestTaskLogContractWithoutAReaderIsUnavailable(t *testing.T) {
	local := store.OpenTest(t)
	// remoteContract leaves Deps.TaskLogs nil, which is what a store service
	// sharing no state directory with the daemon looks like.
	c := remoteContract(t, local)
	logs, ok := c.(taskLogContract)
	if !ok {
		t.Fatalf("remote client does not carry the task-log contract (%T)", c)
	}

	if _, err := logs.TaskLog(t.Context(), 1, 0, logging.Query{}); !errors.Is(err, logging.ErrTaskLogsUnavailable) {
		t.Errorf("TaskLog without a reader = %v, want ErrTaskLogsUnavailable; an empty page here is what the dashboard read as \"logging was not enabled\"", err)
	}
	var buf bytes.Buffer
	if _, err := logs.TaskLogContent(t.Context(), 1, 0, &buf); !errors.Is(err, logging.ErrTaskLogsUnavailable) {
		t.Errorf("TaskLogContent without a reader = %v, want ErrTaskLogsUnavailable", err)
	}
}

// loggingTailDecodes reports whether one line is a record the logging package
// can decode, using that package's own reader rather than a second opinion
// about its format.
func loggingTailDecodes(t *testing.T, line string) (logging.Entry, bool) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "one.jsonl")
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatalf("write temp log: %v", err)
	}
	res, err := logging.Tail(path, logging.Query{})
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if len(res.Entries) != 1 {
		return logging.Entry{}, false
	}
	return res.Entries[0], true
}
