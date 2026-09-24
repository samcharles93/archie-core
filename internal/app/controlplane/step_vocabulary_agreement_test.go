// The workflow step vocabulary is a contract between processes: the validating
// side (the State Store's control plane) admits a definition and the executing
// side (archie-agent) compiles it. Two packages that each pass their own tests
// and disagree with each other is the failure archie-core-fwmp exists to
// prevent, so this is the one test that registers a provider through the
// production Manager and drives the production entry points that resolve a step
// type with it: the State Store server, the daemon's workflow-definitions
// client (its read path, and its write path, which no production caller reaches
// yet) and agentworker.CompilePinnedWorkflow. A site these do not enumerate is
// caught by TestNoProductionSiteReachesForTheBuiltinStepRegistry below.
//
// "Agreement" here means within one build: archie-state-store and archie-agent
// are separately deployed binaries, so a State Store built from newer source
// than the agent it dispatches to can still disagree, and only a matching
// deploy fixes that. This file cannot reach across a deploy boundary and does
// not claim to.
package controlplane

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gopkg.in/yaml.v3"

	"github.com/samcharles93/archie-core/internal/app/agentworker"
	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
	"github.com/samcharles93/archie-core/internal/infrastructure/workflowsteps"
	"github.com/samcharles93/archie-core/internal/taskrun"
)

const (
	probeWorkflow = "probe"
	// probeStepType is contributed by the provider this test registers through
	// the production Manager, so no shared registry can hand it to one side and
	// not the others.
	probeStepType = "probe.registered"
	// unregisteredStepType is declared by no provider, so every side must
	// refuse it.
	unregisteredStepType = "probe.unregistered"
)

// probeProvider is a plugin set of one: a step type no shipped workflow
// registers, whose stage names itself so a side that merely failed to error
// cannot pass.
type probeProvider struct{}

func (probeProvider) Name() string { return "probe" }

func (probeProvider) StepTypes() []workflow.StepType {
	return []workflow.StepType{{Name: probeStepType, Factory: func(yaml.Node) (workflow.Stage, error) {
		return workflow.Stage{Name: probeStepType}, nil
	}}}
}

// probeSteps builds the process vocabulary the way a composition root does —
// through the production constructor — and registers the probe provider on top
// of it.
func probeSteps(t *testing.T) *workflow.Manager {
	t.Helper()
	steps, err := workflowsteps.NewManager()
	if err != nil {
		t.Fatalf("build workflow step vocabulary: %v", err)
	}
	if err := steps.Register(probeProvider{}); err != nil {
		t.Fatalf("register the probe provider through the production Manager: %v", err)
	}
	return steps
}

func probeDefinition(stepType string) string {
	return "id: " + probeWorkflow + "\nsteps:\n  - type: " + stepType + "\n"
}

func probeCollection(stepType string) workflow.WorkflowDefinitionCollection {
	return workflow.WorkflowDefinitionCollection{
		Definitions: []workflow.WorkflowDefinitionEntry{{ID: probeWorkflow, YAML: probeDefinition(stepType)}},
	}
}

