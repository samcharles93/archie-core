// Command source-metrics emits deterministic, syntax-derived Go source metrics.
// It never writes to the inspected tree and uses only the Go standard library.
package main

import (
	"bytes"
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

type counts struct {
	productionFiles int
	testFiles       int
	generatedFiles  int
	productionLines int
	testLines       int
	generatedLines  int
	functions       int
	methods         int
}

type packageCounts struct {
	module          string
	path            string
	productionFiles int
	testFiles       int
	generatedFiles  int
	productionLines int
	testLines       int
	generatedLines  int
}

type hotspot struct {
	module     string
	path       string
	line       int
	symbol     string
	spanLines  int
	statements int
	complexity int
}

func main() {
	root := flag.String("root", ".", "repository root to inspect")
	top := flag.Int("top", 30, "maximum hotspot rows; zero means all")
	largeLines := flag.Int("large-lines", 80, "minimum body span for a hotspot")
	complexity := flag.Int("complexity", 15, "minimum approximate syntactic complexity for a hotspot")
	includeTests := flag.Bool("include-test-hotspots", false, "include test functions in hotspot rows")
	flag.Parse()

	if *top < 0 || *largeLines < 1 || *complexity < 1 {
		fail(2, "top must be non-negative; thresholds must be positive")
	}

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

	modules := map[string]*counts{}
	packages := map[string]*packageCounts{}
	var hotspots []hotspot
	fset := token.NewFileSet()

	err = filepath.WalkDir(absoluteRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(absoluteRoot, path)
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

		module := moduleFor(relative)
		moduleCounts := modules[module]
		if moduleCounts == nil {
			moduleCounts = &counts{}
			modules[module] = moduleCounts
		}
		dir := filepath.ToSlash(filepath.Dir(relative))
		packageKey := module + "\x00" + dir
		pkg := packages[packageKey]
		if pkg == nil {
			pkg = &packageCounts{module: module, path: dir}
			packages[packageKey] = pkg
		}

		lines := physicalLines(body)
		generated := ast.IsGenerated(file)
		isTest := strings.HasSuffix(entry.Name(), "_test.go")
		switch {
		case generated:
			moduleCounts.generatedFiles++
			moduleCounts.generatedLines += lines
			pkg.generatedFiles++
			pkg.generatedLines += lines
		case isTest:
			moduleCounts.testFiles++
			moduleCounts.testLines += lines
			pkg.testFiles++
			pkg.testLines += lines
		default:
			moduleCounts.productionFiles++
			moduleCounts.productionLines += lines
			pkg.productionFiles++
			pkg.productionLines += lines
		}

		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			if function.Recv == nil {
				moduleCounts.functions++
			} else {
				moduleCounts.methods++
			}
			if generated || (isTest && !*includeTests) {
				continue
			}

			start := fset.Position(function.Body.Lbrace).Line
			end := fset.Position(function.Body.Rbrace).Line
			row := hotspot{
				module:     module,
				path:       relative,
				line:       fset.Position(function.Pos()).Line,
				symbol:     symbolName(function),
				spanLines:  end - start + 1,
				statements: statementNodes(function.Body),
				complexity: syntacticComplexity(function.Body),
			}
			if row.spanLines >= *largeLines || row.complexity >= *complexity {
				hotspots = append(hotspots, row)
			}
		}
		return nil
	})
	if err != nil {
		fail(1, "%v", err)
	}

	fmt.Println("META\tschema\tarchie-source-metrics/v1")
	fmt.Printf("META\tthresholds\tlarge_lines=%d;syntactic_complexity=%d;top=%d\n", *largeLines, *complexity, *top)

	moduleNames := sortedKeys(modules)
	for _, module := range moduleNames {
		value := modules[module]
		emitMetric(module, "production_go_files", value.productionFiles)
		emitMetric(module, "test_go_files", value.testFiles)
		emitMetric(module, "generated_go_files", value.generatedFiles)
		emitMetric(module, "production_physical_lines", value.productionLines)
		emitMetric(module, "test_physical_lines", value.testLines)
		emitMetric(module, "generated_physical_lines", value.generatedLines)
		emitMetric(module, "functions", value.functions)
		emitMetric(module, "methods", value.methods)
	}

	packageRows := make([]*packageCounts, 0, len(packages))
	for _, row := range packages {
		packageRows = append(packageRows, row)
	}
	sort.Slice(packageRows, func(i, j int) bool {
		if packageRows[i].module != packageRows[j].module {
			return packageRows[i].module < packageRows[j].module
		}
		return packageRows[i].path < packageRows[j].path
	})
	for _, row := range packageRows {
		fmt.Printf(
			"PACKAGE\t%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\n",
			row.module,
			row.path,
			row.productionFiles,
			row.testFiles,
			row.generatedFiles,
			row.productionLines,
			row.testLines,
			row.generatedLines,
		)
	}

	sort.Slice(hotspots, func(i, j int) bool {
		if hotspots[i].spanLines != hotspots[j].spanLines {
			return hotspots[i].spanLines > hotspots[j].spanLines
		}
		if hotspots[i].complexity != hotspots[j].complexity {
			return hotspots[i].complexity > hotspots[j].complexity
		}
		if hotspots[i].path != hotspots[j].path {
			return hotspots[i].path < hotspots[j].path
		}
		if hotspots[i].line != hotspots[j].line {
			return hotspots[i].line < hotspots[j].line
		}
		return hotspots[i].symbol < hotspots[j].symbol
	})
	if *top > 0 && len(hotspots) > *top {
		hotspots = hotspots[:*top]
	}
	for _, row := range hotspots {
		fmt.Printf(
			"HOTSPOT\t%s\t%s\t%d\t%s\t%d\t%d\t%d\n",
			row.module,
			row.path,
			row.line,
			row.symbol,
			row.spanLines,
			row.statements,
			row.complexity,
		)
	}
}

