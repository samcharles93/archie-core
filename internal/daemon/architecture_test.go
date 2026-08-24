package daemon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const workflowImportPath = "github.com/samcharles93/archie-core/internal/domain/workflow"

// Autonomous workflow execution belongs exclusively to the task-scoped
// archie-agent worker. A local workflow.Run call in daemon silently restores
// the host-authority fallback this boundary exists to remove.
func TestDaemonNeverExecutesWorkflowLocally(t *testing.T) {
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
		workflowNames := make(map[string]bool)
		dotImport := false
		for _, spec := range file.Imports {
			importPath, unquoteErr := strconv.Unquote(spec.Path.Value)
			if unquoteErr != nil || importPath != workflowImportPath {
				continue
			}
			switch {
			case spec.Name == nil:
				workflowNames["workflow"] = true
			case spec.Name.Name == ".":
				dotImport = true
			case spec.Name.Name != "_":
				workflowNames[spec.Name.Name] = true
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if dotImport {
				if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "Run" {
					reportLocalWorkflowRun(t, fileset, call)
				}
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Run" {
				return true
			}
			pkg, ok := selector.X.(*ast.Ident)
			if ok && workflowNames[pkg.Name] {
				reportLocalWorkflowRun(t, fileset, call)
			}
			return true
		})
	}
}

func reportLocalWorkflowRun(t *testing.T, fileset *token.FileSet, call *ast.CallExpr) {
	t.Helper()
	position := fileset.Position(call.Pos())
	t.Errorf("%s calls workflow.Run; autonomous workflows must use the full-task worker handoff", position)
}

// Daemon and IdentityRunner carry routing and authority state only. Keeping a
// stage runner or workflow registry on either type makes a host execution path
// one call away from returning.
func TestDaemonCarriesNoLocalWorkflowExecutionState(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "daemon.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	forbidden := map[string]bool{"Agent": true, "Workflows": true, "CustomStages": true}
	for _, declaration := range file.Decls {
		gen, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || (typeSpec.Name.Name != "Daemon" && typeSpec.Name.Name != "IdentityRunner") {
				continue
			}
			structure, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range structure.Fields.List {
				for _, name := range field.Names {
					if forbidden[name.Name] {
						t.Errorf("%s carries local workflow execution field %s", typeSpec.Name.Name, name.Name)
					}
				}
			}
		}
	}
}
