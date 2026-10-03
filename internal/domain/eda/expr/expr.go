// Package expr is the CEL environment for EDA playbook `when` and `args`
// expressions, with an `event` root and a per-playbook typed `actions` root.
package expr

import (
	"reflect"
	"sort"
	"strings"

	"cel.dev/cel-go/cel"
	celast "cel.dev/cel-go/common/ast"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
)

// DefaultCostLimit bounds evaluation of every playbook expression (J5 in the
// resolved doc: tunable, start here; linter and daemon share the same value).
const DefaultCostLimit = 100_000

// DeclaredResult is a prior action's id and Result type, read as
// actions.<ID>.result.<field>.
type DeclaredResult struct {
	ID   string
	Type reflect.Type
}

// probeResult is the placeholder Result type used by IsCELFieldName's probe
// environment. It is a struct only so it satisfies the native type registry;
// its fields are never read.
type probeResult struct{}

// IsCELFieldName reports whether id can be written as actions.<id> in CEL.
func IsCELFieldName(id string) bool {
	if id == "" {
		return false
	}
	env := NewEnv(DeclaredResult{ID: id, Type: reflect.TypeFor[probeResult]()})
	_, err := env.Compile("actions." + id)
	return err == nil
}

// Env is the CEL environment for playbook expressions: the declared context
// roots and the default cost limit applied to every compiled program.
type Env struct {
	celEnv    *cel.Env
	costLimit uint64
}

// Context is what an expression may read at dispatch time. Both roots are
// read-only from the expression's perspective; CEL enforces this.
type Context struct {
	// Event is the triggering event's payload, typed dyn.
	Event map[string]any
	// Actions holds prior actions' results by id as {"result": <Result>}.
	Actions map[string]map[string]any
}

