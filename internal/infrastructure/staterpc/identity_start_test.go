package staterpc

import (
	"context"
	"errors"
	"strings"
	"testing"

	pb "github.com/samcharles93/archie-core/internal/contracts/state/v1"
	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
)

type auditTasks struct {
	storecontract.TaskStore
	failInsert bool
	recorded   []events.Event
}

func (*auditTasks) TaskByID(_ context.Context, id int64) (*task.Task, error) {
	return &task.Task{ID: id, Identity: "bot", Workflow: "deploy"}, nil
}

func (a *auditTasks) InsertEvent(_ context.Context, e events.Event) (int64, error) {
	if a.failInsert {
		return 0, errors.New("store down")
	}
	a.recorded = append(a.recorded, e)
	return 1, nil
}

// TestIdentityStartIsAudited pins that a run credential is only issued with
// its identity-start audit written, stamped by the server, and that a start
// whose audit fails leaves no usable credential.
func TestIdentityStartIsAudited(t *testing.T) {
	token := strings.Repeat("cd", 32)
	tests := []struct {
		name       string
		failInsert bool
	}{
		{"audited start issues the credential", false},
		{"unaudited start is undone", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tasks := &auditTasks{failInsert: tt.failInsert}
			grants := &TaskGrants{Store: credentials{}}
			s := &server{deps: Deps{Tasks: tasks, Grants: grants, RootIdentity: "root"}}
			ctx := access.WithActor(context.Background(), "archied")
			_, err := s.RegisterTaskGrant(ctx, &pb.RegisterTaskGrantRequest{TaskId: 7, Token: token, LifetimeSeconds: 60})
			live, _ := grants.taskFor(ctx, token)
			if tt.failInsert {
				if err == nil || live != 0 {
					t.Fatalf("err %v, credential live for task %d", err, live)
				}
				return
			}
			if err != nil || live != 7 || len(tasks.recorded) != 1 {
				t.Fatalf("err %v live %d events %d", err, live, len(tasks.recorded))
			}
			e := tasks.recorded[0]
			if e.Kind != events.KindIdentityStarted || e.ActorID != "archied" || e.PrincipalID != string(identity.StableID("bot")) {
				t.Fatalf("event %+v", e)
			}
		})
	}
}
