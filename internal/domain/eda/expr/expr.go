// Package expr is the CEL expression environment for the EDA playbook engine:
// one mechanism for both an action's `when` condition and its `args` values,
// per eda-playbook-engine.md's resolved open question 1 (CEL decision).
//
// The environment declares two context roots -- `event` (the triggering
// event's decoded payload, kept dyn by J4) and `actions` (prior actions'
// results keyed by the action's declared id) -- and applies a cost limit to
// every program. `actions` is not a global map: its shape depends on the ids
// one playbook declares and the kind each id runs, so NewEnv takes the
// declared ids and their Go Result struct types and builds a per-playbook
// object type (multi-action-playbooks.md, D3). The playbook loader compiles
// every `when` and `args` value here, so load-time rejection and runtime
// evaluation cannot disagree; the standalone lint command covers the flat
// kind/label binding files, not rich EDA playbook documents.
//
// CEL is non-Turing-complete, side-effect-free, and panic-free by design (no
// recover() wrapper is needed; verified in the t2db.14 acceptance tests
// against hostile data). Anything the schema does not accept is a returned
// error here -- the reject-at-load philosophy of the parent design doc.
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

// DeclaredResult is one prior action's result the environment exposes to a
// later expression. ID is the action's declared id, read as
// `actions.<ID>.result.<field>`; Type is the reflect.Type of that action
// kind's Go Result struct. `module.ModuleRegistry.DecodeResult` is the site
// that converts Invoke's flat result map back into this struct before a later
// expression reads it.
type DeclaredResult struct {
	ID   string
	Type reflect.Type
}

// probeResult is the placeholder Result type used by IsCELFieldName's probe
// environment. It is a struct only so it satisfies the native type registry;
// its fields are never read.
type probeResult struct{}

// IsCELFieldName reports whether id can be written as `actions.<id>` in a CEL
// expression. It is the authoritative check the playbook loader uses for a
// module action id, replacing a regex approximation of CEL's token rules: it
// compiles a probe expression against a one-field environment declaring the
// id, so the CEL parser and checker decide. CEL keywords (`in`, `true`,
// `false`, `null`) and any other spelling with no field-selection form return
// false; a valid CEL identifier such as `Build` returns true (the loader adds
// its own lowercase policy on top).
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
	// Event is the triggering event's decoded payload (webhook body, forge
	// issue, schedule tick). Declared dyn because its shape is unknown by
	// design (schema-by-example); field-level typing is follow-up work once
	// multi-action playbooks exist (per-kind generated Result structs
	// declared as CEL types).
	Event map[string]any
	// Actions holds prior actions' results keyed by the action's id as
	// declared in the playbook. Each id maps to the per-id wrapper
	// `{"result": <KindResult struct>}` built by
	// `module.ModuleRegistry.DecodeResult`, so a later expression reads
	// `actions.<id>.result.<field>`.
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

// newResultRegistry registers every declared Result reflect.Type with CEL's
// native type system, using lower-cased Go field names so `Written` is read
// as `written`. The registry is both the provider (for the Result struct
// fields) and the adapter (so a Go Result struct value inside the eval map
// adapts to those same fields).
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

// nativeResultTypeName is the CEL type name cel-go's native type system
// assigns to a reflect.Type (simple package alias + struct name), e.g.
// `log.Result`. It must match the name the native registry registers, because
// the per-id wrapper's `result` field is typed by that name and its fields
// are resolved through the native registry.
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

// The `actions` root object type name and the per-id wrapper type namespace.
// Per-id names must live in a reserved namespace (eda.action.<id>): naming a
// wrapper `actions.<id>` makes the checker parse `actions.<id>` as a type
// reference rather than a field selection, which then fails with "type ...
// does not support field selection".
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

// Compile parses and type-checks a playbook expression string against the
// declared context. A syntax error, an unknown root (anything other than
// event/actions), a dynamic `actions` read, or a field typo on a declared
// result is a returned error -- never a panic. The returned Program is safe
// to evaluate concurrently (CEL programs are stateless once compiled).
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

// actionReferences walks the checked AST once and classifies every read of
// the `actions` context root as either a statically-resolvable action id or
// not. A root read is an identifier the checker typed as the `actions`
// object: matching on type rather than name keeps a comprehension variable
// that happens to be called `actions` out of the count. Each root read
// consumes exactly one such identifier, so
//
//	resolvable = (identCount == staticCount)
//
// where staticCount is the root reads that are a field selection. The
// checker already rejects every other non-static read (a dynamic index,
// `in`, `size`, an undeclared id); the spelling it accepts is a bare
// `actions` value, which compiles but cannot be pinned to an action id at
// load. This walk exists to reject that spelling; the ids return is
// informational. The ids are sorted and de-duplicated.
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

// ActionReferences reports the action ids the expression reads from the
// `actions` context root (sorted, de-duplicated) and whether every `actions`
// read could be statically resolved to one of those ids. ids is empty when
// the expression reads no prior-action result; resolvable is false when the
// expression reads `actions` in any form that cannot be pinned to a prior
// action id at load. Under the per-playbook object type the checker already
// rejects every dynamic `actions` read, so the only form that still reaches
// here unresolved is a bare `actions` value read; the playbook loader rejects
// that rather than evaluating it as a runtime miss.
func (p *Program) ActionReferences() (ids []string, resolvable bool) {
	if p == nil {
		return nil, true
	}
	return p.actionIDs, p.resolvable
}

// Eval evaluates the program against a dispatch-time context. A missing
// field, a wrong type against a dyn value, or an exceeded cost limit is a
// returned error. CEL's own evaluation recovers panics internally (verified
// in cel-go's program.go: Eval wraps evaluation in recover), and its
// returned values implement ref.Val -- unwrapped here to the native Go value
// the caller expects.
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
