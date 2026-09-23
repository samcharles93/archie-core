// Package playbook is the EDA playbook document type and its event
// coordinator: the rich trigger+actions YAML shape (docs/prds/
// eda-playbook-engine.md) with CEL `when` conditions and `args` values
// (open question 1, resolved to CEL). A playbook is one of two shapes
// (multi-action-playbooks.md, D2):
//
//   - a workflow playbook is exactly one `workflow` action, unchanged from
//     the original boundary, and is routed by the daemon's definition pin;
//   - an action playbook is one or more `module` actions in order, each with
//     a registered `kind`, `args`, and an optional `when`/`id`. Store.Run
//     executes it (docs/prds/action-playbook-run.md).
//
// The two shapes never mix in one playbook. This is an ADDITIONAL routing
// source alongside the flat kind/label binding files (t2db.9/.10/.11): the
// daemon consults a matching workflow playbook before those bindings when it
// pins a task's workflow definition (t2db.23). The binding loaders themselves
// are untouched.
package playbook

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/samcharles93/archie-core/internal/domain/eda/expr"
	"github.com/samcharles93/archie-core/internal/domain/stableid"
	"github.com/samcharles93/archie-core/internal/domain/workintake"
)

// KindSchemas is the narrow kind-to-schema source the loader consults to
// type-check an action playbook: each module kind's hand-written Args and
// Result struct types. *module.ModuleRegistry satisfies it, so the playbook
// package grows no import of the module package.
type KindSchemas interface {
	// KindSchema reports the Args and Result reflect types for a known kind;
	// ok is false when kind is not a registered module kind.
	KindSchema(kind string) (args, result reflect.Type, ok bool)
}

// Modules is the module source an action playbook loads and runs against:
// the kinds' schemas, the invoke, and the result decode. *module.ModuleRegistry
// satisfies it.
type Modules interface {
	KindSchemas
	Invoke(ctx context.Context, kind string, args map[string]any) (map[string]any, error)
	DecodeResult(kind string, raw map[string]any) (any, error)
}

// Store is the loaded set of playbooks, validated and compiled at load time.
type Store struct {
	Playbooks []*Playbook
	modules   Modules
}

// Playbook is one trigger+actions document.
type Playbook struct {
	// ID is the playbook's stable identity for execution-time idempotency:
	// its file path relative to the configured directory root. It is unique
	// within a single Load because the EDA loader walks one directory's
	// entries and filenames there are inherently unique -- the doc's claim
	// about "the directory-loader's collision rule" belongs to the ROUTING
	// loader (workflow.LoadPlaybookDirs), a different loader.
	ID string
	// Version is a content hash of the loaded file, recomputed on every
	// load. It pins dispatched-run provenance to the exact definition active
	// when the run fired, mirroring Binding.Version's purpose, without a
	// database row.
	Version string

	Trigger Trigger
	Actions []Action
}

// Trigger decides which incoming events this playbook matches. It reuses the
// existing workintake label/kind vocabulary -- no second label-matching
// mechanism is invented here.
type Trigger struct {
	// Kind is the routing kind (bug/feature/bootstrap); empty matches the
	// default (unlabelled) flow.
	Kind workintake.Kind
	// Labels is the closed label set this trigger matches; an empty set
	// matches any labels.
	Labels []string
}

// Action is one step in a playbook. A workflow action names a workflow
// definition; a module action names a registered module kind and is not
// routed yet.
type Action struct {
	Position string
	// ID is an optional stable identifier for this action. When present it
	// is the key later actions read this action's result under
	// (actions.<id>). Workflow actions use the shared stable-identifier
	// grammar; module actions must use a CEL identifier so the id can be
	// read through `actions.<id>` field selection.
	ID string
	// Kind is the module kind name (position: module only); empty for a
	// workflow action.
	Kind string
	// Workflow is the name of the workflow definition to dispatch to
	// (position: workflow only).
	Workflow string
	// When is a compiled CEL condition; nil means unconditional.
	When *expr.Program
	// Args holds the action's compiled CEL args values, keyed by arg name.
	// Nil or empty means the action takes no args.
	Args map[string]*expr.Program

	// env is the per-action CEL environment this action's expressions were
	// compiled against: the prior actions' declared ids and their kinds'
	// Result types. It is compile-only: expr.Env.Eval reads only the
	// compiled Program (the cost limit is baked in at Compile) and never
	// reads the Env receiver. It is unset only for a hand-built Action
	// (tests, pre-load composition); loaded actions always carry it.
	env *expr.Env
}

