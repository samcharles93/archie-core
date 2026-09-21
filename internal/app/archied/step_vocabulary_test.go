package archied

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/infrastructure/workflowsteps"
)

// stepVocabularyProbe is a provider set of one, used only to prove the manager
// a root builds is live: registering through it must reach its vocabulary.
type stepVocabularyProbe struct{}

func (stepVocabularyProbe) Name() string { return "probe" }

func (stepVocabularyProbe) StepTypes() []workflow.StepType {
	return []workflow.StepType{{Name: "probe.registered", Factory: func(yaml.Node) (workflow.Stage, error) {
		return workflow.Stage{Name: "probe.registered"}, nil
	}}}
}

// sharedStepTypeNames returns the step types the shared provider set
// (internal/infrastructure/workflowsteps) declares. It is read through the same
// Providers() seam the composition roots register, rather than from
// workflow.BuiltinStepRegistry(): the shipped stages arrive by registration, and
// a provider added to that set is the documented way a step type enters every
// binary at once.
func sharedStepTypeNames() []string {
	names := make([]string, 0)
	for _, provider := range workflowsteps.Providers() {
		for _, stepType := range provider.StepTypes() {
			names = append(names, stepType.Name)
		}
	}
	slices.Sort(names)
	return names
}

// requireSharedStepVocabulary holds a composition root's manager to both halves
// of the contract: it resolves the shipped vocabulary, and it is live enough to
// take a provider registered through it.
func requireSharedStepVocabulary(t *testing.T, steps *workflow.Manager) {
	t.Helper()
	if steps == nil {
		t.Fatal("the composition root registered no workflow step vocabulary")
	}
	got := steps.StepTypes()
	// Containment, not equality: adding a provider to the shared set is the
	// documented way a step type arrives, and that must not have to edit this
	// guard. What the guard holds is that no shipped stage is missing, which is
	// what a root regressing to workflow.NewManager() loses.
	for name := range workflow.BuiltinStepRegistry() {
		if !slices.Contains(got, name) {
			t.Errorf("the composition root resolves %v, which is missing the shipped stage %q", got, name)
		}
	}
	// The other half is provenance: every name the root resolves must be
	// declared by the shared provider set, not by a near-identical provider of
	// the root's own. That set is what ties this process's vocabulary to the
	// other side of the contract. It is content, not identity -- the shipped
	// provider rebuilds its factories on every call -- so a root that stopped
	// importing the shared set and hand-rolled an equal one would still pass.
	declared := sharedStepTypeNames()
	for _, name := range got {
		if !slices.Contains(declared, name) {
			t.Errorf("the composition root resolves %q, which no provider in the shared set declares", name)
		}
	}
	if err := steps.Register(stepVocabularyProbe{}); err != nil {
		t.Fatalf("register a provider through the root's manager: %v", err)
	}
	if _, resolved := steps.Registry()["probe.registered"]; !resolved {
		t.Fatal("a provider registered through the root's manager did not reach its vocabulary")
	}
}

// TestStepVocabularyRegistersTheSharedProviderSet pins the unit the daemon root
// resolves its vocabulary from. Handing this function an empty manager
// (workflow.NewManager) would leave the daemon unable to decode the stored
// definitions it pins onto tasks.
func TestStepVocabularyRegistersTheSharedProviderSet(t *testing.T) {
	steps, err := stepVocabulary()
	if err != nil {
		t.Fatalf("stepVocabulary: %v", err)
	}
	requireSharedStepVocabulary(t, steps)
}

// TestArchiedRootsResolveTheStepVocabularyFromThatFunction pins both halves of
// each root's wiring: the root reaches the vocabulary through stepVocabulary,
// and the manager it returns is the one handed to the constructor that resolves
// a step type with it. Neither root can be executed in a unit test -- Run owns
// a SQLite file and a listen address, openDaemonWorkflowDefinitions dials the
// State Store -- so both are asserted by source position, the way
// composition_order_test.go pins the memory-engine ordering for the same
// reason. A root that built its own manager, or built one and handed the
// constructor a different one, would resolve a vocabulary without the shipped
// stages.
func TestArchiedRootsResolveTheStepVocabularyFromThatFunction(t *testing.T) {
	for _, link := range []struct{ file, function, constructor string }{
		// The validating side: the State Store's control plane server.
		{"state_store.go", "openStateStoreControlPlane", "NewServer"},
		// The executing side's client, built by the daemon root alone.
		{"step_vocabulary.go", "openDaemonWorkflowDefinitions", "NewWorkflowDefinitionsClient"},
	} {
		t.Run(link.function+"->stepVocabulary->"+link.constructor, func(t *testing.T) {
			body := parsedBody(t, link.file, link.function)
			manager := stepVocabularyAssignment(body)
			if manager == "" {
				t.Fatalf("%s does not build its step vocabulary through stepVocabulary()", link.function)
			}
			if !callPassesIdent(body, link.constructor, manager) {
				t.Errorf("%s does not hand the manager stepVocabulary() returned (%s) to %s()", link.function, manager, link.constructor)
			}
		})
	}
}

