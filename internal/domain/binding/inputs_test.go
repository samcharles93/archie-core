package binding

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
)

func TestCheckWorkflow(t *testing.T) {
	fields := []mapping.Field{
		{Name: "ip", Type: mapping.TypeString},
		{Name: "count", Type: mapping.TypeNumber},
		{Name: "repo", Type: mapping.TypeString},
		{Name: "blob", Type: mapping.TypeAny},
	}
	investigate := task.WorkflowInterface{
		Repository: task.RepositoryNone,
		Inputs: map[string]task.InputSpec{
			"src_ip":   {Type: "string", Required: true},
			"severity": {Type: "number"},
		},
	}
	review := task.WorkflowInterface{Repository: task.RepositoryRequired}
	tests := []struct {
		name    string
		b       Binding
		w       task.WorkflowInterface
		wantErr string
	}{
		{name: "param and constant", w: investigate, b: Binding{Inputs: map[string]InputSource{
			"src_ip": {Param: "ip"}, "severity": {Value: 3.0},
		}}},
		{name: "any parameter feeds a typed input", w: investigate, b: Binding{Inputs: map[string]InputSource{"src_ip": {Param: "blob"}}}},
		{name: "required input unassigned", w: investigate, b: Binding{}, wantErr: `requires input "src_ip"`},
		{name: "undeclared input", w: investigate, b: Binding{Inputs: map[string]InputSource{
			"src_ip": {Param: "ip"}, "port": {Value: 22.0},
		}}, wantErr: `"port" is not declared`},
		{name: "unknown parameter", w: investigate, b: Binding{Inputs: map[string]InputSource{"src_ip": {Param: "addr"}}}, wantErr: `parameter "addr", which the mapping does not have`},
		{name: "parameter of the wrong type", w: investigate, b: Binding{Inputs: map[string]InputSource{"src_ip": {Param: "count"}}}, wantErr: `"src_ip" is string, but parameter "count" is number`},
		{name: "constant of the wrong type", w: investigate, b: Binding{Inputs: map[string]InputSource{"src_ip": {Value: 4.0}}}, wantErr: "its constant is number"},
		{name: "no-repository workflow with a pin", w: investigate, b: Binding{Owner: "acme", Repo: "api", Inputs: map[string]InputSource{"src_ip": {Param: "ip"}}}, wantErr: "runs without a repository"},
		{name: "repository from a string parameter", w: review, b: Binding{RepoParam: "repo"}},
		{name: "repository parameter missing", w: review, b: Binding{RepoParam: "full_name"}, wantErr: `"full_name" is not in the mapping`},
		{name: "repository parameter not a string", w: review, b: Binding{RepoParam: "count"}, wantErr: "is number, want string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.b.CheckWorkflow(tt.w, fields)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckWorkflow() = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("CheckWorkflow() = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateInputsAndRepository(t *testing.T) {
	base := Binding{Name: "n", Matcher: Matcher{Source: "s"}, MappingID: "m", Workflow: "w"}
	both := base
	both.Inputs = map[string]InputSource{"a": {Param: "p", Value: "x"}}
	neither := base
	neither.Inputs = map[string]InputSource{"a": {}}
	pinned := base
	pinned.Owner, pinned.Repo, pinned.RepoParam = "acme", "api", "repo"
	for name, b := range map[string]Binding{"both": both, "neither": neither, "pin and parameter": pinned} {
		if err := b.Validate(); err == nil {
			t.Errorf("%s: Validate() = nil, want an error", name)
		}
	}
}

func TestResolveInputsAndRepository(t *testing.T) {
	b := Binding{RepoParam: "full", Inputs: map[string]InputSource{
		"src_ip": {Param: "ip"}, "severity": {Value: "high"}, "missing": {Param: "absent"},
	}}
	values := map[string]any{"ip": "10.0.0.1", "full": "acme/api", "n": json.Number("1")}
	got := b.ResolveInputs(values)
	if got["src_ip"] != "10.0.0.1" || got["severity"] != "high" {
		t.Errorf("ResolveInputs() = %v", got)
	}
	if _, ok := got["missing"]; ok {
		t.Error("an unresolved parameter was passed as an input")
	}
	owner, repo, ok, err := b.RepositoryFrom(values)
	if err != nil || !ok || owner != "acme" || repo != "api" {
		t.Errorf("RepositoryFrom() = %q, %q, %v, %v", owner, repo, ok, err)
	}
	for _, bad := range []string{"acme", "acme/", "/api", "a/b/c"} {
		if _, _, _, err := b.RepositoryFrom(map[string]any{"full": bad}); err == nil {
			t.Errorf("RepositoryFrom(%q) accepted a value that is not owner/name", bad)
		}
	}
}
