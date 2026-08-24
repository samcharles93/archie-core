package agentexec

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// The task-scoped archie-agent process owns the workflow and its local model
// loop. agentexec therefore contains the worker-local stage runner, never a
// second process boundary or a stage transport that archied can select.
func TestPackageContainsNoLegacyExecutionModes(t *testing.T) {
	forbidden := map[string]bool{
		"InProcessRunner":       true,
		"NewInProcessRunner":    true,
		"SubprocessRunner":      true,
		"NATSRunner":            true,
		"ServeOne":              true,
		"Invocation":            true,
		"AgentRequestMessage":   true,
		"AgentResponseEnvelope": true,
		"StageMessage":          true,
		"StageConsumer":         true,
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fileset := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fileset, path, nil, 0)
		if parseErr != nil {
			t.Errorf("parse %s: %v", path, parseErr)
			continue
		}
		for _, declaration := range file.Decls {
			switch declaration := declaration.(type) {
			case *ast.GenDecl:
				for _, spec := range declaration.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if ok && forbidden[typeSpec.Name.Name] {
						t.Errorf("%s declares legacy execution type %s", fileset.Position(typeSpec.Pos()), typeSpec.Name.Name)
					}
				}
			case *ast.FuncDecl:
				if declaration.Recv == nil && forbidden[declaration.Name.Name] {
					t.Errorf("%s declares legacy execution constructor %s", fileset.Position(declaration.Pos()), declaration.Name.Name)
				}
			}
		}
	}
}
