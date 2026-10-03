package prreview

import (
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// BlastRadius returns the files that import a changed file or that a changed
// file imports. Unresolvable imports are ignored.
func BlastRadius(fsys fs.FS, changed []string) ([]string, error) {
	graph, err := buildImportGraph(fsys)
	if err != nil {
		return nil, err
	}

	reached := map[string]bool{}
	for _, file := range changed {
		for _, imported := range graph.imports[file] {
			reached[imported] = true
		}
		for _, dependent := range graph.dependents[file] {
			reached[dependent] = true
		}
	}
	for _, file := range changed {
		delete(reached, file)
	}

	radius := make([]string, 0, len(reached))
	for file := range reached {
		radius = append(radius, file)
	}
	sort.Strings(radius)
	return radius, nil
}

// ExposureCounts returns, for each file BlastRadius reaches, how many changed
// files reach it.
func ExposureCounts(fsys fs.FS, changed []string) (map[string]int, error) {
	graph, err := buildImportGraph(fsys)
	if err != nil {
		return nil, err
	}
	changedSet := make(map[string]bool, len(changed))
	for _, file := range changed {
		changedSet[file] = true
	}

	counts := map[string]int{}
	for _, file := range changed {
		reached := map[string]bool{}
		for _, imported := range graph.imports[file] {
			reached[imported] = true
		}
		for _, dependent := range graph.dependents[file] {
			reached[dependent] = true
		}
		for target := range reached {
			if changedSet[target] {
				continue
			}
			counts[target]++
		}
	}
	return counts, nil
}

// HighExposureThreshold is the fan-in above which a blast-radius file counts
// as high-exposure for the coverage gate: reached by more than one changed
// file, so a regression there is not one dimension's problem to have covered.
const HighExposureThreshold = 1

// HighExposureFiles returns the blast-radius files whose exposure count
// exceeds HighExposureThreshold, sorted for a deterministic gap-review order.
func HighExposureFiles(counts map[string]int) []string {
	var files []string
	for file, count := range counts {
		if count > HighExposureThreshold {
			files = append(files, file)
		}
	}
	sort.Strings(files)
	return files
}

// importGraph is a snapshot's file-level dependency graph, in both directions:
// an edge exists when one file's imports resolve to another file.
type importGraph struct {
	imports    map[string][]string
	dependents map[string][]string
}

// buildImportGraph reads every source file in the snapshot once and resolves
// the imports it declares. A file contributes no edges of its own when it
// declares none, or when it cannot be parsed: an unparsable file's imports are
// not dependable, and a guessed edge is worse than a missing one.
func buildImportGraph(fsys fs.FS) (*importGraph, error) {
	paths, err := walkPaths(fsys, isSourcePath)
	if err != nil {
		return nil, err
	}
	module, err := modulePath(fsys)
	if err != nil {
		return nil, err
	}

	goPackages := goPackageFiles(paths, module)
	pythonModules := pythonModuleFiles(paths)
	graph := &importGraph{imports: map[string][]string{}, dependents: map[string][]string{}}
	for _, file := range paths {
		imported, err := importSpecs(fsys, file, goPackages, pythonModules)
		if err != nil {
			return nil, err
		}
		for _, target := range imported {
			graph.imports[file] = appendUnique(graph.imports[file], target)
			graph.dependents[target] = appendUnique(graph.dependents[target], file)
		}
	}
	return graph, nil
}

// isSourcePath reports whether a file's imports are resolved.
func isSourcePath(file string) bool { return sourceExtensions[path.Ext(file)] }

// sourceExtensions are the files whose imports this package resolves.
var sourceExtensions = map[string]bool{
	".go":  true,
	".py":  true,
	".js":  true,
	".jsx": true,
	".ts":  true,
	".tsx": true,
	".mjs": true,
	".cjs": true,
}

// modulePath reads the module path out of the snapshot's go.mod, empty when
// the snapshot has none. Without it an import path cannot be turned back into
// a directory, so no Go edge is claimed at all.
func modulePath(fsys fs.FS) (string, error) {
	data, err := fs.ReadFile(fsys, "go.mod")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("read go.mod: %w", err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if module, found := strings.CutPrefix(line, "module "); found {
			return strings.TrimSpace(module), nil
		}
	}
	return "", nil
}

// goPackageFiles maps each Go import path in the snapshot to its non-test
// files.
func goPackageFiles(paths []string, module string) map[string][]string {
	if module == "" {
		return nil
	}
	packages := map[string][]string{}
	for _, file := range paths {
		if path.Ext(file) != ".go" || strings.HasSuffix(file, "_test.go") {
			continue
		}
		importPath := module
		if directory := path.Dir(file); directory != "." {
			importPath = module + "/" + directory
		}
		packages[importPath] = append(packages[importPath], file)
	}
	return packages
}

// pythonModuleFiles maps every Python module name this snapshot owns to the
// files that claim it, so `from pkg.util import helper` resolves to pkg/util.py
// and `import pkg` to pkg/__init__.py.
func pythonModuleFiles(paths []string) map[string][]string {
	modules := map[string][]string{}
	for _, file := range paths {
		if path.Ext(file) != ".py" {
			continue
		}
		modules[pythonModule(file)] = append(modules[pythonModule(file)], file)
	}
	return modules
}

// pythonModule is the module name Python imports a file by: directories become
// dots and the .py suffix, or a package's __init__, goes.
func pythonModule(file string) string {
	module := strings.TrimSuffix(strings.TrimSuffix(file, ".py"), "/__init__")
	return strings.ReplaceAll(module, "/", ".")
}

// importSpecs resolves the files one source file imports.
func importSpecs(fsys fs.FS, file string, goPackages, pythonModules map[string][]string) ([]string, error) {
	data, err := fs.ReadFile(fsys, file)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", file, err)
	}
	switch path.Ext(file) {
	case ".go":
		return goImports(data, goPackages), nil
	case ".py":
		return pythonImports(data, pythonModules), nil
	default:
		return jsImports(fsys, file, data), nil
	}
}

