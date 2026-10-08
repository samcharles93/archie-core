package daemon

import (
	"context"
	"log/slog"
	"reflect"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	workflowtask "github.com/samcharles93/archie-core/internal/domain/workflow/task"
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

type enablementOf workflowtask.WorkflowEnablement

func (e enablementOf) WorkflowEnablement(context.Context) (workflowtask.WorkflowEnablement, error) {
	return workflowtask.WorkflowEnablement(e), nil
}

// TestWorkflowIdentity pins that a workflow naming an identity runs as it
// only when the dispatcher may run the workflow, and that a workflow naming
// none runs as the dispatcher. A workflow its org disabled is parked whatever
// started it.
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
		disabled     bool
	}{
		{"allowed dispatcher runs as the workflow's identity", named, "", runAllowed{"root": true}, "deployer", false, false},
		{"refused dispatcher is parked", named, "", runAllowed{}, "", true, false},
		{"unconfigured identity is parked", "id: deploy\nidentity: ghost\n", "", runAllowed{"root": true}, "", true, false},
		{"no named identity runs as the dispatcher", plain, "bot", runAllowed{}, "bot", false, false},
		{"disabled workflow is parked", plain, "bot", runAllowed{}, "", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &identityStore{}
			d := &Daemon{
				Store: store, Log: slog.New(slog.DiscardHandler), RootIdentityID: "root",
				Access: tt.allowed, Principals: principalOf{},
				Identities: []*IdentityRunner{{ID: "id-deployer", Name: "deployer"}, {ID: "id-bot", Name: "bot"}},
			}
			if tt.disabled {
				d.WorkflowEnablement = enablementOf(workflowtask.WorkflowEnablement{}.SetEnabled("", "deploy", false))
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

func TestIdentityTasksUseLiveSettings(t *testing.T) {
	root := config.Config{Models: map[string]string{"builder": "old/model"}, Providers: map[string]config.Provider{"old": {}}, Repos: []config.Repo{{Owner: "acme", Name: "app", Base: "main"}}}
	id := config.IdentityConfig{Name: "bot", BotUser: "bot", BotEmail: "bot@example.test", Forge: config.Forge{Type: "github"}, Repos: []config.Repo{{Owner: "acme", Name: "app", Base: "stale"}}}
	d := &Daemon{Cfg: config.NewHolder(root), Identities: []*IdentityRunner{{Name: "bot", Cfg: id, Repos: id.Repos}}}
	task := &workflow.Task{Identity: "bot", Owner: "acme", Repo: "app"}
	for _, model := range []string{"old/model", "new/model"} {
		root.Models = map[string]string{"builder": model}
		root.Budgets.MaxSteps++
		root.Dispatch.Trigger = "label"
		root.Notify.Webhook = "https://example.test/notify"
		d.Cfg.Set(root)
		got := d.configFor(task)
		if !reflect.DeepEqual(got.Models, root.Models) || !reflect.DeepEqual(got.Providers, root.Providers) || got.Budgets != root.Budgets || got.Dispatch != root.Dispatch || got.Notify != root.Notify {
			t.Fatalf("identity settings replaced the current runtime settings: %+v", got)
		}
		repo, ok := d.repoFor(task)
		if !ok || repo.Base != "main" {
			t.Fatalf("repository = %+v, found %v; want current base main", repo, ok)
		}
		if got.BotUser != id.BotUser || got.Forge != id.Forge {
			t.Fatal("identity lost its forge account")
		}
	}
}
