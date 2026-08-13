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
