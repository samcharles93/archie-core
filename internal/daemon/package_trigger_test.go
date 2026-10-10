package daemon

import (
	"context"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/storepkg"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

// TestCheckPackageTrigger pins that a package workflow starts only by a
// trigger accepted for its package, failing closed when none is accepted or
// the package is unknown, while operator workflows are unbounded.
func TestCheckPackageTrigger(t *testing.T) {
	d := &Daemon{PackageAuthorities: acceptedTools{
		"triage":   storepkg.Authority{Triggers: []string{"label:triage", "binding:b1", "call"}},
		"chatty":   storepkg.Authority{Triggers: []string{"source:chat"}},
		"unsigned": storepkg.Authority{},
	}}
	tests := []struct {
		name    string
		task    workflow.Task
		wantErr bool
	}{
		{"operator workflow", workflow.Task{WorkflowDefinitionYAML: "id: a\n"}, false},
		{"accepted label", workflow.Task{WorkflowDefinitionYAML: "package: triage\nid: a\n", Labels: "bug, triage"}, false},
		{"accepted binding", workflow.Task{WorkflowDefinitionYAML: "package: triage\nid: a\n", BindingID: "b1"}, false},
		{"accepted call", workflow.Task{WorkflowDefinitionYAML: "package: triage\nid: a\n", CallParentTaskID: 7}, false},
		{"accepted source", workflow.Task{WorkflowDefinitionYAML: "package: chatty\nid: a\n", Source: "chat"}, false},
		{"unaccepted label", workflow.Task{WorkflowDefinitionYAML: "package: triage\nid: a\n", Labels: "bug"}, true},
		{"unaccepted binding", workflow.Task{WorkflowDefinitionYAML: "package: triage\nid: a\n", BindingID: "b2"}, true},
		{"forge source not accepted", workflow.Task{WorkflowDefinitionYAML: "package: chatty\nid: a\n"}, true},
		{"nothing accepted", workflow.Task{WorkflowDefinitionYAML: "package: unsigned\nid: a\n", Labels: "triage"}, true},
		{"package that is gone", workflow.Task{WorkflowDefinitionYAML: "package: gone\nid: a\n", Labels: "triage"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := d.checkPackageTrigger(context.Background(), &tt.task)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
