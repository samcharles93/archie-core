package daemon

import (
	"context"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/store"
)

type workflowDefinitionsStub struct {
	collection workflow.WorkflowDefinitionCollection
	version    int64
}

func (s *workflowDefinitionsStub) WorkflowDefinitions(context.Context) (workflow.WorkflowDefinitionCollection, int64, error) {
	return s.collection, s.version, nil
}

func TestWorkflowDefinitionPinSurvivesActiveOverride(t *testing.T) {
	resources := store.OpenTest(t)
	defer resources.Close()
	task, err := resources.EnqueueChatTask(t.Context(), "acme", "widget", "custom", "", "custom", "operator")
	if err != nil {
		t.Fatal(err)
	}
	first := "id: custom\nsteps:\n  - type: bootstrap.apply\n"
	provider := &workflowDefinitionsStub{collection: workflow.WorkflowDefinitionCollection{Definitions: []workflow.WorkflowDefinitionEntry{{ID: "custom", YAML: first}}}, version: 4}
	d := &Daemon{Store: resources, WorkflowDefinitions: provider}
	if err := d.pinWorkflowDefinition(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	provider.collection.Definitions[0].YAML = "id: custom\nsteps:\n  - type: bootstrap.prepare\n"
	provider.version = 5
	if err := d.pinWorkflowDefinition(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	if task.WorkflowDefinitionYAML != first || task.WorkflowDefinitionVersion != 4 {
		t.Fatalf("pin changed after override: version=%d yaml=%q", task.WorkflowDefinitionVersion, task.WorkflowDefinitionYAML)
	}
	stored, err := resources.TaskByID(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.WorkflowDefinitionDigest != workflow.DigestDefinition(first) {
		t.Fatal("persisted workflow digest does not identify the pinned YAML")
	}
}