// NewEnv builds a CEL environment whose `actions` root is an object type with
// one field per declared result. An empty declared set is valid: `actions` is
// then an object with no fields, and any `actions.<id>` read fails at compile
// time. `event` stays dyn (J4).
func NewEnv(declared ...DeclaredResult) *Env {
	reg, err := newResultRegistry(declared)
	if err != nil {
		// NewEnv with an unsupported Result type (not a struct) is a
		// programming error, not a playbook error. Panic is appropriate
		// (package init-style invariant).
		panic(err)
	}
	provider := newActionResultProvider(declared, reg)
	celEnv, err := cel.NewEnv(
		cel.CustomTypeAdapter(reg),
		cel.CustomTypeProvider(provider),
		cel.Variable("event", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("actions", provider.actionsType()),
	)
	if err != nil {
		// cel.NewEnv with static variable declarations cannot fail in
		// practice; a failure here is a programming error, not a playbook
		// error. Panic is appropriate.
		panic(err)
	}
	return &Env{celEnv: celEnv, costLimit: DefaultCostLimit}
}

// newResultRegistry registers the declared Result types with lower-cased
// field names.
func newResultRegistry(declared []DeclaredResult) (*types.Registry, error) {
	items := make([]any, 0, len(declared)+1)
	items = append(items, types.ParseStructField(lowerFieldName))
	for _, d := range declared {
		items = append(items, d.Type)
	}
	return types.NewRegistry(items...)
}

// lowerFieldName maps a Go struct field to its CEL name: the lower-cased
// field name (Written -> written, Level -> level).
func lowerFieldName(f reflect.StructField) string {
	return strings.ToLower(f.Name)
}

// nativeResultTypeName returns the CEL name for a Go type, e.g. log.Result.
func nativeResultTypeName(t reflect.Type) string {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	pkg := t.PkgPath()
	if i := strings.LastIndexByte(pkg, '/'); i >= 0 {
		pkg = pkg[i+1:]
	}
	return pkg + "." + t.Name()
}

// Type names for the actions root and per-id wrappers.
const (
	actionsTypeName  = "actions"
	actionTypePrefix = "eda.action."
	resultFieldName  = "result"
)

// actionResultProvider is the data-driven types.Provider for the
// per-playbook `actions` object. It owns the `actions` root and each
// `eda.action.<id>` wrapper; every other type name (including the native
// Result structs) is delegated to the native registry.
type actionResultProvider struct {
	ids      []string
	resultOf map[string]string
	base     *types.Registry
}

// newActionResultProvider builds the provider from the declared results,
// de-duplicating and sorting ids so field-name reporting is deterministic.
func newActionResultProvider(declared []DeclaredResult, base *types.Registry) *actionResultProvider {
	p := &actionResultProvider{
		ids:      make([]string, 0, len(declared)),
		resultOf: make(map[string]string, len(declared)),
		base:     base,
	}
	for _, d := range declared {
		if _, seen := p.resultOf[d.ID]; seen {
			continue
		}
		p.resultOf[d.ID] = nativeResultTypeName(d.Type)
		p.ids = append(p.ids, d.ID)
	}
	sort.Strings(p.ids)
	return p
}

func (p *actionResultProvider) actionsType() *types.Type {
	return types.NewObjectType(actionsTypeName)
}

func (p *actionResultProvider) wrapperType(id string) string {
	return actionTypePrefix + id
}

// EnumValue implements types.Provider.
func (p *actionResultProvider) EnumValue(enumName string) ref.Val {
	return p.base.EnumValue(enumName)
}

// FindIdent implements types.Provider.
func (p *actionResultProvider) FindIdent(identName string) (ref.Val, bool) {
	return p.base.FindIdent(identName)
}

// FindStructType implements types.Provider.
func (p *actionResultProvider) FindStructType(structType string) (*types.Type, bool) {
	if structType == actionsTypeName {
		return types.NewTypeTypeWithParam(types.NewObjectType(structType)), true
	}
	for _, id := range p.ids {
		if structType == p.wrapperType(id) {
			return types.NewTypeTypeWithParam(types.NewObjectType(structType)), true
		}
	}
	return p.base.FindStructType(structType)
}

// FindStructFieldNames implements types.Provider.
func (p *actionResultProvider) FindStructFieldNames(structType string) ([]string, bool) {
	if structType == actionsTypeName {
		return append([]string(nil), p.ids...), true
	}
	for _, id := range p.ids {
		if structType == p.wrapperType(id) {
			return []string{resultFieldName}, true
		}
	}
	return p.base.FindStructFieldNames(structType)
}

// FindStructFieldType implements types.Provider.
func (p *actionResultProvider) FindStructFieldType(structType, fieldName string) (*types.FieldType, bool) {
	if structType == actionsTypeName {
		for _, id := range p.ids {
			if fieldName == id {
				return &types.FieldType{Type: types.NewObjectType(p.wrapperType(id))}, true
			}
		}
		return nil, false
	}
	for _, id := range p.ids {
		if structType == p.wrapperType(id) && fieldName == resultFieldName {
			return &types.FieldType{Type: types.NewObjectType(p.resultOf[id])}, true
		}
	}
	return p.base.FindStructFieldType(structType, fieldName)
}

// NewValue implements types.Provider.
func (p *actionResultProvider) NewValue(structType string, fields map[string]ref.Val) ref.Val {
	return p.base.NewValue(structType, fields)
}

// Compile parses and type-checks an expression. Programs are safe for
// concurrent use.
func (e *Env) Compile(src string) (*Program, error) {
	ast, issues := e.celEnv.Compile(src)
	if issues != nil && issues.Err() != nil {
		return nil, issues.Err()
	}
	prg, err := e.celEnv.Program(ast, cel.CostLimit(e.costLimit))
	if err != nil {
		return nil, err
	}
	ids, resolvable := actionReferences(ast)
	return &Program{prg: prg, actionIDs: ids, resolvable: resolvable, outType: ast.OutputType()}, nil
}

// actionReferences returns the sorted action ids the expression reads and
// whether every read of `actions` is a field selection.
func actionReferences(ast *cel.Ast) ([]string, bool) {
	if ast == nil || ast.NativeRep() == nil {
		return nil, true
	}
	checked := ast.NativeRep()
	isRoot := func(e celast.Expr) bool {
		return e.Kind() == celast.IdentKind && checked.GetType(e.ID()).TypeName() == actionsTypeName
	}
	var identCount, staticCount int
	seen := map[string]struct{}{}
	visitor := celast.NewExprVisitor(func(e celast.Expr) {
		switch {
		case isRoot(e):
			identCount++
		case e.Kind() == celast.SelectKind && isRoot(e.AsSelect().Operand()):
			staticCount++
			seen[e.AsSelect().FieldName()] = struct{}{}
		}
	})
	celast.PreOrderVisit(checked.Expr(), visitor)
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, identCount == staticCount
}

// Program is a compiled, cost-limited playbook expression.
type Program struct {
	prg        cel.Program
	actionIDs  []string
	resolvable bool
	outType    *cel.Type
}

// Fits reports whether the program's checked output type can fill a Go value
// of type t. A dyn output fits anything: its type is known only per
// evaluation. A Go type with no CEL scalar counterpart is not checked here.
func (p *Program) Fits(t reflect.Type) bool {
	if p.outType == nil || p.outType.Kind() == types.DynKind {
		return true
	}
	want, ok := celScalar(t)
	return !ok || want.IsExactType(p.outType)
}

// OutputType is the program's checked output type, for error messages.
func (p *Program) OutputType() string {
	if p.outType == nil {
		return "dyn"
	}
	return p.outType.String()
}

// celScalar maps a Go scalar kind to the CEL type an expression must produce
// to fill it.
func celScalar(t reflect.Type) (*cel.Type, bool) {
	switch t.Kind() {
	case reflect.String:
		return cel.StringType, true
	case reflect.Bool:
		return cel.BoolType, true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return cel.IntType, true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return cel.UintType, true
	case reflect.Float32, reflect.Float64:
		return cel.DoubleType, true
	default:
		return nil, false
	}
}

// ActionReferences returns the action ids the expression reads and whether
// each `actions` read resolves to an id.
func (p *Program) ActionReferences() (ids []string, resolvable bool) {
	if p == nil {
		return nil, true
	}
	return p.actionIDs, p.resolvable
}

// Eval evaluates the program and returns the native Go value.
func (e *Env) Eval(prg *Program, ctx Context) (any, error) {
	data := map[string]any{
		"event":   ctx.Event,
		"actions": ctx.Actions,
	}
	v, _, err := prg.prg.Eval(data)
	if err != nil {
		return nil, err
	}
	return v.Value(), nil
}