// goImports resolves a Go file's imports to the files of the packages they
// name.
func goImports(data []byte, packages map[string][]string) []string {
	if packages == nil {
		return nil
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), "", data, parser.ImportsOnly)
	if err != nil {
		return nil
	}
	var imported []string
	for _, spec := range parsed.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		imported = append(imported, packages[importPath]...)
	}
	return imported
}

// pythonImports resolves a Python file's imports to the modules this snapshot
// owns. A relative import (from .util import helper) names its package only
// relative to a file the parser is not given, so it stays unresolved rather
// than guessed at.
var pythonImportsRE = regexp.MustCompile(`(?m)^\s*(?:from|import)\s+([\w.]+)`)

func pythonImports(data []byte, modules map[string][]string) []string {
	var imported []string
	for _, match := range pythonImportsRE.FindAllStringSubmatch(string(data), -1) {
		imported = append(imported, modules[match[1]]...)
	}
	return imported
}

// jsImports resolves a JavaScript or TypeScript file's relative specifiers to
// files inside the snapshot. A bare specifier names a package, and a package
// is not a file this snapshot owns, so only "./" and "../" are resolved.
func jsImports(fsys fs.FS, file string, data []byte) []string {
	source := string(data)
	var imported []string
	for _, pattern := range []*regexp.Regexp{jsFromRE, jsSideRE, jsRequireRE} {
		for _, match := range pattern.FindAllStringSubmatch(source, -1) {
			specifier := match[1]
			if !strings.HasPrefix(specifier, "./") && !strings.HasPrefix(specifier, "../") {
				continue
			}
			if resolved := resolveJS(fsys, file, specifier); resolved != "" {
				imported = appendUnique(imported, resolved)
			}
		}
	}
	return imported
}

var (
	jsFromRE    = regexp.MustCompile(`(?m)\bfrom\s+['"]([^'"]+)['"]`)
	jsSideRE    = regexp.MustCompile(`(?m)\bimport\s+['"]([^'"]+)['"]`)
	jsRequireRE = regexp.MustCompile(`(?m)\b(?:require|import)\s*\(\s*['"]([^'"]+)['"]\s*\)`)
)

// jsExtensions are the extensions a specifier is probed with when it names no
// file of its own.
var jsExtensions = []string{".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs"}

// resolveJS turns one relative specifier into a file of the snapshot. An empty
// answer means the specifier names nothing here: a probe that fails is a
// missing file, not a failure of the review.
func resolveJS(fsys fs.FS, from, specifier string) string {
	base := path.Clean(path.Join(path.Dir(from), specifier))
	for _, candidate := range jsCandidates(base) {
		if info, err := fs.Stat(fsys, candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

// jsCandidates are the paths one specifier may name: the file itself, the file
// with each extension the language allows, and a directory's index file.
func jsCandidates(base string) []string {
	candidates := make([]string, 0, 1+2*len(jsExtensions))
	candidates = append(candidates, base)
	for _, extension := range jsExtensions {
		candidates = append(candidates, base+extension)
	}
	for _, extension := range jsExtensions {
		candidates = append(candidates, path.Join(base, "index"+extension))
	}
	return candidates
}
