package workflow

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
)

func eventRegistry() StepRegistry {
	registry := BuiltinStepRegistry()
	registry[AgentRunStepName] = newAgentRunStage
	return registry
}

func TestParseDefinitionRepositoryMode(t *testing.T) {
	for _, test := range []struct{ name, yaml, want string }{
		{"none with a repo step", "id: w\nrepository: none\nsteps:\n  - type: implement.prepare\n", `"implement.prepare" needs a repository`},
		{"optional with a repo step", "id: w\nrepository: optional\nsteps:\n  - type: implement.prepare\n", "needs a repository"},
		{"agent.run without a mission", "id: w\nrepository: none\nsteps:\n  - type: agent.run\n", "settings.mission is required"},
		{"unknown input type", "id: w\ninputs:\n  ip: {type: ipv4}\nsteps:\n  - type: implement.prepare\n", `type "ipv4"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseDefinition(test.yaml, eventRegistry())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseDefinition() error = %v, want containing %q", err, test.want)
			}
		})
	}

	definition, err := ParseDefinition("id: w\nrepository: none\nprofile: net\ninputs:\n  src_ip: {type: string, required: true}\nsteps:\n  - type: agent.run\n    settings:\n      mission: investigate\n", eventRegistry())
	if err != nil {
		t.Fatalf("ParseDefinition(repo-free) = %v", err)
	}
	if definition.Profile != "net" || !definition.Inputs["src_ip"].Required {
		t.Fatalf("interface = %+v, want profile net and a required src_ip", definition.WorkflowInterface)
	}
}

// agent.run works in whatever workspace the task has, hands the agent its
// inputs as a JSON block, and completes the task with the agent's summary.
func TestAgentRunCompletesWithInputs(t *testing.T) {
	wf, err := ParseAndCompile("id: w\nrepository: none\nsteps:\n  - type: agent.run\n    settings:\n      mission: investigate the address\n      read_only: true\n", eventRegistry())
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeAgentRunner{result: agentexec.Result{Status: agentexec.StatusPassed, Summary: "benign scanner"}}
	store := &recordingStore{}
	tc := &TaskContext{
		Task:  &Task{ID: 3, Attempt: 1, Inputs: map[string]any{"src_ip": "10.0.0.9"}},
		Agent: runner, Store: store, Trees: &fakeTrees{dir: "/scratch/3"},
		Cfg: config.Config{Models: map[string]string{"builder": "p/m"}},
		Log: slog.New(slog.DiscardHandler),
	}
	Run(context.Background(), wf, tc)

	if runner.workspace != "/scratch/3" {
		t.Errorf("workspace = %q, want the task's scratch workspace", runner.workspace)
	}
	if !runner.request.ReadOnly {
		t.Error("read_only setting did not reach the agent request")
	}
	if !strings.Contains(runner.request.Mission, "investigate the address") || !strings.Contains(runner.request.Mission, `"src_ip": "10.0.0.9"`) {
		t.Errorf("mission = %q, want the mission and the inputs as JSON", runner.request.Mission)
	}
	if tc.Outcome.Status != StatusCompleted || tc.Outcome.Detail != "benign scanner" {
		t.Errorf("outcome = %+v, want completed with the summary", tc.Outcome)
	}
}