// TestEveryProductionResolutionSiteResolvesTheRegisteredStepType drives the
// State Store server, the control-plane client's read and write paths, and
// archie-agent with one vocabulary: a site that resolves the builtin registry
// instead of the manager fails its own assertion, because the probe step type
// exists nowhere else.
func TestEveryProductionResolutionSiteResolvesTheRegisteredStepType(t *testing.T) {
	tests := []struct {
		name     string
		stepType string
		admitted bool
		// wantStage is the stage a definition must compile to. It is empty for
		// a shipped stage, whose factory keeps the workflow-local stage name.
		wantStage string
	}{
		{name: "step type registered through the manager", stepType: probeStepType, admitted: true, wantStage: probeStepType},
		{name: "shipped stage", stepType: "bootstrap.apply", admitted: true},
		{name: "step type no provider declares", stepType: unregisteredStepType},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			steps := probeSteps(t)

			// Every side asserts against the same expectation, so a side that
			// resolves a vocabulary other than the registered one fails its own
			// subtest. That shared column is the agreement this file pins.
			t.Run("state store server", func(t *testing.T) {
				before, after, err := admitThroughStateStore(t, steps, test.stepType)
				if !test.admitted {
					if code := status.Code(err); code != codes.InvalidArgument {
						t.Fatalf("State Store answered %v (code %s), want InvalidArgument: a refused definition is a validation failure, not a store failure", err, code)
					}
					requireRefusal(t, "the State Store server", test.stepType, err)
					return
				}
				requireAdmission(t, "the State Store server", test.stepType, err)
				if after != before+1 {
					t.Errorf("store version moved %d -> %d, want exactly one write: a repeated request ID is replayed idempotently and would make this admission vacuous", before, after)
				}
			})

			t.Run("workflow-definitions client", func(t *testing.T) {
				value, err := json.Marshal(probeCollection(test.stepType))
				if err != nil {
					t.Fatalf("encode definitions: %v", err)
				}
				rpc := &definitionsClient{value: value}
				client, err := NewWorkflowDefinitionsClient(rpc, steps)
				if err != nil {
					t.Fatalf("build workflow-definitions client: %v", err)
				}

				// Read path: the daemon decodes the stored collection here.
				collection, _, readErr := client.WorkflowDefinitions(t.Context())
				// Write path: the client re-validates before it sends anything.
				_, writeErr := client.ReplaceWorkflowDefinitions(t.Context(), probeCollection(test.stepType), 4, "test", "test", "replace:"+test.stepType)

				if !test.admitted {
					requireRefusal(t, "the control-plane client read", test.stepType, readErr)
					requireRefusal(t, "the control-plane client write", test.stepType, writeErr)
					if rpc.command != nil {
						t.Errorf("the client sent %q for a step type it does not resolve", rpc.command.ValueJson)
					}
					return
				}
				requireAdmission(t, "the control-plane client read", test.stepType, readErr)
				if _, ok := collection.DefinitionByID(probeWorkflow); !ok {
					t.Errorf("client read %#v, want the stored %q definition", collection.Definitions, probeWorkflow)
				}
				requireAdmission(t, "the control-plane client write", test.stepType, writeErr)
				if rpc.command == nil {
					t.Fatal("the client accepted the collection without sending a command")
				}
				if !strings.Contains(string(rpc.command.ValueJson), test.stepType) {
					t.Errorf("the client sent %q, which does not carry step type %q", rpc.command.ValueJson, test.stepType)
				}
			})

			t.Run("archie-agent", func(t *testing.T) {
				compiled, err := agentworker.CompilePinnedWorkflow(probeRequest(test.stepType), steps)
				if !test.admitted {
					requireRefusal(t, "archie-agent", test.stepType, err)
					return
				}
				requireAdmission(t, "archie-agent", test.stepType, err)
				if compiled.Name != probeWorkflow || len(compiled.Stages) != 1 {
					t.Fatalf("compiled workflow = %+v, want the single-stage %q definition", compiled, probeWorkflow)
				}
				if test.wantStage != "" && compiled.Stages[0].Name != test.wantStage {
					t.Errorf("compiled stage = %q, want the provider's own stage %q", compiled.Stages[0].Name, test.wantStage)
				}
			})
		})
	}
}

// TestNilStepVocabularyFailsClosedAtEveryResolutionSite pins the guard on the
// manager every resolution site now requires. A nil manager is a wiring bug at
// the composition root, so each site must name it and refuse to resolve, rather
// than dereference it: a nil-pointer panic is the same failure with no clue in
// it.
func TestNilStepVocabularyFailsClosedAtEveryResolutionSite(t *testing.T) {
	resources := pgstore.Open(t)
	t.Cleanup(func() {
		if err := resources.Close(); err != nil {
			t.Errorf("close test store: %v", err)
		}
	})

	if _, err := NewServer(resources, nil); err == nil || !strings.Contains(err.Error(), "no workflow step vocabulary") {
		t.Errorf("NewServer with no step vocabulary: error = %v, want the missing-vocabulary refusal", err)
	}
	if _, err := NewWorkflowDefinitionsClient(&definitionsClient{}, nil); err == nil || !strings.Contains(err.Error(), "no workflow step vocabulary") {
		t.Errorf("NewWorkflowDefinitionsClient with no step vocabulary: error = %v, want the missing-vocabulary refusal", err)
	}
	if _, err := agentworker.CompilePinnedWorkflow(probeRequest(probeStepType), nil); err == nil || !strings.Contains(err.Error(), "no step vocabulary") {
		t.Errorf("CompilePinnedWorkflow with no step vocabulary: error = %v, want the missing-vocabulary refusal", err)
	}
}

