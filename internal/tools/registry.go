package tools

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path"
	"strconv"
	"sync"
)

// ErrDuplicateTool is returned by [Registry.Register] when a tool with the
// same name has already been registered.
var ErrDuplicateTool = errors.New("duplicate tool name")

// defaultRegistry is the package-level singleton.
var defaultRegistry = NewRegistry()

// DefaultRegistry returns the package-level singleton registry. Most
// callers should use this rather than creating their own.
func DefaultRegistry() *Registry {
	return defaultRegistry
}

// Registry is a thread-safe tool registry. It maps tool names to
// [ToolEntry] values and supports lookup by toolset and availability.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]ToolEntry
}

// NewRegistry creates a new, empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]ToolEntry),
	}
}

// Register adds a tool entry to the registry. It returns an error if the
// entry fails [ToolEntry.Validate], or if a tool with the same name is
// already registered (wrapping [ErrDuplicateTool]).
func (r *Registry) Register(e ToolEntry) error {
	if err := e.Validate(); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tools[e.Name]; exists {
		return fmt.Errorf("%w: %q", ErrDuplicateTool, e.Name)
	}

	// Defensive copy  --  sever shared Schema/RequiresEnv references so the
	// caller cannot mutate them and race with concurrent readers.
	r.tools[e.Name] = e.Clone()
	return nil
}

// All returns all registered tool entries. The returned slice is a
// snapshot  --  mutating it does not affect the registry.
func (r *Registry) All() []ToolEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]ToolEntry, 0, len(r.tools))
	for _, e := range r.tools {
		out = append(out, e.Clone())
	}
	return out
}

// ByToolset returns tools belonging to the given toolset. Pass an empty
// string to match tools that have no toolset.
func (r *Registry) ByToolset(toolset string) []ToolEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var out []ToolEntry
	for _, e := range r.tools {
		if e.Toolset == toolset {
			out = append(out, e.Clone())
		}
	}
	return out
}

// Available returns tools whose [ToolEntry.Available] method returns true.
//
// CheckFn is evaluated outside the registry lock to avoid deadlock if
// the function re-enters the registry (e.g. calling Register or All).
func (r *Registry) Available() []ToolEntry {
	r.mu.RLock()
	snapshot := make([]ToolEntry, 0, len(r.tools))
	for _, e := range r.tools {
		snapshot = append(snapshot, e.Clone())
	}
	r.mu.RUnlock()

	// Evaluate CheckFn outside the lock  --  no deadlock risk.
	var out []ToolEntry
	for _, e := range snapshot {
		if e.Available() {
			out = append(out, e)
		}
	}
	return out
}

// Discover scans a directory of Go source files for Register calls
// that pass a [ToolEntry] composite literal. It uses [go/parser] and
// [go/ast] to statically analyse source files without executing them.
//
// Discover returns the number of Register calls found. Function fields
// (Handler, CheckFn, DynamicSchemaOverrides) cannot be resolved from
// static source analysis and remain nil. Entries that fail
// [ToolEntry.Validate]  --  typically because Handler is nil  --  are
// counted but not registered.
//
// Parse errors in individual files do not stop the scan; they are
// accumulated and returned as a joined error alongside the count of
// Register calls found in successfully-parsed files.
func (r *Registry) Discover(fsys fs.FS, dir string) (int, error) {
	entries, err := fs.Glob(fsys, path.Join(dir, "*.go"))
	if err != nil {
		return 0, err
	}

	count := 0
	var errs []error
	for _, filePath := range entries {
		src, err := fs.ReadFile(fsys, filePath)
		if err != nil {
			errs = append(errs, fmt.Errorf("read %s: %w", filePath, err))
			continue
		}

		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, filePath, src, parser.ParseComments)
		if err != nil {
			errs = append(errs, fmt.Errorf("parse %s: %w", filePath, err))
			continue
		}

		count += countRegisterCalls(f)

		for _, entry := range extractToolEntries(f) {
			// Best-effort registration: only entries that pass
			// Validate (have Name + Handler) are registered.
			_ = r.Register(entry)
		}
	}

	return count, errors.Join(errs...)
}

// countRegisterCalls counts the number of Register method calls in a Go AST.
func countRegisterCalls(f *ast.File) int {
	count := 0
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if isRegisterCall(call) {
			count++
		}
		return true
	})
	return count
}

// extractToolEntries walks the AST and extracts [ToolEntry] composite
// literals from Register calls.
func extractToolEntries(f *ast.File) []ToolEntry {
	var entries []ToolEntry
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if !isRegisterCall(call) {
			return true
		}
		if len(call.Args) == 0 {
			return true
		}
		comp, ok := call.Args[0].(*ast.CompositeLit)
		if !ok {
			return true
		}
		entries = append(entries, toolEntryFromCompositeLit(comp))
		return true
	})
	return entries
}

// isRegisterCall reports whether call is a .Register(...) method call on
// a *Registry receiver. It accepts common patterns:
//
//   - DefaultRegistry().Register(...)
//   - tools.DefaultRegistry().Register(...)
//   - r.Register(...)  where r is a known registry variable
func isRegisterCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if sel.Sel.Name != "Register" {
		return false
	}

	// The receiver must be a call to DefaultRegistry, with or without
	// a package qualifier (e.g. DefaultRegistry() or tools.DefaultRegistry()).
	rcv, ok := sel.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	return callsDefaultRegistry(rcv)
}

