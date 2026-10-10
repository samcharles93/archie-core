// Package playbook loads EDA playbooks: a trigger plus either one workflow
// action or ordered module actions, with CEL `when` and `args`.
package playbook

import (
	"context"
	"crypto/sha256"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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
	// ID is the playbook's stable name.
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
	// Kind is the routing kind (bug/feature); empty matches the
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
	// ID optionally names this action so later actions can read actions.<id>.
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

	// env is the CEL environment this action's expressions were compiled
	// against.
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

	// Source lines, so a finding names where the author wrote it: the
	// action's first line, its `when` key, and each args key.
	line     int
	whenLine int
	argLines map[string]int
}

// UnmarshalYAML decodes the action as plain data and records its source
// lines.
func (a *rawAction) UnmarshalYAML(n *yaml.Node) error {
	type plain rawAction
	if err := n.Decode((*plain)(a)); err != nil {
		return err
	}
	a.line = n.Line
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, val := n.Content[i], n.Content[i+1]
		switch key.Value {
		case "when":
			a.whenLine = key.Line
		case "args":
			a.argLines = make(map[string]int, len(val.Content)/2)
			for j := 0; j+1 < len(val.Content); j += 2 {
				a.argLines[val.Content[j].Value] = val.Content[j].Line
			}
		}
	}
	return nil
}

// at is path:line, the compiler-style location a finding leads with; a zero
// line (a hand-built action) leaves the bare path.
func at(path string, line int) string {
	if line == 0 {
		return path
	}
	return fmt.Sprintf("%s:%d", path, line)
}

// Module action ids must be lowercase CEL field names.

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

// Document is one playbook's source text under its stable id.
type Document struct {
	ID   string
	YAML []byte
}

// Load reads and compiles every playbook in dir. Any invalid playbook fails
// the load. A missing directory is an empty store.
func Load(dir string, modules Modules) (*Store, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return &Store{modules: modules}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read playbook dir %s: %w", dir, err)
	}

	var docs []Document
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if ext := strings.ToLower(filepath.Ext(e.Name())); ext != ".yaml" && ext != ".yml" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read playbook %s: %w", e.Name(), err)
		}
		docs = append(docs, Document{ID: e.Name(), YAML: data})
	}
	return Compile(docs, modules)
}

// Compile validates and compiles playbook documents in id order. Any invalid
// playbook fails the whole set: a partially valid set never runs.
func Compile(docs []Document, modules Modules) (*Store, error) {
	slices.SortFunc(docs, func(a, b Document) int { return strings.Compare(a.ID, b.ID) })
	store := &Store{modules: modules}
	for _, doc := range docs {
		pb, err := compileOne(doc, modules)
		if err != nil {
			return nil, err
		}
		store.Playbooks = append(store.Playbooks, pb)
	}
	return store, nil
}

// compileOne validates a single playbook, deriving its content-hash Version.
func compileOne(doc Document, schemas KindSchemas) (*Playbook, error) {
	path := doc.ID
	var raw rawPlaybook
	if err := yaml.Unmarshal(doc.YAML, &raw); err != nil {
		return nil, fmt.Errorf("parse playbook %s: %w", path, err)
	}

	pb := &Playbook{
		ID:      path,
		Version: fmt.Sprintf("%x", sha256.Sum256(doc.YAML)),
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
				return nil, fmt.Errorf("playbook %s: action %d must declare a position (%q or %q)", at(path, a.line), i+1, "workflow", "module")
			}
		}
		if pos != "workflow" && pos != "module" {
			return nil, fmt.Errorf("playbook %s: action %d position %q is not supported (want %q or %q)", at(path, a.line), i+1, pos, "workflow", "module")
		}
		a.Position = pos
		if err := validateActionShapeField(at(path, a.line), i, pos, a); err != nil {
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

// validateActionShapeField rejects a field belonging to the other action
// shape.
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
		return nil, fmt.Errorf("playbook %s: workflow-kind action must name a workflow", at(path, a.line))
	}

	env := expr.NewEnv()
	action := Action{
		Position: "workflow",
		ID:       a.ID,
		Workflow: strings.TrimSpace(a.Workflow),
		env:      env,
	}
	if strings.TrimSpace(a.When) != "" {
		prg, err := compileExpr(at(path, a.whenLine), "when condition", a.When, env)
		if err != nil {
			return nil, err
		}
		action.When = prg
	}
	args, err := compileArgs(path, a.argLines, "", "", a.Args, env, nil)
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
			return nil, fmt.Errorf("playbook %s: %s must name a kind", at(path, a.line), label)
		}
		argsType, resultType, ok := schemas.KindSchema(kind)
		if !ok {
			return nil, fmt.Errorf("playbook %s: %s has unknown module kind %q", at(path, a.line), label, kind)
		}

		env := expr.NewEnv(declared...)
		action := Action{
			Position: "module",
			ID:       a.ID,
			Kind:     kind,
			env:      env,
		}
		if strings.TrimSpace(a.When) != "" {
			prg, err := compileExpr(at(path, a.whenLine), label+" when condition", a.When, env)
			if err != nil {
				return nil, err
			}
			action.When = prg
		}
		args, err := compileArgs(path, a.argLines, label, kind, a.Args, env, argsType)
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