// TestAgentWorkerCompilesShippedDefinitionsFromTheInjectedVocabulary pins the
// shipped-definition route inside CompilePinnedWorkflow. That route compiles
// definitions whose steps are all shipped stages, so no plugin assertion can
// reach it; a vocabulary that carries the probe step type and nothing else must
// fail there, and it cannot fail if the route reaches for the builtin registry
// instead of the manager the composition root registered.
func TestAgentWorkerCompilesShippedDefinitionsFromTheInjectedVocabulary(t *testing.T) {
	steps := workflow.NewManager()
	if err := steps.Register(probeProvider{}); err != nil {
		t.Fatalf("register the probe provider through the production Manager: %v", err)
	}

	_, err := agentworker.CompilePinnedWorkflow(&taskrun.Request{Task: &workflow.Task{ID: 1}}, steps)
	if err == nil {
		t.Fatal("the shipped-definition route compiled against a vocabulary carrying no shipped stage")
	}
	if !strings.Contains(err.Error(), "compile shipped workflow definition") || !strings.Contains(err.Error(), "bootstrap.") {
		t.Errorf("error = %v, want the shipped stage the injected vocabulary could not resolve", err)
	}
}

func requireAdmission(t *testing.T, side, stepType string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s refused step type %q: %v", side, stepType, err)
	}
}

// requireRefusal checks the error is a refusal *of this step type*: a store
// outage, a transport failure or an unrelated compile error must not be able to
// stand in for one.
func requireRefusal(t *testing.T, side, stepType string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s accepted step type %q, which no provider declares", side, stepType)
	}
	if !strings.Contains(err.Error(), stepType) {
		t.Errorf("%s failed for %q with %v, which does not name the step type: that is not a refusal of it", side, stepType, err)
	}
}

// admitThroughStateStore writes one definition the way an operator does: a
// replace command against the workflow-definitions resource of a server built
// by the production constructor.
func admitThroughStateStore(t *testing.T, steps *workflow.Manager, stepType string) (before, after int64, err error) {
	t.Helper()

	resources := pgstore.Open(t)
	t.Cleanup(func() {
		if closeErr := resources.Close(); closeErr != nil {
			t.Errorf("close test store: %v", closeErr)
		}
	})
	server, err := NewServer(resources, steps)
	if err != nil {
		t.Fatalf("build control plane server: %v", err)
	}

	if resource, resourceErr := resources.Resource(t.Context(), WorkflowDefinitionsKind); resourceErr == nil {
		before = resource.Version
	}
	value, err := json.Marshal(probeCollection(stepType))
	if err != nil {
		t.Fatalf("encode definitions: %v", err)
	}
	written, err := server.Command(t.Context(), &pb.CommandRequest{
		Kind: WorkflowDefinitionsKind, Command: "replace", ValueJson: value,
		ExpectedVersion: before, Actor: "test", Source: "test",
		// A request ID per step type: the store replays a repeated one
		// idempotently, and a replayed write would validate nothing.
		RequestId: "admit:" + stepType,
	})
	if err != nil {
		return before, before, err
	}
	return before, written.Resource.Version, nil
}

func probeRequest(stepType string) *taskrun.Request {
	definition := probeDefinition(stepType)
	return &taskrun.Request{
		Task: &workflow.Task{
			ID:                       1,
			Workflow:                 probeWorkflow,
			WorkflowDefinitionDigest: workflow.DigestDefinition(definition),
		},
		WorkflowDefinition: definition,
	}
}

