package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCommandBoundary(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if !strings.HasSuffix(path, "_test.go") && path != "main.go" {
			t.Errorf("cmd/archie-agent contains substantive production file %q; runtime ownership belongs in internal/app/agentworker", path)
		}
	}

	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	const (
		projectInternal = "github.com/samcharles93/archie-core/internal/"
		appImport       = projectInternal + "app/agentworker"
	)
	appIdent := "agentworker"
	for _, imported := range file.Imports {
		path, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(path, projectInternal) && path != appImport {
			t.Errorf("cmd/archie-agent imports substantive internal package %q; only %q is allowed", path, appImport)
		}
		if path == appImport && imported.Name != nil {
			if imported.Name.Name == "." || imported.Name.Name == "_" {
				t.Errorf("cmd/archie-agent imports %q with unsupported alias %q", appImport, imported.Name.Name)
			} else {
				appIdent = imported.Name.Name
			}
		}
	}

	allowedFunctions := map[string]bool{
		"main":                   true,
		"run":                    true,
		"runCommand":             true,
		"natsConnectionSettings": true,
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if function.Recv != nil || !allowedFunctions[function.Name.Name] {
			t.Errorf("cmd/archie-agent main.go declares unexpected function %q; orchestration belongs in internal/app/agentworker", function.Name.Name)
		}
		delete(allowedFunctions, function.Name.Name)
	}
	for name := range allowedFunctions {
		t.Errorf("cmd/archie-agent main.go is missing intended process function %q", name)
	}

	var runFunction *ast.FuncDecl
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == "run" {
			runFunction = function
			break
		}
	}
	if runFunction == nil {
		t.Fatal("cmd/archie-agent has no run function")
	}

	workerCalls := 0
	ast.Inspect(runFunction.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		callee, ok := call.Fun.(*ast.Ident)
		if !ok || callee.Name != "runCommand" {
			return true
		}
		for _, argument := range call.Args {
			selector, ok := argument.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Run" {
				continue
			}
			pkg, ok := selector.X.(*ast.Ident)
			if ok && pkg.Name == appIdent {
				workerCalls++
			}
		}
		return true
	})
	if workerCalls != 1 {
		t.Fatalf("cmd/archie-agent run passes agentworker.Run to runCommand %d times, want exactly 1", workerCalls)
	}
}
