package agentworker

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestApplicationOwnsNoTransportWireMechanics(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imported := range file.Imports {
			name, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case name == "encoding/json":
				t.Errorf("%s imports JSON wire encoding", path)
			case name == "github.com/samcharles93/archie-core/internal/domain/workintake":
				t.Errorf("%s imports NATS topology subjects", path)
			case name == "github.com/nats-io/nats.go" || strings.HasPrefix(name, "github.com/nats-io/nats.go/"):
				t.Errorf("%s imports raw NATS SDK package %q", path, name)
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if selector.Sel.Name == "ReplyAddress" || selector.Sel.Name == "Subject" || selector.Sel.Name == "Data" {
				t.Errorf("%s inspects transport wire selector %s", path, selector.Sel.Name)
			}
			return true
		})
	}
}

// Full-task request/reply is the worker's only production entry point. A
// single-stage loop would recreate the obsolete split where archied owns the
// workflow and remotely invokes each stage.
func TestApplicationContainsNoSingleStageWorkerLoop(t *testing.T) {
	for _, path := range []string{"single_stage_request.go", "stage_loop.go"} {
		matches, err := filepath.Glob(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 0 {
			t.Errorf("legacy single-stage worker file still exists: %s", path)
		}
	}
}
