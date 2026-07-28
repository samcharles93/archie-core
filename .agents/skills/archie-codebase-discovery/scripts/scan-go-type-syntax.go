// Command scan-go-type-syntax reports syntax-level construction candidates for
// a named Go type. It deliberately uses only the standard library and never
// writes to the inspected tree.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type finding struct {
	path   string
	line   int
	column int
	kind   string
	detail string
}

func main() {
	includeTests := flag.Bool("tests", false, "include *_test.go files")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: %s [-tests] TypeName [root]\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() < 1 || flag.NArg() > 2 {
		flag.Usage()
		os.Exit(2)
	}

	typeName := flag.Arg(0)
	root := "."
	if flag.NArg() == 2 {
		root = flag.Arg(1)
	}
	if !token.IsIdentifier(typeName) {
		fmt.Fprintf(os.Stderr, "error: %q is not a Go identifier\n", typeName)
		os.Exit(2)
	}

	results, err := scan(root, typeName, *includeTests)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	fmt.Fprintln(out, "kind\tlocation\tdetail")
	for _, result := range results {
		fmt.Fprintf(out, "%s\t%s:%d:%d\t%s\n",
			result.kind, result.path, result.line, result.column, result.detail)
	}
}

func scan(root, typeName string, includeTests bool) ([]finding, error) {
	fset := token.NewFileSet()
	var results []finding

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path == root {
				return nil
			}
			name := entry.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" ||
				name == "vendor" || name == "bin" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		if !includeTests && strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}

		ast.Inspect(file, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.TypeSpec:
				if node.Name.Name == typeName {
					results = appendFinding(results, fset, node.Name.Pos(),
						path, "declaration", "type "+node.Name.Name)
				}
			case *ast.CompositeLit:
				if matchesType(node.Type, typeName) {
					results = appendFinding(results, fset, node.Type.Pos(),
						path, "literal", "direct composite literal of "+typeName)
				}
			case *ast.FuncDecl:
				if returnsType(node.Type.Results, typeName) {
					results = appendFinding(results, fset, node.Name.Pos(),
						path, "returns-type", "func "+node.Name.Name)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(results, func(i, j int) bool {
		left, right := results[i], results[j]
		if left.path != right.path {
			return left.path < right.path
		}
		if left.line != right.line {
			return left.line < right.line
		}
		if left.column != right.column {
			return left.column < right.column
		}
		return left.kind < right.kind
	})
	return results, nil
}

func appendFinding(results []finding, fset *token.FileSet, pos token.Pos, path, kind, detail string) []finding {
	position := fset.Position(pos)
	return append(results, finding{
		path:   filepath.ToSlash(path),
		line:   position.Line,
		column: position.Column,
		kind:   kind,
		detail: detail,
	})
}

func returnsType(fields *ast.FieldList, typeName string) bool {
	if fields == nil {
		return false
	}
	for _, field := range fields.List {
		if matchesType(field.Type, typeName) {
			return true
		}
	}
	return false
}

func matchesType(expr ast.Expr, typeName string) bool {
	switch expr := expr.(type) {
	case *ast.Ident:
		return expr.Name == typeName
	case *ast.SelectorExpr:
		return expr.Sel.Name == typeName
	case *ast.StarExpr:
		return matchesType(expr.X, typeName)
	case *ast.IndexExpr:
		return matchesType(expr.X, typeName)
	case *ast.IndexListExpr:
		return matchesType(expr.X, typeName)
	default:
		return false
	}
}
