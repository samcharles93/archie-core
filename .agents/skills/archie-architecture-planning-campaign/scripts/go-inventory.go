// Command go-inventory emits a stable TSV inventory of Go declarations.
//
// It uses the Go parser instead of text matching, so architecture research can
// enumerate imports, types, struct fields, functions, methods, variables, and
// constants before tracing their references with gopls.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type record struct {
	kind    string
	pkg     string
	file    string
	line    int
	name    string
	detail  string
	sortKey string
}

func main() {
	rootFlag := flag.String("root", ".", "repository root")
	scopeFlag := flag.String("scope", "internal", "repository-relative directory to inspect")
	includeTests := flag.Bool("include-tests", true, "include _test.go files")
	flag.Parse()

	root, err := filepath.Abs(*rootFlag)
	if err != nil {
		fatalf("resolve root: %v", err)
	}
	scope, err := confinedPath(root, *scopeFlag)
	if err != nil {
		fatalf("resolve scope: %v", err)
	}

	fset := token.NewFileSet()
	var records []record
	err = filepath.WalkDir(scope, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "testdata", "vendor":
				if path != scope {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || (!*includeTests && strings.HasSuffix(path, "_test.go")) {
			return nil
		}

		parsed, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return fmt.Errorf("parse %s: %w", path, parseErr)
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		relative = filepath.ToSlash(relative)
		pkg := filepath.ToSlash(filepath.Dir(relative))
		records = append(records, fileRecords(fset, parsed, pkg, relative)...)
		return nil
	})
	if err != nil {
		fatalf("inventory: %v", err)
	}

	sort.Slice(records, func(i, j int) bool {
		return records[i].sortKey < records[j].sortKey
	})
	fmt.Println("KIND\tPACKAGE\tFILE\tLINE\tNAME\tDETAIL")
	for _, item := range records {
		fmt.Printf("%s\t%s\t%s\t%d\t%s\t%s\n",
			item.kind,
			item.pkg,
			item.file,
			item.line,
			sanitize(item.name),
			sanitize(item.detail),
		)
	}
}

func confinedPath(root, scope string) (string, error) {
	if filepath.IsAbs(scope) {
		return "", fmt.Errorf("scope must be repository-relative")
	}
	clean := filepath.Clean(scope)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("scope escapes repository root")
	}
	path := filepath.Join(root, clean)
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", clean)
	}
	return path, nil
}

func fileRecords(fset *token.FileSet, file *ast.File, pkg, relative string) []record {
	var records []record
	add := func(kind string, pos token.Pos, name, detail string) {
		line := fset.Position(pos).Line
		records = append(records, record{
			kind:    kind,
			pkg:     pkg,
			file:    relative,
			line:    line,
			name:    name,
			detail:  detail,
			sortKey: strings.Join([]string{pkg, relative, fmt.Sprintf("%09d", line), kind, name}, "\x00"),
		})
	}

	for _, imported := range file.Imports {
		path, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			path = imported.Path.Value
		}
		name := ""
		if imported.Name != nil {
			name = imported.Name.Name
		}
		add("IMPORT", imported.Pos(), name, path)
	}

	for _, declaration := range file.Decls {
		switch node := declaration.(type) {
		case *ast.FuncDecl:
			kind := "FUNC"
			name := node.Name.Name
			if node.Recv != nil && len(node.Recv.List) > 0 {
				kind = "METHOD"
				name = expression(node.Recv.List[0].Type) + "." + name
			}
			add(kind, node.Pos(), name, expression(node.Type))
		case *ast.GenDecl:
			for _, spec := range node.Specs {
				switch item := spec.(type) {
				case *ast.TypeSpec:
					detail := typeKind(item.Type)
					add("TYPE", item.Pos(), item.Name.Name, detail)
					structType, ok := item.Type.(*ast.StructType)
					if !ok {
						continue
					}
					for _, field := range structType.Fields.List {
						fieldNames := make([]string, 0, len(field.Names))
						for _, name := range field.Names {
							fieldNames = append(fieldNames, name.Name)
						}
						if len(fieldNames) == 0 {
							fieldNames = append(fieldNames, expression(field.Type))
						}
						for _, fieldName := range fieldNames {
							add("FIELD", field.Pos(), item.Name.Name+"."+fieldName, expression(field.Type))
						}
					}
				case *ast.ValueSpec:
					kind := "VAR"
					if node.Tok == token.CONST {
						kind = "CONST"
					}
					for _, name := range item.Names {
						add(kind, item.Pos(), name.Name, expression(item.Type))
					}
				}
			}
		}
	}
	return records
}

func expression(node ast.Node) string {
	if node == nil {
		return ""
	}
	var buffer bytes.Buffer
	if err := format.Node(&buffer, token.NewFileSet(), node); err != nil {
		return fmt.Sprintf("%T", node)
	}
	return buffer.String()
}

func typeKind(node ast.Expr) string {
	switch node.(type) {
	case *ast.StructType:
		return "struct"
	case *ast.InterfaceType:
		return "interface"
	case *ast.FuncType:
		return "func"
	case *ast.ArrayType:
		return "array-or-slice"
	case *ast.MapType:
		return "map"
	case *ast.ChanType:
		return "chan"
	default:
		return expression(node)
	}
}

func sanitize(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
