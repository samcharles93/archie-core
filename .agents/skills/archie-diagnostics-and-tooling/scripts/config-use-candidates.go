// Command config-use-candidates reports syntax-level selector evidence for
// tagged fields declared by internal/config. Results are candidates, not proof
// that a field is wired, live, or dead.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

type fieldDefinition struct {
	path       string
	line       int
	typeName   string
	fieldName  string
	tags       string
	duplicates int
}

type referenceCounts struct {
	config     int
	entrypoint int
	production int
	test       int
	generated  int
}

func main() {
	root := flag.String("root", ".", "repository root to inspect")
	flag.Parse()

	absoluteRoot, err := filepath.Abs(*root)
	if err != nil {
		fail(2, "resolve root: %v", err)
	}
	info, err := os.Stat(absoluteRoot)
	if err != nil {
		fail(2, "inspect root: %v", err)
	}
	if !info.IsDir() {
		fail(2, "root is not a directory: %s", absoluteRoot)
	}

	fset := token.NewFileSet()
	fields, err := readFieldDefinitions(absoluteRoot, fset)
	if err != nil {
		fail(1, "%v", err)
	}
	if len(fields) == 0 {
		fail(1, "no tagged fields found under internal/config")
	}

	nameCounts := map[string]int{}
	for _, field := range fields {
		nameCounts[field.fieldName]++
	}
	for index := range fields {
		fields[index].duplicates = nameCounts[fields[index].fieldName]
	}

	references := map[string]*referenceCounts{}
	for name := range nameCounts {
		references[name] = &referenceCounts{}
	}
	if err := countSelectors(absoluteRoot, fset, references); err != nil {
		fail(1, "%v", err)
	}

	sort.Slice(fields, func(i, j int) bool {
		if fields[i].typeName != fields[j].typeName {
			return fields[i].typeName < fields[j].typeName
		}
		if fields[i].fieldName != fields[j].fieldName {
			return fields[i].fieldName < fields[j].fieldName
		}
		if fields[i].path != fields[j].path {
			return fields[i].path < fields[j].path
		}
		return fields[i].line < fields[j].line
	})

	fmt.Println("META\tschema\tarchie-config-use-candidates/v1")
	fmt.Println("META\tmeaning\tselector-name counts are heuristic candidates; confirm exact fields with gopls references and production composition")
	for _, field := range fields {
		count := references[field.fieldName]
		status := "candidate-selector-seen-in-entrypoint"
		switch {
		case count.production == 0 && count.entrypoint == 0:
			status = "candidate-no-selector-outside-config"
		case count.entrypoint == 0:
			status = "candidate-no-entrypoint-selector"
		}
		fmt.Printf(
			"CONFIG_FIELD\t%s\t%d\t%s.%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%s\n",
			field.path,
			field.line,
			field.typeName,
			field.fieldName,
			field.tags,
			field.duplicates,
			count.config,
			count.entrypoint,
			count.production,
			count.test,
			count.generated,
			status,
		)
	}
}

func readFieldDefinitions(root string, fset *token.FileSet) ([]fieldDefinition, error) {
	configDir := filepath.Join(root, "internal", "config")
	info, err := os.Stat(configDir)
	if err != nil {
		return nil, fmt.Errorf("inspect internal/config: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("internal/config is not a directory")
	}

	var fields []fieldDefinition
	err = filepath.WalkDir(configDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(fset, path, body, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.TYPE {
				continue
			}
			for _, specification := range general.Specs {
				typeSpec, ok := specification.(*ast.TypeSpec)
				if !ok {
					continue
				}
				structType, ok := typeSpec.Type.(*ast.StructType)
				if !ok {
					continue
				}
				for _, field := range structType.Fields.List {
					tags, ok := configTags(field.Tag)
					if !ok {
						continue
					}
					for _, name := range field.Names {
						fields = append(fields, fieldDefinition{
							path:      relative,
							line:      fset.Position(name.Pos()).Line,
							typeName:  typeSpec.Name.Name,
							fieldName: name.Name,
							tags:      tags,
						})
					}
				}
			}
		}
		return nil
	})
	return fields, err
}

func configTags(literal *ast.BasicLit) (string, bool) {
	if literal == nil {
		return "", false
	}
	raw, err := strconv.Unquote(literal.Value)
	if err != nil {
		return "", false
	}
	tag := reflect.StructTag(raw)
	var parts []string
	for _, key := range []string{"toml", "yaml", "json"} {
		value, ok := tag.Lookup(key)
		if !ok {
			continue
		}
		parts = append(parts, key+"="+value)
	}
	return strings.Join(parts, ";"), len(parts) > 0
}

func countSelectors(root string, fset *token.FileSet, references map[string]*referenceCounts) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			if relative != "." && excludedDirectory(relative, entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(entry.Name()) != ".go" {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(fset, path, body, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parse %s: %w", relative, err)
		}
		generated := ast.IsGenerated(file)
		isTest := strings.HasSuffix(entry.Name(), "_test.go")
		inConfig := strings.HasPrefix(relative, "internal/config/")
		inEntrypoint := strings.HasPrefix(relative, "cmd/")

		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			count := references[selector.Sel.Name]
			if count == nil {
				return true
			}
			switch {
			case generated:
				count.generated++
			case isTest:
				count.test++
			case inConfig:
				count.config++
			case inEntrypoint:
				count.entrypoint++
			default:
				count.production++
			}
			return true
		})
		return nil
	})
}

func excludedDirectory(relative, name string) bool {
	switch name {
	case ".git", ".claude", ".references", "node_modules", "vendor", ".gotmp":
		return true
	}
	return strings.HasPrefix(relative, "docs/.vitepress/cache") ||
		strings.HasPrefix(relative, "docs/.vitepress/dist")
}

func fail(code int, format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, "config-use-candidates: "+format+"\n", arguments...)
	os.Exit(code)
}
