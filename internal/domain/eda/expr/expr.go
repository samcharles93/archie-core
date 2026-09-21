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
// object type (multi-action-playbooks.md, D3). The same compile path serves
// the playbook loader (reject-at-load) and the lint tool, so author-time
// diagnostics and runtime evaluation cannot disagree.
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
// kind's Go Result struct (the value the run marshals into the per-id
// `{result: ...}` map before a later expression reads it).
type DeclaredResult struct {
	ID   string
	Type reflect.Type
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
	// `{"result": <KindResult struct>}` the run produces, so a later
	// expression reads `actions.<id>.result.<field>`.
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
	return &Program{prg: prg, actionIDs: ids, resolvable: resolvable}, nil
}

// actionReferences walks the compiled AST once and classifies every read of
// the `actions` context root as either a statically-resolvable action id or
// not. The invariant is exhaustive by construction: each read of `actions`
// consumes exactly one `actions` identifier node, so counting identifier
// nodes and counting the reads that match one of the two static access
// shapes (a field selection on the `actions` ident, or a map index whose key
// is a string literal) yields
//
//	resolvable = (identCount == staticCount)
//
// Any other spelling that mentions `actions` contributes an identifier node
// without a matching static access, so it reports unresolvable rather than
// slipping through as a runtime miss. The returned ids are the sorted,
// de-duplicated ids of the static accesses, for the playbook loader's
// unknown-id check.
//
// With the per-playbook object type the checker already rejects every
// non-static `actions` read at compile time, so a program that reaches this
// walk is resolvable by construction; the walk is retained until the next
// pass removes it together with the loader's duplicate unknown-id check.
func actionReferences(ast *cel.Ast) ([]string, bool) {
	if ast == nil || ast.NativeRep() == nil {
		return nil, true
	}
	var identCount, staticCount int
	seen := map[string]struct{}{}
	visitor := celast.NewExprVisitor(func(e celast.Expr) {
		switch e.Kind() {
		case celast.IdentKind:
			if e.AsIdent() == "actions" {
				identCount++
			}
		case celast.SelectKind:
			sel := e.AsSelect()
			if !isActionsIdent(sel.Operand()) {
				return
			}
			staticCount++
			seen[sel.FieldName()] = struct{}{}
		case celast.CallKind:
			call := e.AsCall()
			if call.FunctionName() != "_[_]" || len(call.Args()) != 2 {
				return
			}
			if !isActionsIdent(call.Args()[0]) {
				return
			}
			key := call.Args()[1]
			if key.Kind() != celast.LiteralKind {
				return
			}
			id, ok := key.AsLiteral().Value().(string)
			if !ok {
				return
			}
			staticCount++
			seen[id] = struct{}{}
		}
	})
	celast.PreOrderVisit(ast.NativeRep().Expr(), visitor)
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, identCount == staticCount
}

// isActionsIdent reports whether e is the `actions` context-root identifier.
func isActionsIdent(e celast.Expr) bool {
	return e.Kind() == celast.IdentKind && e.AsIdent() == "actions"
}

// Program is a compiled, cost-limited playbook expression.
type Program struct {
	prg        cel.Program
	actionIDs  []string
	resolvable bool
}

// ActionReferences reports the action ids the expression reads from the
// `actions` context root (sorted, de-duplicated) and whether every `actions`
// read could be statically resolved to one of those ids. ids is empty when
// the expression reads no prior-action result; resolvable is false when the
// expression reads `actions` in any form that cannot be pinned to a prior
// action id at load. The playbook loader rejects a non-resolvable program
// rather than evaluating it as a runtime miss.
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