func excludedDirectory(relative, name string) bool {
	switch name {
	case ".git", ".claude", ".references", "node_modules", "vendor", ".gotmp":
		return true
	}
	return strings.HasPrefix(relative, "docs-2/.vitepress/cache") ||
		strings.HasPrefix(relative, "docs-2/.vitepress/dist")
}

func moduleFor(relative string) string {
	if relative == "tools" || strings.HasPrefix(relative, "tools/") {
		return "tools"
	}
	return "root"
}

func physicalLines(body []byte) int {
	if len(body) == 0 {
		return 0
	}
	lines := bytes.Count(body, []byte{'\n'})
	if body[len(body)-1] != '\n' {
		lines++
	}
	return lines
}

func symbolName(function *ast.FuncDecl) string {
	if function.Recv == nil || len(function.Recv.List) == 0 {
		return function.Name.Name
	}
	return receiverName(function.Recv.List[0].Type) + "." + function.Name.Name
}

func receiverName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.StarExpr:
		return "*" + receiverName(value.X)
	case *ast.IndexExpr:
		return receiverName(value.X)
	case *ast.IndexListExpr:
		return receiverName(value.X)
	default:
		return "receiver"
	}
}

func statementNodes(node ast.Node) int {
	count := 0
	ast.Inspect(node, func(candidate ast.Node) bool {
		statement, ok := candidate.(ast.Stmt)
		if !ok {
			return true
		}
		switch statement.(type) {
		case *ast.BlockStmt, *ast.EmptyStmt:
		default:
			count++
		}
		return true
	})
	return count
}

func syntacticComplexity(node ast.Node) int {
	value := 1
	ast.Inspect(node, func(candidate ast.Node) bool {
		switch item := candidate.(type) {
		case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.CommClause:
			value++
		case *ast.CaseClause:
			if len(item.List) > 0 {
				value++
			}
		case *ast.BinaryExpr:
			if item.Op == token.LAND || item.Op == token.LOR {
				value++
			}
		}
		return true
	})
	return value
}

func sortedKeys(values map[string]*counts) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func emitMetric(module, name string, value int) {
	fmt.Printf("METRIC\t%s\t%s\t%d\n", module, name, value)
}

func fail(code int, format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, "source-metrics: "+format+"\n", arguments...)
	os.Exit(code)
}
