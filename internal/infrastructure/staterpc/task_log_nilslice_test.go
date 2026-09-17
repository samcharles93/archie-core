package staterpc

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/logging"
	"github.com/samcharles93/archie-core/internal/store"
)

// A decoded task-log page must never carry a nil slice for a collection field.
// Go marshals a nil slice to JSON null, and null is not an empty list: a
// client that reads the field as an array sees a different type than the same
// route serves when the collection is non-empty.
//
// This is the nil-slice class the contract-conformance audit deferred (its
// named revisit condition is a handler that can produce nil). The local reader
// already normalises both fields; the wire mapping did not, because protobuf
// decodes an empty `repeated` field to nil, so the grpc path regressed what the
// local path got right. Asserting the JSON shape rather than the slice catches
// it on both paths without depending on which one is under test.
func TestTaskLogPageCarriesNoNilCollectionFields(t *testing.T) {
	// Both collections must be exercised empty. A log with entries but no
	// structured "component" field leaves only Components empty; an attempt
	// whose log file exists but holds no lines leaves only Entries empty, and
	// that second shape is what catches an empty-slice mapping regressing on
	// the entries side.
	for _, tc := range []struct {
		name  string
		write bool
	}{
		{name: "entries present, components empty", write: true},
		{name: "entries empty, components empty", write: false},
	} {
		for _, mode := range []string{"local", "grpc"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				ctx := t.Context()
				baseDir := filepath.Join(t.TempDir(), "logs", "tasks")
				reader := logging.NewTaskRegistry(baseDir, logging.NewFeed(10), logging.TaskSinkOptions{})

				const taskID = 7
				if err := reader.Open(taskID, 0); err != nil {
					t.Fatalf("open sink: %v", err)
				}
				if tc.write {
					// No "component" field at all: Components stays empty while
					// Entries does not.
					reader.Write(ctx, taskID, logging.Entry{Level: "INFO", Message: "starting"})
				}
				if err := reader.Close(taskID); err != nil {
					t.Fatalf("close sink: %v", err)
				}

				var c taskLogContract = reader
				if mode == "grpc" {
					c = remoteTaskStore(t, store.OpenTest(t), reader)
				}

				page, err := c.TaskLog(ctx, taskID, 0, logging.Query{})
				if err != nil {
					t.Fatalf("TaskLog: %v", err)
				}
				if !page.Found {
					t.Fatal("Found = false for an attempt whose log file exists")
				}
				if tc.write && len(page.Entries) != 1 {
					t.Fatalf("entries = %d, want the line the sink wrote", len(page.Entries))
				}
				if !tc.write && len(page.Entries) != 0 {
					t.Fatalf("entries = %d, want none for a log with no lines", len(page.Entries))
				}

				raw, err := json.Marshal(page)
				if err != nil {
					t.Fatalf("marshal page: %v", err)
				}
				var decoded map[string]json.RawMessage
				if err := json.Unmarshal(raw, &decoded); err != nil {
					t.Fatalf("unmarshal page: %v", err)
				}
				for _, field := range []string{"entries", "components"} {
					if got := string(decoded[field]); got == "null" {
						t.Errorf("%s marshalled to null, want an empty list: the field's type must not depend on whether it has contents",
							field)
					}
				}
			})
		}
	}
}
