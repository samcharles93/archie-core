package agentworker

import (
	"context"
	"log/slog"
	"testing"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	workflowtask "github.com/samcharles93/archie-core/internal/domain/workflow/task"
	agentnats "github.com/samcharles93/archie-core/internal/infrastructure/agenttransport/nats"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
	"github.com/samcharles93/archie-core/internal/taskrun"
)

// stubRunner is the agent runner that lets a test workflow end in an
// agent.run tail without an LLM: the call step under test needs no agent,
// but the workflow must still end with an outcome.
type stubRunner struct{}

func (stubRunner) Run(_ context.Context, _ string, req agentexec.Request, _ agentexec.ToolCallReporter) (agentexec.Result, error) {
	return agentexec.Result{
		Version: agentexec.ProtocolVersion, TaskID: req.TaskID, Attempt: req.Attempt,
		Stage: req.Stage, Status: agentexec.StatusPassed, Summary: "reported",
	}, nil
}

// fakeScratchTrees is the Trees for a scratch task: the workspace already
// exists (the call step needs none, the agent.run tail works in it), and
// nothing publishes.
type fakeScratchTrees struct {
	dir string
}

func (f *fakeScratchTrees) Prepare(context.Context, string, string, string, int, string, string, string) (string, string, error) {
	return f.dir, "", nil
}

func (*fakeScratchTrees) CommitAll(context.Context, string, string) (bool, error) { return false, nil }
func (*fakeScratchTrees) Push(context.Context) error                              { return nil }
func (*fakeScratchTrees) Diff(context.Context, string, string) (string, error)    { return "", nil }
func (*fakeScratchTrees) ChangedFiles(context.Context, string, string) ([]string, error) {
	return nil, nil
}

func (*fakeScratchTrees) ChangedLines(context.Context, string, string) (int, error) { return 0, nil }
func (*fakeScratchTrees) Snapshot(context.Context, string, string) error            { return nil }

// TestExecuteTaskRequestStartsWorkflowCallCallee proves the engine's
// task.Caller wiring end to end: a run of a repository-none workflow carrying
// one workflow.call step starts a real callee task row through the same
// State Store gRPC contract the production worker dials
// (docs/prds/workflow-calls.md), and the caller carries on to its tail.
func TestExecuteTaskRequestStartsWorkflowCallCallee(t *testing.T) {
	ctx := t.Context()
	st := pgstore.Open(t)

	const callerYAML = "id: caller\nrepository: none\ninputs:\n  src_ip: {type: string}\nsteps:\n  - type: workflow.call\n    settings:\n      workflow: callee\n      inputs: {src_ip: \"inputs.src_ip\"}\n      wait: false\n  - type: agent.run\n    settings:\n      mission: report\n"
	// A real caller row, claimed and running, so the store's enqueue reads
	// the same row shape production does.
	task, err := st.EnqueueChatTask(ctx, "", "", "workflow.call integration", "", "caller", "")
	if err != nil {
		t.Fatalf("seed caller task: %v", err)
	}
	if err := st.Transition(ctx, task.ID, task.Status, workflow.StatusRunning, "started"); err != nil {
		t.Fatalf("claim caller: %v", err)
	}
	task.Status = workflow.StatusRunning
	task.Inputs = map[string]any{"src_ip": "10.0.0.9"}
	task.WorkflowDefinitionYAML = callerYAML
	task.WorkflowDefinitionDigest = workflow.DigestDefinition(callerYAML)
	if err := st.Update(ctx, task); err != nil {
		t.Fatalf("seed caller task: %v", err)
	}

	natsSrv := startEmbeddedTaskRPCServer(t)
	transport, err := agentnats.Connect(ctx, agentnats.Config{
		URL:           natsSrv.ClientURL(),
		StateStoreURL: startStateStoreGRPC(t, st),
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transport.Close)

	workDir := t.TempDir()
	request := taskrun.Request{
		Task:               task,
		Repo:               config.Repo{},
		Cfg:                config.Config{Models: map[string]string{"builder": "p/m"}}.ForTask(),
		WorkflowDefinition: callerYAML,
	}
	dependencies := taskDependencies{
		store: transport.Store(rpcTimeout),
		calls: transport.Calls(rpcTimeout),
		trees: &fakeScratchTrees{dir: workDir},
		steps: testSteps(t),
	}
	fakeRunner := runnerFactory(func(map[string]agentexec.Provider, *slog.Logger) agentexec.Runner { return stubRunner{} })
	response, err := runTask(ctx, request, dependencies, fakeRunner, workDir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != workflow.StatusCompleted {
		t.Fatalf("response status = %q (park reason %q), want completed", response.Status, response.Task.ParkReason)
	}

	callee := calleeTaskOf(t, ctx, st, task.ID)
	if callee.Workflow != "callee" || callee.CallParentTaskID != task.ID || callee.CallDepth != 1 {
		t.Fatalf("callee = %+v, want workflow callee linked to task %d at depth 1", callee, task.ID)
	}
	if callee.Inputs["src_ip"] != "10.0.0.9" {
		t.Fatalf("callee inputs = %+v, want the call's resolved reference", callee.Inputs)
	}
	if callee.Org != org.OrgID("default") {
		t.Fatalf("callee org = %q, want the caller's %q", callee.Org, task.Org)
	}
}

// calleeTaskOf reads the one callee task whose call_parent_task_id is
// callerTaskID, straight off the pool the test owns.
func calleeTaskOf(t *testing.T, ctx context.Context, st *pgstore.TaskDB, callerTaskID int64) *workflow.Task {
	t.Helper()
	rows, err := st.Pool.Query(ctx,
		`SELECT id, workflow, call_parent_task_id, call_depth, org_id, inputs
		 FROM tasks WHERE call_parent_task_id = $1`, callerTaskID)
	if err != nil {
		t.Fatalf("callee query: %v", err)
	}
	defer rows.Close()
	var callees []*workflow.Task
	for rows.Next() {
		var id, parent, depth int64
		var wf, orgID, inputs string
		if err := rows.Scan(&id, &wf, &parent, &depth, &orgID, &inputs); err != nil {
			t.Fatalf("callee scan: %v", err)
		}
		decoded, _ := workflowtask.DecodeInputs(inputs)
		callees = append(callees, &workflow.Task{
			ID: id, Workflow: wf, CallParentTaskID: parent, CallDepth: int(depth),
			Org: org.OrgID(orgID), Inputs: decoded,
		})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("callee rows: %v", err)
	}
	if len(callees) != 1 {
		t.Fatalf("callee tasks of %d = %+v, want exactly one", callerTaskID, callees)
	}
	return callees[0]
}