// TestOnlyTheDaemonRootRegistersAStepVocabulary pins the cut the shape applies
// to every other process: a root that cannot resolve a step type registers no
// vocabulary and cannot be failed by one. openStateStoreAdapter is shared by the
// daemon and the gateway, and the gateway resolves no step type -- its
// control-plane surfaces are catalog, runtime settings and personas -- so the
// vocabulary belongs to the daemon root alone, exactly as
// internal/app/archiemessaging's dead vocabulary was removed. Asserted by
// source position for the same reason as above.
func TestOnlyTheDaemonRootRegistersAStepVocabulary(t *testing.T) {
	if pos := identCallPosition(parsedBody(t, "bootstrap.go", "openStateStoreAdapter"), "stepVocabulary"); pos != token.NoPos {
		t.Error("openStateStoreAdapter registers a step vocabulary; it is shared with the gateway root, which never resolves a step type")
	}
	if fileMentionsIdent(t, "gateway.go", "stepVocabulary") {
		t.Error("the gateway root file mentions stepVocabulary; the gateway resolves no workflow step type")
	}

	run := parsedBody(t, "main.go", "Run")
	adapter := methodCallPosition(run, "openStateStoreAdapter")
	wiring := methodCallPosition(run, "openDaemonWorkflowDefinitions")
	build := methodCallPosition(run, "buildDaemon")
	if wiring == token.NoPos {
		t.Fatal("Run never builds the daemon's workflow-definitions client; every task pin would fall back to the shipped definitions")
	}
	if adapter == token.NoPos || wiring < adapter {
		t.Errorf("Run builds the workflow-definitions client at %v, at or before openStateStoreAdapter at %v: it would wrap the transport the adapter has not opened yet", wiring, adapter)
	}
	if build == token.NoPos || wiring > build {
		t.Errorf("Run builds the workflow-definitions client at %v, after buildDaemon at %v: the daemon would capture no definitions surface", wiring, build)
	}
}

func parsedBody(t *testing.T, file, function string) *ast.BlockStmt {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return methodBody(t, parsed, function)
}

// stepVocabularyAssignment returns the identifier assigned from a direct call
// to stepVocabulary() in body, or "" when body never calls it.
func stepVocabularyAssignment(body *ast.BlockStmt) string {
	manager := ""
	ast.Inspect(body, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) == 0 || len(assign.Rhs) == 0 || manager != "" {
			return true
		}
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok {
			return true
		}
		callee, ok := call.Fun.(*ast.Ident)
		if !ok || callee.Name != "stepVocabulary" {
			return true
		}
		if lhs, ok := assign.Lhs[0].(*ast.Ident); ok {
			manager = lhs.Name
		}
		return true
	})
	return manager
}

// callPassesIdent reports whether some call to name (bare or selected) passes an
// identifier called ident as an argument.
func callPassesIdent(body *ast.BlockStmt, name, ident string) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || !callNameIs(call, name) {
			return true
		}
		for _, argument := range call.Args {
			if passed, ok := argument.(*ast.Ident); ok && passed.Name == ident {
				found = true
			}
		}
		return true
	})
	return found
}

func callNameIs(call *ast.CallExpr, name string) bool {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name == name
	case *ast.SelectorExpr:
		return fun.Sel.Name == name
	}
	return false
}

// fileMentionsIdent reports whether any declaration in file names ident.
func fileMentionsIdent(t *testing.T, file, ident string) bool {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	mentions := false
	ast.Inspect(parsed, func(node ast.Node) bool {
		if found, ok := node.(*ast.Ident); ok && found.Name == ident {
			mentions = true
		}
		return true
	})
	return mentions
}

// identCallPosition returns the position of the first call to a bare function
// name in body, or token.NoPos when the body never calls it.
func identCallPosition(body *ast.BlockStmt, name string) token.Pos {
	position := token.NoPos
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		ident, ok := call.Fun.(*ast.Ident)
		if ok && ident.Name == name && position == token.NoPos {
			position = call.Pos()
		}
		return true
	})
	return position
}
