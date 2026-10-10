package postgres_test

import (
	"context"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
)

// An org's tasks, their events and the stats built from them are invisible to
// a request scoped to another org. An event a task emits belongs to the
// task's org whatever org the writer acts in.
func TestTasksStayInTheirOrg(t *testing.T) {
	scoped := func(id org.OrgID) context.Context {
		return org.WithScope(org.WithOrg(t.Context(), id), id)
	}
	db := pgstore.Open(t)
	var taskID int64
	if err := db.Pool.QueryRow(t.Context(),
		`INSERT INTO tasks (owner, repo, issue_number, identity, workflow, status, tokens_used, org_id, workspace_id)
		 VALUES ('o', 'r', 1, 'sam', 'deploy', 'running', 10, 'acme', 'default') RETURNING id`,
	).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.InsertEvent(t.Context(), events.Event{Kind: "stage_finish", TaskID: taskID, Workflow: "deploy", Stage: "plan"}); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name string
		ctx  func() context.Context //nolint:containedctx // table input, not stored state
		sees bool
	}{
		{"the owning org", func() context.Context { return scoped("acme") }, true},
		{"another org", func() context.Context { return scoped("other") }, false},
		{"an internal service", t.Context, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := tt.ctx()
			listed, err := db.Tasks(ctx, 10)
			if err != nil {
				t.Fatal(err)
			}
			got, err := db.TaskByID(ctx, taskID)
			if err != nil {
				t.Fatal(err)
			}
			counts, err := db.StatusCounts(ctx)
			if err != nil {
				t.Fatal(err)
			}
			timeline, err := db.TaskEvents(ctx, taskID)
			if err != nil {
				t.Fatal(err)
			}
			feed, err := db.EventsSince(ctx, "", 10)
			if err != nil {
				t.Fatal(err)
			}
			workflows, err := db.WorkflowStats(ctx)
			if err != nil {
				t.Fatal(err)
			}
			stages, err := db.StageStats(ctx)
			if err != nil {
				t.Fatal(err)
			}
			seen := []bool{len(listed) == 1, got != nil, counts["running"] == 1, len(timeline) == 1, len(feed) == 1, len(workflows) == 1, len(stages) == 1}
			for i, s := range seen {
				if s != tt.sees {
					t.Fatalf("read %d visible = %v, want %v", i, s, tt.sees)
				}
			}
		})
	}
}
