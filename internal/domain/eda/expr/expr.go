// Package expr is the CEL expression environment for the EDA playbook engine:
// one mechanism for both an action's `when` condition and its `args` values,
// per eda-playbook-engine.md's resolved open question 1 (CEL decision).
//
// The environment declares two context roots -- `event` (the triggering
// event's decoded payload) and `actions` (prior actions' results keyed by the
// action's declared id, `actions.<id>.result.<field>`) -- and applies a cost
// limit to every program. The same compile path serves the playbook loader
// (reject-at-load) and the lint tool, so author-time diagnostics and runtime
// evaluation cannot disagree.
//
// CEL is non-Turing-complete, side-effect-free, and panic-free by design (no
// recover() wrapper is needed; verified in the t2db.14 acceptance tests
// against hostile data). Anything the schema does not accept is a returned
// error here -- the reject-at-load philosophy of the parent design doc.
package expr

import (
	"fmt"
	"sort"

	"cel.dev/cel-go/cel"
	celast "cel.dev/cel-go/common/ast"
)

// DefaultCostLimit bounds evaluation of every playbook expression (J5 in the
// resolved doc: tunable, start here; linter and daemon share the same value).
const DefaultCostLimit = 100_000

// Env is the CEL environment for playbook expressions: the declared context
// roots and the default cost limit applied to every compiled program.
type Env struct {
	celEnv    *cel.Env
	costLimit uint64
}

// Context is what an expression may read at dispatch time. Both maps are
// read-only from the expression's perspective; CEL enforces this.
type Context struct {
	// Event is the triggering event's decoded payload (webhook body, forge
	// issue, schedule tick). Declared dyn because its shape is unknown by
	// design (schema-by-example); field-level typing is follow-up work once
	// multi-action playbooks exist (per-kind generated Result structs
	// declared as CEL types).
	Event map[string]any
	// Actions holds prior actions' results keyed by the action's id as
	// declared in the playbook. Empty today (single-action playbooks have
	// no prior actions); kept dyn for the same reason as Event.
	Actions map[string]map[string]any
}

// NewEnv builds the CEL environment with the two context roots and the
// default cost limit.
func NewEnv() *Env {
	celEnv, err := cel.NewEnv(
		cel.Variable("event", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("actions", cel.MapType(cel.StringType, cel.DynType)),
	)
	if err != nil {
		// cel.NewEnv with static variable declarations cannot fail in
		// practice; a failure here is a programming error, not a playbook
		// error. Panic is appropriate (package init-style invariant).
		panic(err)
	}
	return &Env{celEnv: celEnv, costLimit: DefaultCostLimit}
}

// Compile parses and type-checks a playbook expression string against the
// declared context. A syntax error, an unknown root (anything other than
// event/actions), a type error, or a non-literal `actions` index
// (`actions[event.name]`, `actions["a" + "b"]`) is a returned error -- never
// a panic. The returned Program is safe to evaluate concurrently (CEL
// programs are stateless once compiled).
//
// This is the reject-at-load entry point: the playbook loader and the lint
// tool call this at load time, so a bad expression drops the playbook with a
// reported error rather than failing at dispatch.
func (e *Env) Compile(src string) (*Program, error) {
	ast, issues := e.celEnv.Compile(src)
	if issues != nil && issues.Err() != nil {
		return nil, issues.Err()
	}
	prg, err := e.celEnv.Program(ast, cel.CostLimit(e.costLimit))
	if err != nil {
		return nil, err
	}
	ids, err := referencedActionIDs(ast)
	if err != nil {
		return nil, err
	}
	return &Program{prg: prg, actionIDs: ids}, nil
}

// referencedActionIDs walks the compiled AST and returns the sorted, de-
// duplicated set of action ids an expression reads from `actions`. Both
// field-selection (`actions.notify`) and literal map-index
// (`actions["notify"]`) forms are collected; `actions` is declared
// map(string,dyn), so CEL type-checking cannot reject an unknown id, and the
// playbook loader compares this set against the ids of prior actions to
// reject unknown references at load (J1 in
// docs/prds/playbook-expression-syntax.md).
//
// A non-literal index key (`actions[key]`, `actions[event.name]`,
// `actions["a" + "b"]`) is rejected here with an error: its id cannot be
// resolved statically, so it cannot be checked against declared prior-action
// ids and must not pass the load check as a runtime miss.
func referencedActionIDs(ast *cel.Ast) ([]string, error) {
	if ast == nil || ast.NativeRep() == nil {
		return nil, nil
	}
	seen := map[string]struct{}{}
	var walkErr error
	visitor := celast.NewExprVisitor(func(e celast.Expr) {
		if walkErr != nil {
			return
		}
		switch e.Kind() {
		case celast.SelectKind:
			sel := e.AsSelect()
			if isActionsIdent(sel.Operand()) {
				seen[sel.FieldName()] = struct{}{}
			}
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
				walkErr = fmt.Errorf("non-literal actions index key (want a string literal)")
				return
			}
			id, ok := key.AsLiteral().Value().(string)
			if !ok {
				walkErr = fmt.Errorf("non-literal actions index key (want a string literal)")
				return
			}
			seen[id] = struct{}{}
		}
	})
	celast.PreOrderVisit(ast.NativeRep().Expr(), visitor)
	if walkErr != nil {
		return nil, walkErr
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// isActionsIdent reports whether e is the `actions` context-root identifier.
func isActionsIdent(e celast.Expr) bool {
	return e.Kind() == celast.IdentKind && e.AsIdent() == "actions"
}

// Program is a compiled, cost-limited playbook expression.
type Program struct {
	prg       cel.Program
	actionIDs []string
}

// ReferencedActionIDs returns the sorted, de-duplicated action ids the
// expression reads from `actions` (either `actions.<id>` or
// `actions["<id>"]`). It is empty when the expression reads no prior-action
// result. A non-literal index (`actions[event.name]`) never reaches this
// method: it is rejected at Compile.
func (p *Program) ReferencedActionIDs() []string {
	if p == nil {
		return nil
	}
	return p.actionIDs
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
