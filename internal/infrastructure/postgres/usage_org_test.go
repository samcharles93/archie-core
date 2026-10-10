package postgres_test

import (
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/usage"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
)

// A task's usage is billed to the task's org whatever org the caller names;
// usage with no task keeps the caller's org.
func TestRecordUsageBillsTheTaskOrg(t *testing.T) {
	ctx := t.Context()
	db := pgstore.Open(t)
	var taskID int64
	if err := db.Pool.QueryRow(ctx,
		`INSERT INTO tasks (owner, repo, issue_number, identity, org_id, workspace_id) VALUES ('o', 'r', 1, 'sam', 'acme', 'default') RETURNING id`,
	).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name   string
		record usage.Record
		want   string
	}{
		{"a task bills its own org", usage.Record{Org: "other", Source: usage.SourceTask, TaskID: taskID}, "acme"},
		{"chat keeps the caller's org", usage.Record{Org: "org-sys", Source: usage.SourceChat}, "org-sys"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tt.record.Step, tt.record.At = tt.name, time.Now()
			tt.record = tt.record.WithRef("openai/gpt-6-luna")
			if err := db.RecordUsage(ctx, tt.record); err != nil {
				t.Fatal(err)
			}
			var got string
			if err := db.Pool.QueryRow(ctx, "SELECT org_id FROM model_usage WHERE step = $1", tt.name).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("org = %q, want %q", got, tt.want)
			}
		})
	}
}
