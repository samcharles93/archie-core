package taskrun

import (
	"encoding/json"
	"testing"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/domain/workintake"
)

func TestRequestJSONRoundTrip(t *testing.T) {
	req := Request{
		Task: &workflow.Task{
			ID:          1,
			Owner:       "acme",
			Repo:        "widget",
			IssueNumber: 42,
			Title:       "feat: thing",
			Status:      workflow.StatusRunning,
		},
		Repo: config.Repo{Owner: "acme", Name: "widget", Base: "main"},
		Cfg:  config.Config{DiffCapLines: new(500)}.ForTask(),
		Providers: map[string]agentexec.Provider{
			"anthropic": {Class: "anthropic", APIKeyEnv: "ANTHROPIC_API_KEY"},
		},
		WorktreeGrant: "grant",
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Request
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Task == nil || got.Task.ID != 1 || got.Task.Owner != "acme" || got.Task.IssueNumber != 42 {
		t.Fatalf("Task did not round-trip: %+v", got.Task)
	}
	if got.Repo.FullName() != "acme/widget" || got.Repo.BaseBranch() != "main" {
		t.Fatalf("Repo did not round-trip: %+v", got.Repo)
	}
	if got.Cfg.DiffCapLines != 500 {
		t.Fatalf("Cfg did not round-trip: %+v", got.Cfg)
	}
	if got.Providers["anthropic"].APIKeyEnv != "ANTHROPIC_API_KEY" {
		t.Fatalf("Providers did not round-trip: %+v", got.Providers)
	}
	if got.WorktreeGrant != "grant" {
		t.Fatalf("WorktreeGrant did not round-trip: %q", got.WorktreeGrant)
	}
}

func TestRequestValidateRequiresPositiveTaskID(t *testing.T) {
	for _, test := range []struct {
		name    string
		request Request
		wantErr bool
	}{
		{name: "missing task", wantErr: true},
		{name: "zero ID", request: Request{Task: &workflow.Task{}}, wantErr: true},
		{name: "negative ID", request: Request{Task: &workflow.Task{ID: -1}}, wantErr: true},
		{name: "missing worktree grant", request: Request{Task: &workflow.Task{ID: 1}}, wantErr: true},
		{name: "valid request", request: Request{Task: &workflow.Task{ID: 1}, WorktreeGrant: "grant", WorkflowDefinition: "id: test\nsteps: []\n"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.request.Validate()
			if (err != nil) != test.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestResponseJSONRoundTrip(t *testing.T) {
	resp := Response{
		Task:   &workflow.Task{ID: 1, Status: workflow.StatusPROpen},
		Status: "passed",
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Response
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Task == nil || got.Task.Status != workflow.StatusPROpen || got.Status != "passed" {
		t.Fatalf("Response did not round-trip: %+v", got)
	}
}

// TestRequestRoutingBindingsWireKeys pins the wire contract for the routing
// bindings. The daemon and the archie-agent worker are separate binaries, so the
// JSON keys on Request are the actual interface between them: a renamed tag
// would compile, pass every round-trip test that unmarshals into the same Go
// type, and silently fall back to built-in routing against a version-skewed
// peer. Assert the emitted keys literally.
func TestRequestRoutingBindingsWireKeys(t *testing.T) {
	data, err := json.Marshal(Request{
		Task:           &workflow.Task{ID: 1, Owner: "acme", Repo: "widget"},
		WorktreeGrant:  "grant",
		KindWorkflows:  workflow.KindWorkflows{workintake.KindBug: "custom-bug"},
		LabelWorkflows: workflow.LabelWorkflows{"security": "security-review"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"kind_workflows", "label_workflows"} {
		raw, ok := doc[key]
		if !ok {
			got := make([]string, 0, len(doc))
			for k := range doc {
				got = append(got, k)
			}
			t.Fatalf("marshalled Request has no %q key; the cross-process routing contract changed. Present keys: %v", key, got)
		}
		if string(raw) == "{}" || string(raw) == "null" {
			t.Fatalf("%q = %s, want the bindings object", key, raw)
		}
	}
}

// A task with no repository publishes nothing, so it runs without a
// worktree grant; a repository task still needs one.
func TestRequestValidateGrantFollowsRepository(t *testing.T) {
	scratch := Request{Task: &workflow.Task{ID: 1}, WorkflowDefinition: "id: w\n"}
	if err := scratch.Validate(); err != nil {
		t.Errorf("no-repository request without a grant: %v", err)
	}
	repo := Request{Task: &workflow.Task{ID: 1, Owner: "acme", Repo: "api"}, WorkflowDefinition: "id: w\n"}
	if err := repo.Validate(); err == nil {
		t.Error("repository request without a grant was accepted")
	}
}
