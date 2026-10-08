package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	workflowtask "github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

type workflowPrincipals map[identity.IdentityID]org.OrgID

func (p workflowPrincipals) PrincipalFor(_ context.Context, id identity.IdentityID) (access.Principal, error) {
	owner, ok := p[id]
	if !ok {
		return access.Principal{}, fmt.Errorf("principal %q unavailable", id)
	}
	return access.Principal{IdentityID: id, Org: owner}, nil
}

type orgBindingStore struct {
	storecontract.TaskStore
	storecontract.BindingStore
	storecontract.MappingStore
	bindings []binding.Binding
	started  map[org.OrgID]*workflow.Task
}

func (s *orgBindingStore) ListBindings(context.Context) ([]binding.Binding, error) {
	return s.bindings, nil
}

func (s *orgBindingStore) ArmedBindingsForSource(_ context.Context, source string) ([]binding.Binding, error) {
	return slices.DeleteFunc(slices.Clone(s.bindings), func(b binding.Binding) bool { return b.Matcher.Source != source }), nil
}

func (*orgBindingStore) ListUndispatchedCaptures(_ context.Context, sources []string, _ int) ([]storecontract.CapturedEvent, error) {
	var captures []storecontract.CapturedEvent
	for _, source := range sources {
		captures = append(captures, storecontract.CapturedEvent{ID: source, Source: source, EventType: source, Body: "{}", Authenticated: true})
	}
	return captures, nil
}

func (*orgBindingStore) RecordDispatch(context.Context, string, int64, string, int64, string) error {
	return nil
}
func (*orgBindingStore) SetDispatchTask(context.Context, string, string, int64) error { return nil }
func (*orgBindingStore) InsertEvent(context.Context, events.Event) (int64, error)     { return 0, nil }

func (*orgBindingStore) GetMapping(_ context.Context, id string) (*mapping.Mapping, error) {
	return &mapping.Mapping{EventTypeID: id}, nil
}

func (s *orgBindingStore) EnqueueBindingTask(ctx context.Context, owner, repo, _, _, wf, identity, bindingID string, _ int, _ map[string]any) (*workflow.Task, error) {
	run := &workflow.Task{ID: 1, Owner: owner, Repo: repo, Workflow: wf, Identity: identity, Org: org.OrgFromContext(ctx), BindingID: bindingID}
	s.started[run.Org] = run
	return run, nil
}

func TestDispatchOrgWorkflows(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("acme disabled %v", disabled), func(t *testing.T) {
			store := &orgBindingStore{started: map[org.OrgID]*workflow.Task{}, bindings: []binding.Binding{
				{ID: "root", MappingID: "root", Matcher: binding.Matcher{Source: "root"}, OrgID: org.DefaultOrgID, Workflow: "deploy", Status: binding.StatusArmed, Owner: "owner", Repo: "repo"},
				{ID: "acme", MappingID: "acme", Matcher: binding.Matcher{Source: "acme"}, OrgID: "acme", Workflow: "deploy", Status: binding.StatusArmed},
			}}
			d := &Daemon{Cfg: config.NewHolder(config.Config{}), Store: store, Log: slog.New(slog.DiscardHandler), Bindings: store, BindingDispatcher: store, BindingTaskCreator: store, Mappings: store, RootIdentityID: "root", Identities: []*IdentityRunner{{ID: "acme-bot", Name: "bot"}}, Principals: workflowPrincipals{"root": org.DefaultOrgID, "acme-bot": "acme"}, WorkflowDefinitions: orgDefinitions{
				org.DefaultOrgID: "id: deploy\nrepository: required\nsteps:\n  - type: workflow.finish\n",
				"acme":           "id: deploy\nrepository: none\nsteps:\n  - type: workflow.finish\n",
			}}
			if disabled {
				d.WorkflowEnablement = enablementOf(workflowtask.WorkflowEnablement{}.SetEnabled("acme", "deploy", false))
			}
			d.dispatchBindings(t.Context())
			root := store.started[org.DefaultOrgID]
			if root == nil || root.Repo != "repo" || root.Identity != "" {
				t.Fatalf("root dispatch %#v", root)
			}
			acme := store.started["acme"]
			if disabled {
				if acme != nil {
					t.Fatalf("disabled org dispatched %#v", acme)
				}
				return
			}
			if acme == nil || acme.Repo != "" || acme.Identity != "acme-bot" {
				t.Fatalf("org's repo-free workflow did not dispatch as its identity: %#v", acme)
			}
		})
	}
}

type orgDefinitions map[org.OrgID]string

func (d orgDefinitions) WorkflowDefinitions(ctx context.Context) (workflow.WorkflowDefinitionCollection, int64, error) {
	owner := org.DefaultOrgID
	if p, ok := access.PrincipalFromContext(ctx); ok && p.Org != "" {
		owner = p.Org
	}
	return workflow.WorkflowDefinitionCollection{Definitions: []workflow.WorkflowDefinitionEntry{{ID: "deploy", YAML: d[owner]}}}, 3, nil
}

func TestPinWorkflowInTaskOrg(t *testing.T) {
	const shared = "id: deploy\nsteps:\n  - type: workflow.finish\n"
	const custom = "id: deploy\nrepository: none\nsteps:\n  - type: workflow.finish\n"
	for _, tc := range []struct {
		name       string
		owner      org.OrgID
		principals workflowPrincipals
		want       string
		refused    bool
		contextOrg org.OrgID
	}{
		{"legacy default org", "", nil, shared, false, ""},
		{"non-default org definition", "acme", workflowPrincipals{"bot": "acme"}, custom, false, ""},
		{"another org cannot supply the definition", "acme", workflowPrincipals{"bot": org.DefaultOrgID}, "", true, ""},
		{"unresolved org is refused", "acme", nil, "", true, ""},
		{"unresolved context cannot select another org", "", nil, "", true, "acme"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &Daemon{Store: &identityStore{}, RootIdentityID: "bot", WorkflowDefinitions: orgDefinitions{org.DefaultOrgID: shared, "acme": custom}}
			if tc.principals != nil {
				d.Principals = tc.principals
			}
			task := &workflow.Task{ID: 1, Org: tc.owner, Workflow: "deploy"}
			ctx := t.Context()
			if tc.contextOrg != "" {
				ctx = org.WithOrg(access.WithPrincipal(ctx, access.Principal{IdentityID: "foreign", Org: tc.contextOrg}), tc.contextOrg)
			}
			err := d.pinWorkflowDefinition(ctx, task)
			if tc.refused {
				if err == nil || pinParkClass(err) != taskstate.ParkNeedsHuman || task.WorkflowDefinitionYAML != "" {
					t.Fatalf("cross-org pin accepted: task %#v err %v", task, err)
				}
				return
			}
			if err != nil || task.WorkflowDefinitionYAML != tc.want {
				t.Fatalf("pinned %q, want %q; err %v", task.WorkflowDefinitionYAML, tc.want, err)
			}
		})
	}
}