// callsDefaultRegistry reports whether call is a DefaultRegistry()
// invocation, with or without a package qualifier.
func callsDefaultRegistry(call *ast.CallExpr) bool {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name == "DefaultRegistry"
	case *ast.SelectorExpr:
		// pkg.DefaultRegistry()
		return fn.Sel.Name == "DefaultRegistry"
	default:
		return false
	}
}

// toolEntryFromCompositeLit extracts a [ToolEntry] from an AST composite
// literal. Only basic literal values (strings, bools, ints) are extracted;
// function references are ignored.
func toolEntryFromCompositeLit(lit *ast.CompositeLit) ToolEntry {
	var e ToolEntry
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch key.Name {
		case "Name":
			e.Name = stringLit(kv.Value)
		case "Toolset":
			e.Toolset = stringLit(kv.Value)
		case "Description":
			e.Description = stringLit(kv.Value)
		case "Emoji":
			e.Emoji = stringLit(kv.Value)
		case "IsAsync":
			e.IsAsync = boolLit(kv.Value)
		case "MaxResultSizeChars":
			e.MaxResultSizeChars = intLit(kv.Value)
		case "RequiresEnv":
			e.RequiresEnv = stringSliceLit(kv.Value)
		case "Schema":
			e.Schema = schemaLit(kv.Value)
		}
	}
	return e
}

// --- AST value extractors ---

func stringLit(expr ast.Expr) string {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	// strconv.Unquote handles double-quoted, backtick-delimited (raw),
	// and interpreted string literals with escape sequences.
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}
	return s
}

func boolLit(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "true"
}

func intLit(expr ast.Expr) int {
	// Handle negative literals: the AST represents -5 as
	// UnaryExpr{Op: token.SUB, X: BasicLit{Kind: INT, Value: "5"}}.
	if un, ok := expr.(*ast.UnaryExpr); ok && un.Op == token.SUB {
		lit, ok := un.X.(*ast.BasicLit)
		if !ok || lit.Kind != token.INT {
			return 0
		}
		v, err := strconv.ParseInt(lit.Value, 0, 0)
		if err != nil {
			return 0
		}
		return -int(v)
	}

	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.INT {
		return 0
	}
	// base=0 auto-detects decimal, hex (0x), octal (0o), binary (0b).
	v, err := strconv.ParseInt(lit.Value, 0, 0)
	if err != nil {
		return 0
	}
	return int(v)
}

func stringSliceLit(expr ast.Expr) []string {
	comp, ok := expr.(*ast.CompositeLit)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(comp.Elts))
	for _, elt := range comp.Elts {
		if s := stringLit(elt); s != "" || isStringLit(elt) {
			out = append(out, s)
		}
	}
	return out
}

// isStringLit reports whether expr is a string literal (including empty).
func isStringLit(expr ast.Expr) bool {
	lit, ok := expr.(*ast.BasicLit)
	return ok && lit.Kind == token.STRING
}

func schemaLit(expr ast.Expr) JSONSchema {
	comp, ok := expr.(*ast.CompositeLit)
	if !ok {
		return nil
	}
	s := make(JSONSchema)
	for _, elt := range comp.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key := schemaKey(kv.Key)
		if key == "" {
			continue
		}
		if v := schemaValue(kv.Value); v != nil {
			s[key] = v
		}
	}
	if len(s) == 0 {
		return nil
	}
	return s
}

// schemaKey extracts a key string from a schema literal key expression.
// Handles string literals and identifier names (e.g. Type: "object").
func schemaKey(expr ast.Expr) string {
	if s := stringLit(expr); s != "" {
		return s
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

// schemaValue extracts a value from a schema literal, handling strings,
// ints, bools, nested maps, and arrays of strings.
func schemaValue(expr ast.Expr) any {
	switch v := expr.(type) {
	case *ast.BasicLit:
		switch v.Kind {
		case token.STRING:
			return stringLit(v)
		case token.INT:
			return intLit(v)
		default:
			return nil
		}
	case *ast.Ident:
		if v.Name == "true" {
			return true
		}
		if v.Name == "false" {
			return false
		}
		return nil
	case *ast.CompositeLit:
		// Check if it's a slice/array (no key-value pairs) or a map.
		if isArrayLit(v) {
			return schemaArrayLit(v)
		}
		return schemaLit(v)
	default:
		return nil
	}
}

// isArrayLit reports whether a composite literal looks like a slice/array
// (elements without keys) rather than a map.
func isArrayLit(lit *ast.CompositeLit) bool {
	if len(lit.Elts) == 0 {
		return false
	}
	_, ok := lit.Elts[0].(*ast.KeyValueExpr)
	return !ok
}

// schemaArrayLit extracts a []any from an array composite literal.
func schemaArrayLit(lit *ast.CompositeLit) []any {
	var out []any
	for _, elt := range lit.Elts {
		if v := schemaValue(elt); v != nil {
			out = append(out, v)
		}
	}
	return out
}
