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
// event/actions), or a type error is a returned error -- never a panic. The
// returned Program is safe to evaluate concurrently (CEL programs are
// stateless once compiled).
//
// Compile does not decide whether every `actions` read can be pinned to a
// prior action id at load; that classification is Program.ActionReferences'
// job, and the playbook loader rejects a program whose ActionReferences
// reports unresolvable. The split keeps author-time diagnostics and runtime
// evaluation on the same compiled artifact.
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
