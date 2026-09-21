package archied

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
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

// TestStepVocabularyRegistersTheSharedProviderSet pins the unit both archied
// roots resolve their vocabulary from. Handing this function an empty manager
// (workflow.NewManager) would leave the State Store unable to admit any stored
// definition and the daemon unable to decode one.
func TestStepVocabularyRegistersTheSharedProviderSet(t *testing.T) {
	steps, err := stepVocabulary()
	if err != nil {
		t.Fatalf("stepVocabulary: %v", err)
	}
	if steps == nil {
		t.Fatal("stepVocabulary registered no workflow step vocabulary")
	}
	want := make([]string, 0, len(workflow.BuiltinStepRegistry()))
	for name := range workflow.BuiltinStepRegistry() {
		want = append(want, name)
	}
	slices.Sort(want)
	if got := steps.StepTypes(); !slices.Equal(got, want) {
		t.Fatalf("archied resolves %v, want the shared provider set's shipped stages %v", got, want)
	}
	if err := steps.Register(stepVocabularyProbe{}); err != nil {
		t.Fatalf("register a provider through archied's manager: %v", err)
	}
	if _, resolved := steps.Registry()["probe.registered"]; !resolved {
		t.Fatal("a provider registered through archied's manager did not reach its vocabulary")
	}
}

// TestArchiedRootsResolveTheStepVocabularyFromThatFunction pins that reading
// through stepVocabulary is how both roots reach the vocabulary. Neither root
// can be executed in a unit test -- RunStateStore owns a SQLite file and
// listen address, openStateStoreAdapter dials the State Store -- so the calls
// are asserted by source position, the way composition_order_test.go pins the
// memory-engine ordering for the same reason. A root that built its own manager
// would resolve a vocabulary of its own and silently lose the shipped stages.
func TestArchiedRootsResolveTheStepVocabularyFromThatFunction(t *testing.T) {
	for _, link := range []struct{ file, function, resolves string }{
		{"state_store.go", "RunStateStore", "openStateStoreControlPlane"},
		// RunStateStore delegates to this function, which is where the shipped
		// stages enter this process.
		{"state_store.go", "openStateStoreControlPlane", "stepVocabulary"},
		{"bootstrap.go", "openStateStoreAdapter", "stepVocabulary"},
	} {
		t.Run(link.function+"->"+link.resolves, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), link.file, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			if identCallPosition(methodBody(t, file, link.function), link.resolves) == token.NoPos {
				t.Errorf("%s does not reach its step vocabulary through %s()", link.function, link.resolves)
			}
		})
	}
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
