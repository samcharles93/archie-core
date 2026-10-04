package daemon

import (
	"context"
	"log/slog"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

type identityStore struct {
	storecontract.TaskStore
	parked bool
}

func (*identityStore) Update(context.Context, *workflow.Task) error { return nil }

func (s *identityStore) ParkTask(context.Context, int64, string, string, taskstate.ParkClass) error {
	s.parked = true
	return nil
}

type principalOf struct{}

func (principalOf) PrincipalFor(_ context.Context, id identity.IdentityID) (access.Principal, error) {
	return access.Principal{IdentityID: id}, nil
}

// runAllowed permits run only to the identities it lists.
type runAllowed map[identity.IdentityID]bool

func (r runAllowed) Authorize(p access.Principal, _ access.Action, _ access.Resource, _ access.Context) access.Decision {
	if r[p.IdentityID] {
		return access.Allowed()
	}
	return access.DeniedAt(access.LevelOrg, nil)
}

func (runAllowed) Reload(context.Context) error { return nil }

// TestWorkflowIdentity pins that a workflow naming an identity runs as it
// only when the dispatcher may run the workflow, and that a workflow naming
// none runs as the dispatcher.
func TestWorkflowIdentity(t *testing.T) {
	const named = "id: deploy\nidentity: deployer\n"
	const plain = "id: deploy\n"
	tests := []struct {
		name         string
		yaml         string
		dispatcher   string
		allowed      runAllowed
		wantIdentity string
		wantParked   bool
	}{
		{"allowed dispatcher runs as the workflow's identity", named, "", runAllowed{"root": true}, "deployer", false},
		{"refused dispatcher is parked", named, "", runAllowed{}, "", true},
		{"unconfigured identity is parked", "id: deploy\nidentity: ghost\n", "", runAllowed{"root": true}, "", true},
		{"no named identity runs as the dispatcher", plain, "bot", runAllowed{}, "bot", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &identityStore{}
			d := &Daemon{
				Store: store, Log: slog.New(slog.DiscardHandler), RootIdentityID: "root",
				Access: tt.allowed, Principals: principalOf{},
				Identities: []*IdentityRunner{{ID: "id-deployer", Name: "deployer"}, {ID: "id-bot", Name: "bot"}},
			}
			task := &workflow.Task{
				ID: 1, Identity: tt.dispatcher, Workflow: "deploy",
				WorkflowDefinitionYAML: tt.yaml, WorkflowDefinitionDigest: workflow.DigestDefinition(tt.yaml),
			}
			ok := d.adoptWorkflowIdentity(context.Background(), task)
			if ok == tt.wantParked || store.parked != tt.wantParked || (ok && task.Identity != tt.wantIdentity) {
				t.Fatalf("ok %v parked %v identity %q, want parked %v identity %q", ok, store.parked, task.Identity, tt.wantParked, tt.wantIdentity)
			}
		})
	}
}