// definitionsClient answers the control-plane RPCs the definitions client uses,
// so the production client path is driven without a live State Store.
type definitionsClient struct {
	value   []byte
	command *pb.CommandRequest
}

func (c *definitionsClient) Catalog(context.Context, *pb.CatalogRequest, ...grpc.CallOption) (*pb.CatalogResponse, error) {
	panic("Catalog is not part of the workflow-definitions path")
}

func (c *definitionsClient) Query(context.Context, *pb.QueryRequest, ...grpc.CallOption) (*pb.QueryResponse, error) {
	return &pb.QueryResponse{Resource: &pb.Resource{
		Kind: WorkflowDefinitionsKind, Version: 4, ValueJson: c.value,
	}}, nil
}

func (c *definitionsClient) History(context.Context, *pb.HistoryRequest, ...grpc.CallOption) (*pb.HistoryResponse, error) {
	panic("History is not part of the workflow-definitions path")
}

func (c *definitionsClient) Audit(context.Context, *pb.AuditRequest, ...grpc.CallOption) (*pb.AuditResponse, error) {
	return &pb.AuditResponse{}, nil
}

func (c *definitionsClient) Command(_ context.Context, request *pb.CommandRequest, _ ...grpc.CallOption) (*pb.CommandResponse, error) {
	c.command = request
	return &pb.CommandResponse{Resource: &pb.Resource{Version: 5}}, nil
}

func (c *definitionsClient) Watch(context.Context, *pb.WatchRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[pb.WatchResponse], error) {
	panic("Watch is not part of the workflow-definitions path")
}

// TestNoProductionSiteReachesForTheBuiltinStepRegistry guards the class of
// defect archie-core-fwmp exists to remove: a call site that resolves workflow
// step types out of workflow.BuiltinStepRegistry() instead of the manager its
// composition root registered. The agreement test above can only drive the
// entry points it enumerates, so a sixth one added later would validate, or
// fail to compile, against a vocabulary no other process has. Exactly two
// non-test files may name it: the domain file that declares it, and the shipped
// provider that bundles the shipped stages through it.
func TestNoProductionSiteReachesForTheBuiltinStepRegistry(t *testing.T) {
	allowed := map[string]string{
		filepath.Join("internal", "domain", "workflow", "definition.go"):                 "declares BuiltinStepRegistry",
		filepath.Join("internal", "infrastructure", "workflowsteps", "workflowsteps.go"): "bundles the shipped stages as a provider",
	}
	// The module root, three levels up from this package.
	root := filepath.Join("..", "..", "..")
	scanned := 0
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() {
			// The root is "../../..", whose own name starts with a dot: only
			// directories below it are filtered.
			if path == root {
				return nil
			}
			// tools is a separate module, and the rest carry no Archie source.
			if name == "tools" || name == "node_modules" || name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if _, permitted := allowed[relative]; permitted {
			return nil
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		scanned++
		ast.Inspect(file, func(node ast.Node) bool {
			if ident, ok := node.(*ast.Ident); ok && ident.Name == "BuiltinStepRegistry" {
				t.Errorf("%s names BuiltinStepRegistry; only %s (%s) and %s (%s) may, or that resolution site keeps a step vocabulary of its own",
					relative,
					filepath.Join("internal", "domain", "workflow", "definition.go"), allowed[filepath.Join("internal", "domain", "workflow", "definition.go")],
					filepath.Join("internal", "infrastructure", "workflowsteps", "workflowsteps.go"), allowed[filepath.Join("internal", "infrastructure", "workflowsteps", "workflowsteps.go")])
			}
			return true
		})
		return nil
	})
	if walkErr != nil {
		t.Fatalf("scan the module for BuiltinStepRegistry: %v", walkErr)
	}
	if scanned == 0 {
		t.Fatal("the scan read no non-test Go file; it is not looking at the module")
	}
}
