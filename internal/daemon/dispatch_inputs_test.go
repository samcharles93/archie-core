package daemon

import (
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

const (
	investigateYAML = "id: investigate\nrepository: none\ninputs:\n  src_ip: {type: string, required: true}\n  severity: {type: string}\nsteps:\n  - type: agent.run\n    settings:\n      mission: look\n"
	reviewYAML      = "id: review\nrepository: required\nsteps:\n  - type: implement.prepare\n"
)

func eventDefinitions() *workflowDefinitionsStub {
	return &workflowDefinitionsStub{collection: workflow.WorkflowDefinitionCollection{Definitions: []workflow.WorkflowDefinitionEntry{
		{ID: "investigate", YAML: investigateYAML},
		{ID: "review", YAML: reviewYAML},
	}}, version: 1}
}

// armBinding saves and approves b on source, returning its id.
func armBinding(t *testing.T, s *dispatchStores, b binding.Binding) string {
	t.Helper()
	id, err := s.EdaStore.InsertBinding(t.Context(), b)
	if err != nil {
		t.Fatalf("InsertBinding: %v", err)
	}
	if err := s.EdaStore.ApproveBinding(t.Context(), id); err != nil {
		t.Fatalf("ApproveBinding: %v", err)
	}
	return id
}

func dispatchFailures(t *testing.T, s *dispatchStores) []string {
	t.Helper()
	events, err := s.EventsSince(t.Context(), "", 100)
	if err != nil {
		t.Fatalf("EventsSince: %v", err)
	}
	var out []string
	for _, e := range events {
		if e.Kind == "binding_dispatch_failure" {
			out = append(out, e.Detail)
		}
	}
	return out
}

func TestDispatchBindingTargetsTheWorkflowInterface(t *testing.T) {
	fields := []mapping.Field{
		{Name: "ip", Path: "ip", Type: mapping.TypeString},
		{Name: "sev", Path: "sev", Type: mapping.TypeString},
		{Name: "full", Path: "repository.full_name", Type: mapping.TypeString},
	}
	tests := []struct {
		name        string
		binding     binding.Binding
		body        string
		wantOwner   string
		wantRepo    string
		wantInputs  map[string]any
		wantFailure string
	}{
		{
			name: "no-repository workflow gets structured inputs and no repo",
			binding: binding.Binding{Workflow: "investigate", Inputs: map[string]binding.InputSource{
				"src_ip": {Param: "ip"}, "severity": {Value: "high"},
			}},
			body:       `{"ip":"10.0.0.9"}`,
			wantInputs: map[string]any{"src_ip": "10.0.0.9", "severity": "high"},
		},
		{
			name:        "a required input that did not resolve is refused",
			binding:     binding.Binding{Workflow: "investigate", Inputs: map[string]binding.InputSource{"src_ip": {Param: "ip"}}},
			body:        `{"sev":"low"}`,
			wantFailure: `input "src_ip" is required`,
		},
		{
			name:      "repository from the payload",
			binding:   binding.Binding{Workflow: "review", RepoParam: "full"},
			body:      `{"repository":{"full_name":"acme/api"}}`,
			wantOwner: "acme", wantRepo: "api",
		},
		{
			name:        "repository from the payload must be configured",
			binding:     binding.Binding{Workflow: "review", RepoParam: "full"},
			body:        `{"repository":{"full_name":"evil/elsewhere"}}`,
			wantFailure: "evil/elsewhere from the event is not a configured repository",
		},
		{
			name:        "undefined workflow is refused",
			binding:     binding.Binding{Workflow: "gone"},
			body:        `{}`,
			wantFailure: `workflow "gone" is not defined`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := openDispatchTestStore(t)
			mappingID := seedMapping(t, s, "fw", fields...)
			b := tt.binding
			b.Name, b.Matcher, b.MappingID = "b", binding.Matcher{Source: "fw"}, mappingID
			armBinding(t, s, b)
			seedCapture(t, s, "fw", true, tt.body)

			d := newDispatchDaemonWithRepos(t, s, []config.Repo{{Owner: "acme", Name: "api"}, {Owner: "acme", Name: "web"}})
			d.WorkflowDefinitions = eventDefinitions()
			d.dispatchBindings(t.Context())

			tasks, err := s.Tasks(t.Context(), 10)
			if err != nil {
				t.Fatalf("Tasks: %v", err)
			}
			failures := dispatchFailures(t, s)
			if tt.wantFailure != "" {
				if len(tasks) != 0 {
					t.Fatalf("dispatched %d task(s), want none", len(tasks))
				}
				if len(failures) == 0 || !strings.Contains(failures[0], tt.wantFailure) {
					t.Fatalf("failures = %q, want one containing %q", failures, tt.wantFailure)
				}
				return
			}
			if len(tasks) != 1 {
				t.Fatalf("dispatched %d task(s), want 1 (failures %q)", len(tasks), failures)
			}
			got, err := s.TaskByID(t.Context(), tasks[0].ID)
			if err != nil {
				t.Fatalf("TaskByID: %v", err)
			}
			if got.Owner != tt.wantOwner || got.Repo != tt.wantRepo {
				t.Errorf("task repo = %q/%q, want %q/%q", got.Owner, got.Repo, tt.wantOwner, tt.wantRepo)
			}
			if len(got.Inputs) != len(tt.wantInputs) {
				t.Fatalf("inputs = %v, want %v", got.Inputs, tt.wantInputs)
			}
			for k, v := range tt.wantInputs {
				if got.Inputs[k] != v {
					t.Errorf("input %s = %v, want %v", k, got.Inputs[k], v)
				}
			}
			if len(tt.wantInputs) > 0 && strings.Contains(got.Body, "10.0.0.9") {
				t.Errorf("body %q repeats the inputs as text", got.Body)
			}
		})
	}
}