// rawPlaybook is the YAML document shape before compilation.
type rawPlaybook struct {
	Trigger rawTrigger  `yaml:"trigger"`
	Actions []rawAction `yaml:"actions"`
}

type rawTrigger struct {
	Kind   string   `yaml:"kind"`
	Labels []string `yaml:"labels"`
}

type rawAction struct {
	Position string            `yaml:"position"`
	ID       string            `yaml:"id"`
	Workflow string            `yaml:"workflow"`
	Kind     string            `yaml:"kind"`
	When     string            `yaml:"when"`
	Args     map[string]string `yaml:"args"`
}

// Module action ids are stricter than workflow ids because they must also be
// readable as `actions.<id>` in CEL field selection. The authoritative check
// is expr.IsCELFieldName (which compiles the probe), plus a lowercase policy:
// the id must be a lowercase CEL field name, so `Build` -- a valid CEL
// identifier -- is rejected for its case, while a CEL keyword such as `in` is
// rejected because `actions.in` has no field-selection spelling.

// validateActionIDs enforces the WORKFLOW id shape and uniqueness. An absent
// id is fine (ids are optional); a declared id must match the
// stable-identifier grammar. The workflow shape has exactly one action, so
// the uniqueness check is a guard against future shape changes.
func validateActionIDs(actions []rawAction) error {
	seen := make(map[string]struct{}, len(actions))
	for _, a := range actions {
		id := a.ID
		if id == "" {
			continue
		}
		if !stableid.Valid(id) {
			return fmt.Errorf("action id %q is not a valid stable identifier (want %s)", id, stableid.Pattern)
		}
		if _, dup := seen[id]; dup {
			return fmt.Errorf("duplicate action id %q", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

// validateModuleActionIDs enforces the MODULE id shape and uniqueness. A
// module id must be a lowercase CEL field name writable as `actions.<id>`
// (stricter than the workflow stable-identifier grammar) and may not repeat;
// the error names the playbook, the action index, and the offending id.
func validateModuleActionIDs(path string, actions []rawAction) error {
	seen := make(map[string]struct{}, len(actions))
	for i, a := range actions {
		id := a.ID
		if id == "" {
			continue
		}
		if id != strings.ToLower(id) || !expr.IsCELFieldName(id) {
			return fmt.Errorf("playbook %s: action %d id %q must be a lowercase CEL field name writable as actions.%s", path, i+1, id, id)
		}
		if _, dup := seen[id]; dup {
			return fmt.Errorf("playbook %s: duplicate action id %q", path, id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

// Load reads every *.yaml/*.yml playbook in dir, validates each against the
// two-shape boundary (exactly one workflow action, or one or more module
// actions; never mixed), and compiles each when expression and args value
// against the action's per-playbook environment. ANY failure -- malformed
// YAML, a mixed/unsupported/empty action shape, an unknown kind, a when
// compile error, an args key error -- fails the whole load: the reject-at-load
// philosophy of the parent design doc. A missing directory is an empty store
// (matching the flat binding loaders' convention).
func Load(dir string, modules Modules) (*Store, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return &Store{modules: modules}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read playbook dir %s: %w", dir, err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if ext := strings.ToLower(filepath.Ext(e.Name())); ext == ".yaml" || ext == ".yml" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	store := &Store{modules: modules}
	for _, name := range names {
		path := filepath.Join(dir, name)
		pb, err := loadOne(dir, path, modules)
		if err != nil {
			return nil, err
		}
		store.Playbooks = append(store.Playbooks, pb)
	}
	return store, nil
}

// loadOne loads and validates a single playbook file, deriving its stable ID
// (path relative to the configured root) and a content-hash Version.
func loadOne(dir, path string, schemas KindSchemas) (*Playbook, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read playbook %s: %w", path, err)
	}

	id := path
	if rel, relErr := filepath.Rel(dir, path); relErr == nil {
		id = rel
	}

	var raw rawPlaybook
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse playbook %s: %w", path, err)
	}

	pb := &Playbook{
		ID:      filepath.ToSlash(id),
		Version: fmt.Sprintf("%x", sha256.Sum256(data)),
		Trigger: Trigger{
			Kind:   workintake.Kind(strings.TrimSpace(raw.Trigger.Kind)),
			Labels: raw.Trigger.Labels,
		},
	}

	if pb.Trigger.Kind == "" && len(raw.Trigger.Labels) == 0 {
		return nil, fmt.Errorf("playbook %s: trigger must declare a kind or labels", path)
	}
	if err := pb.Trigger.Kind.Validate(); err != nil {
		return nil, fmt.Errorf("playbook %s: %w", path, err)
	}

	actions, err := compileActions(path, raw.Actions, schemas)
	if err != nil {
		return nil, err
	}
	pb.Actions = actions
	return pb, nil
}

// compileActions validates the action shape (D2) and compiles every action's
// expressions against that action's environment of prior results. Exactly one
// workflow action is a workflow playbook; one or more module actions is an
// action playbook; anything else fails the load naming the playbook.
func compileActions(path string, raw []rawAction, schemas KindSchemas) ([]Action, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("playbook %s: must declare at least one action", path)
	}

	var workflows, modules int
	for i := range raw {
		a := &raw[i]
		pos := strings.TrimSpace(a.Position)
		if pos == "" {
			// Default position for a single bare workflow name stays workflow
			// for the smallest-useful case.
			if len(raw) == 1 && strings.TrimSpace(a.Workflow) != "" {
				pos = "workflow"
			} else {
				return nil, fmt.Errorf("playbook %s: action %d must declare a position (%q or %q)", path, i+1, "workflow", "module")
			}
		}
		if pos != "workflow" && pos != "module" {
			return nil, fmt.Errorf("playbook %s: action %d position %q is not supported (want %q or %q)", path, i+1, pos, "workflow", "module")
		}
		a.Position = pos
		if err := validateActionShapeField(path, i, pos, a); err != nil {
			return nil, err
		}
		if pos == "workflow" {
			workflows++
		} else {
			modules++
		}
	}

	switch {
	case workflows > 0 && modules > 0:
		return nil, fmt.Errorf("playbook %s: cannot mix workflow and module actions in one playbook", path)
	case workflows > 0:
		if workflows != 1 {
			return nil, fmt.Errorf("playbook %s: exactly one workflow action is supported; got %d", path, workflows)
		}
		return compileWorkflowActions(path, raw)
	default:
		return compileModuleActions(path, raw, schemas)
	}
}

// validateActionShapeField rejects a field from the other shape being present
// on an action: a workflow action must not declare kind (module-only), and a
// module action must not declare workflow (workflow-only). yaml.Unmarshal
// accepts both keys, so this is where a silently-ignored foreign key becomes
// a reported load failure naming the playbook and the action index.
func validateActionShapeField(path string, i int, pos string, a *rawAction) error {
	switch pos {
	case "workflow":
		if strings.TrimSpace(a.Kind) != "" {
			return fmt.Errorf("playbook %s: action %d is a workflow action and must not declare kind", path, i+1)
		}
	case "module":
		if strings.TrimSpace(a.Workflow) != "" {
			return fmt.Errorf("playbook %s: action %d is a module action and must not declare workflow", path, i+1)
		}
	}
	return nil
}

// compileWorkflowActions builds the single-action workflow playbook shape.
// The env has no prior result ids, so a workflow `when` or `args` value that
// reads `actions.<id>` fails at compile -- the unchanged workflow behaviour.
func compileWorkflowActions(path string, raw []rawAction) ([]Action, error) {
	if err := validateActionIDs(raw); err != nil {
		return nil, fmt.Errorf("playbook %s: %w", path, err)
	}
	a := raw[0]
	if strings.TrimSpace(a.Workflow) == "" {
		return nil, fmt.Errorf("playbook %s: workflow-kind action must name a workflow", path)
	}

	env := expr.NewEnv()
	action := Action{
		Position: "workflow",
		ID:       a.ID,
		Workflow: strings.TrimSpace(a.Workflow),
		env:      env,
	}
	if strings.TrimSpace(a.When) != "" {
		prg, err := compileExpr(path, "when condition", a.When, env)
		if err != nil {
			return nil, err
		}
		action.When = prg
	}
	args, err := compileArgs(path, "", "", a.Args, env, nil)
	if err != nil {
		return nil, err
	}
	action.Args = args
	return []Action{action}, nil
}

// compileModuleActions builds the one-or-more-module-actions playbook shape.
// Each action's env declares the prior actions' ids typed by their kinds'
// Result structs, so a later action can read an earlier result and a forward
// or field-typo read fails at compile (multi-action-playbooks.md, D3).
func compileModuleActions(path string, raw []rawAction, schemas KindSchemas) ([]Action, error) {
	if err := validateModuleActionIDs(path, raw); err != nil {
		return nil, err
	}
	actions := make([]Action, 0, len(raw))
	var declared []expr.DeclaredResult
	for i := range raw {
		a := &raw[i]
		label := actionLabel(i, a.ID)
		kind := strings.TrimSpace(a.Kind)
		if kind == "" {
			return nil, fmt.Errorf("playbook %s: %s must name a kind", path, label)
		}
		argsType, resultType, ok := schemas.KindSchema(kind)
		if !ok {
			return nil, fmt.Errorf("playbook %s: %s has unknown module kind %q", path, label, kind)
		}

		env := expr.NewEnv(declared...)
		action := Action{
			Position: "module",
			ID:       a.ID,
			Kind:     kind,
			env:      env,
		}
		if strings.TrimSpace(a.When) != "" {
			prg, err := compileExpr(path, label+" when condition", a.When, env)
			if err != nil {
				return nil, err
			}
			action.When = prg
		}
		args, err := compileArgs(path, label, kind, a.Args, env, argsType)
		if err != nil {
			return nil, err
		}
		action.Args = args
		actions = append(actions, action)

		if a.ID != "" {
			declared = append(declared, expr.DeclaredResult{ID: a.ID, Type: resultType})
		}
	}
	return actions, nil
}

// actionLabel renders the 1-based action location used in module-action load
// errors, including the declared id when present so an operator can find the
// offending action by either index or id.
func actionLabel(i int, id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Sprintf("action %d", i+1)
	}
	return fmt.Sprintf("action %d (id %q)", i+1, id)
}

// argsLabel prefixes an args-key location with the action label when one is
// supplied; workflow actions pass an empty label and keep the bare
// `args["key"]` spelling.
func argsLabel(label, key string) string {
	if label == "" {
		return fmt.Sprintf("args[%q]", key)
	}
	return fmt.Sprintf("%s args[%q]", label, key)
}

// compileArgs compiles every args value as a CEL expression at load, keyed by
// arg name. J2 has no literal/expression split: the YAML scalar text IS the
// CEL source, so a string literal is quoted inside YAML and a number or
// context read is written as CEL. When argsSchema is non-nil (a module kind's
// Args struct) every key must name one of its fields; workflow actions pass
// nil and keep free-form args. Each program goes through the same
// compile/reference validation as `when`, with label naming the offending
// action and the args key naming the offending field.
func compileArgs(path, label, kind string, raw map[string]string, env *expr.Env, argsSchema reflect.Type) (map[string]*expr.Program, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if argsSchema != nil {
		if err := validateArgsKeys(path, label, kind, argsSchema, raw); err != nil {
			return nil, err
		}
	}
	keys := make([]string, 0, len(raw))
	for key := range raw {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	args := make(map[string]*expr.Program, len(raw))
	for _, key := range keys {
		prg, err := compileExpr(path, argsLabel(label, key), raw[key], env)
		if err != nil {
			return nil, err
		}
		args[key] = prg
	}
	return args, nil
}

// validateArgsKeys rejects an args key the kind's Args struct does not define
// (multi-action-playbooks.md, D4), so an arg typo is a load failure rather
// than a dispatch-time shape mismatch. Go field names are lower-cased to the
// YAML spelling (Message -> message), and the YAML key is compared verbatim:
// the decoder also matches literal lower-case keys, so `Message` is rejected
// here rather than loading and then failing at dispatch.
func validateArgsKeys(path, label, kind string, argsSchema reflect.Type, raw map[string]string) error {
	t := argsSchema
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	fields := make(map[string]struct{}, t.NumField())
	for field := range t.Fields() {
		fields[strings.ToLower(field.Name)] = struct{}{}
	}
	keys := make([]string, 0, len(raw))
	for key := range raw {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	loc := kind + " kind"
	if label != "" {
		loc = label + " " + loc
	}
	for _, key := range keys {
		if _, ok := fields[key]; !ok {
			return fmt.Errorf("playbook %s: %s args[%q] is not a declared Args field", path, loc, key)
		}
	}
	return nil
}

// compileExpr compiles one playbook expression and applies the remaining
// load-time reference check: every `actions` read must be statically
// resolvable to a prior action id. The per-playbook object type rejects an
// undeclared or forward id and a dynamic `actions` read at compile time; the
// one spelling it does not reject is a bare `actions` value read, which this
// check still refuses. field is the human-readable expression location used in
// errors (`when condition` or `args["name"]`), so a failure names the
// playbook path and the offending expression.
func compileExpr(path, field, src string, env *expr.Env) (*expr.Program, error) {
	prg, err := env.Compile(strings.TrimSpace(src))
	if err != nil {
		return nil, fmt.Errorf("playbook %s: %s: %w", path, field, err)
	}
	_, resolvable := prg.ActionReferences()
	if !resolvable {
		return nil, fmt.Errorf(
			"playbook %s: %s contains an `actions` reference that cannot be statically resolved to an action id (the `actions` context root must be read as a prior action id)",
			path, field,
		)
	}
	return prg, nil
}

// DispatchInput is what the coordinator evaluates a playbook against at
// dispatch time.
type DispatchInput struct {
	// Labels are the incoming task's labels (comma-split already).
	Labels []string
	// Kind is the routing kind the labels produced (workintake.KindForLabels).
	Kind string
	// TaskID is the originating task's identity, used to derive the event_id
	// half of the playbook_dispatches idempotency ledger key. It carries the
	// TaskEnvelope.IdempotencyKey() value ("archie:owner/repo/number"), the
	// stable identity available at the discovery/dispatch point (pollNATS and
	// the webhook receiver both compute kind/labels from a TaskEnvelope before
	// any workflow.Task row exists). It is NOT a workflow.Task.ID int64, which
	// does not exist until the task is persisted.
	TaskID string
	// Event is the event payload exposed as `event` in CEL expressions. For
	// a workflow-kind dispatch this carries the label/kind fields cheaply
	// available at this point.
	Event map[string]any
}

// IsActionPlaybook reports whether pb is an action playbook (one or more
// module actions), as opposed to a workflow playbook (exactly one workflow
// action). It is the single two-shape predicate shared by Dispatch (which
// routes only workflow playbooks) and Run (which executes only action
// playbooks), so the two can never disagree.
func (pb *Playbook) IsActionPlaybook() bool {
	return len(pb.Actions) != 1 || pb.Actions[0].Position != "workflow"
}

// Match reports whether the playbook's trigger matches the input's labels.
func (pb *Playbook) Match(input DispatchInput) bool {
	labels := input.Labels
	if len(labels) == 0 {
		labels = []string{}
	}
	kind := workintake.Kind(input.Kind)

	if pb.Trigger.Kind != "" && pb.Trigger.Kind != kind {
		return false
	}
	if len(pb.Trigger.Labels) > 0 {
		// Trigger labels must all be present in the input's labels.
		inputSet := make(map[string]bool, len(labels))
		for _, l := range labels {
			inputSet[strings.TrimSpace(l)] = true
		}
		for _, want := range pb.Trigger.Labels {
			if !inputSet[strings.TrimSpace(want)] {
				return false
			}
		}
	}
	return true
}

// Decision is what the coordinator selected for one event: the workflow a
// matching playbook's action names, plus the provenance of the definition
// that chose it. Version pins the decision to the exact file content active
// when it fired, and both fields are the first two components of the
// per-action idempotency key the resolved gap-2 scheme derives
// (docs/prds/eda-playbook-engine.md, "Idempotency at execution time").
type Decision struct {
	PlaybookID string
	Version    string
	Workflow   string
	// ActionID is the dispatched action's declared id; empty when the action
	// declares none. It is the `actions.<id>` key later actions (and the
	// dispatch ledger) read this action under.
	ActionID string
	// ActionPosition is the 1-based index of the dispatched action in the
	// playbook's actions list. It is the idempotency-key fallback when
	// ActionID is empty (docs/prds/eda-playbook-engine.md, "Idempotency at
	// execution time").
	ActionPosition int
}

// Dispatch returns the workflow name the first matching workflow playbook
// selects for the input, and whether any playbook matched. Action playbooks
// choose no workflow, so they are skipped here and executed by Run. No match
// means trigger
// mismatch or a when condition evaluating false, and the caller keeps its own
// routing.
//
// The name is returned rather than a compiled workflow because the production
// caller (the daemon's definition pin) decides against the active definition
// collection, not against a compiled registry; whether the named workflow
// exists is that caller's check, in the same place it makes it for every
// other routing source.
//
// A when evaluation error follows the resolved doc's J3: the condition
// evaluates to false and dispatch is skipped (the caller logs).
func (s *Store) Dispatch(input DispatchInput) (Decision, bool) {
	// Nil-receiver-safe: a composition root that builds its daemon before the
	// playbook load hands over a nil store, and "no playbooks" is the honest
	// answer there rather than a panic.
	if s == nil {
		return Decision{}, false
	}
	ctx := evalContext(input)
	for _, pb := range s.Playbooks {
		if !pb.Match(input) {
			continue
		}
		if pb.IsActionPlaybook() {
			// Action playbook: loaded and validated, not routed (D1).
			continue
		}
		a := pb.Actions[0]
		if !a.whenHolds(ctx) {
			continue
		}
		return Decision{PlaybookID: pb.ID, Version: pb.Version, Workflow: a.Workflow, ActionID: a.ID, ActionPosition: 1}, true
	}
	return Decision{}, false
}

// evalContext is the single evaluation context every CEL expression reads at
// dispatch time. `when` and `args` are both CEL expressions evaluated against
// this one context (J2), so the two can never observe different data.
func evalContext(input DispatchInput) expr.Context {
	return expr.Context{
		Event:   input.Event,
		Actions: map[string]map[string]any{},
	}
}

// EvalArgs evaluates a compiled action's args against the dispatch context,
// returning the resulting name->value map. Run evaluates args the same way
// against a context that also carries earlier actions' results. An action declaring no args evaluates to an empty map, nil-program entries
// are skipped, and the first evaluation error is returned. Nil-receiver-safe
// for the pre-load composition phase.
//
// Asymmetry with `when`: `when` is a predicate, so an evaluation error is
// false (J3: skip + log); `args` is data, so an evaluation error has no
// meaningful substitute and is returned to the caller to abort the dispatch.
// Ratifying that rule against a real consumer is tracked by archie-core-1h05.
func (s *Store) EvalArgs(a Action, input DispatchInput) (map[string]any, error) {
	if s == nil {
		return map[string]any{}, nil
	}
	return a.evalArgs(evalContext(input))
}

// whenHolds reports whether the action's `when` is absent or evaluates to
// true. An evaluation error is false (J3: skip, caller logs).
func (a Action) whenHolds(ctx expr.Context) bool {
	if a.When == nil {
		return true
	}
	val, err := a.env.Eval(a.When, ctx)
	if err != nil {
		return false
	}
	b, ok := val.(bool)
	return ok && b
}

// evalArgs evaluates the action's args against ctx in key order, returning
// the first evaluation error.
func (a Action) evalArgs(ctx expr.Context) (map[string]any, error) {
	out := make(map[string]any, len(a.Args))
	keys := make([]string, 0, len(a.Args))
	for key := range a.Args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if a.Args[key] == nil {
			continue
		}
		val, err := a.env.Eval(a.Args[key], ctx)
		if err != nil {
			return nil, fmt.Errorf("evaluate args[%q]: %w", key, err)
		}
		out[key] = val
	}
	return out, nil
}
