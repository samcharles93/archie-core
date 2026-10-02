package agentworker

import (
	"log/slog"
	"testing"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
	"github.com/samcharles93/archie-core/internal/taskrun"
	"github.com/samcharles93/archie-core/internal/tools"
)

// guardrailRunYAML is a repository-none workflow with one agent stage: a task
// run reaches AgentStage.handleResult once, and the engine that recorded that
// outcome is observable by its call count and by the decision a low threshold
// earns.
const guardrailRunYAML = "id: guardrail\nrepository: none\nsteps:\n" +
	"  - type: agent.run\n    settings:\n      mission: first\n"

// TestProductionWorkerDependenciesBuildGuardrails pins the producer: the
// process that runs agent stages is archie-agent, so its composition root must
// build the guardrail engine every TaskContext records against. A daemon-built
// engine can never reach this process -- it is a separate binary -- so before
// this wiring TaskContext.Guardrails was nil on every production run and no
// decision was ever recorded.
func TestProductionWorkerDependenciesBuildGuardrails(t *testing.T) {
	deps, err := productionWorkerDependencies()
	if err != nil {
		t.Fatalf("productionWorkerDependencies: %v", err)
	}
	if deps.guardrails == nil {
		t.Fatal("worker composition built no guardrail engine: TaskContext.Guardrails stays nil and no agent-stage decision is recorded")
	}
}

// TestRunTaskRecordsGuardrailDecision is the end-to-end half: a workflow run
// whose TaskContext carries the engine records its agent stage's outcome. Before
// the wiring the engine was never assigned, so the run recorded zero -- the
// guardrail engine observed nothing for any task.
func TestRunTaskRecordsGuardrailDecision(t *testing.T) {
	ctx := t.Context()
	st := pgstore.Open(t)

	task, err := st.EnqueueChatTask(ctx, "", "", "guardrail recording", "", "guardrail", "", nil)
	if err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if err := st.Transition(ctx, task.ID, task.Status, workflow.StatusRunning, "started"); err != nil {
		t.Fatalf("claim task: %v", err)
	}
	task.Status = workflow.StatusRunning
	task.WorkflowDefinitionYAML = guardrailRunYAML
	task.WorkflowDefinitionDigest = workflow.DigestDefinition(guardrailRunYAML)
	if err := st.Update(ctx, task); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	workDir := t.TempDir()
	// A threshold a single stage reaches turns the recorded outcome into an
	// actual guardrail decision, so the test proves the run consulted the
	// engine rather than merely holding a reference.
	engine := tools.NewGuardrailEngine(tools.ToolCallGuardrailConfig{
		NoProgressWarnAfter:     1,
		HardStopAfterWarnRepeat: 1,
	})
	request := taskrun.Request{
		Task:               task,
		Repo:               config.Repo{},
		Cfg:                config.Config{Models: map[string]string{"builder": "p/m"}}.ForTask(),
		WorkflowDefinition: guardrailRunYAML,
	}
	dependencies := taskDependencies{
		store:      st,
		trees:      &fakeScratchTrees{dir: workDir},
		steps:      testSteps(t),
		guardrails: engine,
	}
	fakeRunner := runnerFactory(func(map[string]agentexec.Provider, *slog.Logger) agentexec.Runner {
		return stubRunner{}
	})
	if _, err := runTask(ctx, request, dependencies, fakeRunner, workDir, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("runTask: %v", err)
	}

	if got := engine.TotalCalls(); got != 1 {
		t.Fatalf("guardrail engine recorded %d tool outcomes, want 1 (the agent stage): TaskContext.Guardrails is not wired to the engine the run was given", got)
	}
	if !engine.HardStopped() {
		t.Fatal("guardrail engine issued no decision for the run's agent stage: TaskContext.Guardrails is not wired to the engine the run was given")
	}
}