// checkArgType refuses an args value whose checked CEL type cannot fill the
// Args field it names. A dyn value passes: the module decode checks it per
// event.
func checkArgType(path, label, kind string, argsSchema reflect.Type, key string, prg *expr.Program) error {
	t := argsSchema
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	for field := range t.Fields() {
		if strings.ToLower(field.Name) != key || prg.Fits(field.Type) {
			continue
		}
		loc := kind + " kind"
		if label != "" {
			loc = label + " " + loc
		}
		return fmt.Errorf("playbook %s: %s args[%q] is %s, want %s", path, loc, key, prg.OutputType(), field.Type)
	}
	return nil
}

// compileArgs compiles each args value as CEL. With argsSchema, every key
// must name one of its fields.
func compileArgs(path string, lines map[string]int, label, kind string, raw map[string]string, env *expr.Env, argsSchema reflect.Type) (map[string]*expr.Program, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if argsSchema != nil {
		if err := validateArgsKeys(path, lines, label, kind, argsSchema, raw); err != nil {
			return nil, err
		}
	}
	keys := slices.Sorted(maps.Keys(raw))
	args := make(map[string]*expr.Program, len(raw))
	for _, key := range keys {
		prg, err := compileExpr(at(path, lines[key]), argsLabel(label, key), raw[key], env)
		if err != nil {
			return nil, err
		}
		if argsSchema != nil {
			if err := checkArgType(at(path, lines[key]), label, kind, argsSchema, key, prg); err != nil {
				return nil, err
			}
		}
		args[key] = prg
	}
	return args, nil
}

// validateArgsKeys rejects args keys the kind's Args struct does not define.
func validateArgsKeys(path string, lines map[string]int, label, kind string, argsSchema reflect.Type, raw map[string]string) error {
	t := argsSchema
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	fields := make(map[string]struct{}, t.NumField())
	for field := range t.Fields() {
		fields[strings.ToLower(field.Name)] = struct{}{}
	}
	keys := slices.Sorted(maps.Keys(raw))
	loc := kind + " kind"
	if label != "" {
		loc = label + " " + loc
	}
	for _, key := range keys {
		if _, ok := fields[key]; !ok {
			return fmt.Errorf("playbook %s: %s args[%q] is not a declared Args field", at(path, lines[key]), loc, key)
		}
	}
	return nil
}

// compileExpr compiles one expression and rejects a bare `actions` read.
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
	// TaskID is the originating task's idempotency key, used as the event id.
	TaskID string
	// Event is the event payload exposed as `event` in CEL expressions. For
	// a workflow-kind dispatch this carries the label/kind fields cheaply
	// available at this point.
	Event map[string]any
}

// IsActionPlaybook reports whether pb has module actions rather than a
// workflow action.
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

// Decision is the workflow a matching playbook selected, with its playbook
// id and version.
type Decision struct {
	PlaybookID string
	Version    string
	Workflow   string
	// ActionID is the dispatched action's declared id; empty when the action
	// declares none. It is the `actions.<id>` key later actions (and the
	// dispatch ledger) read this action under.
	ActionID string
	// ActionPosition is the 1-based index of the dispatched action in the
	// playbook's actions list. It is the idempotency-key fallback when ActionID
	// is empty.
	ActionPosition int
}

// Dispatch returns the workflow the first matching workflow playbook names,
// and whether one matched. A `when` error counts as false.
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

// EvalArgs evaluates an action's args and returns the first error.
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
	keys := slices.Sorted(maps.Keys(a.Args))
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
