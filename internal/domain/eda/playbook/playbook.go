// Package playbook is the EDA playbook document type and its event
// coordinator: the rich trigger+actions YAML shape (docs/prds/
// eda-playbook-engine.md) with CEL `when` conditions (open question 1,
// resolved to CEL). This slice is deliberately single-action,
// workflow-position only: multi-action playbooks and Module/Channel/Forge
// action positions remain blocked on the unresolved execution-time gaps
// (mid-run failure semantics, idempotency for non-workflow actions) and are
// rejected at load -- the hard boundary, not to be relaxed without sign-off.
//
// This is an ADDITIONAL routing source alongside the flat kind/label binding
// files (t2db.9/.10/.11): the daemon consults a matching playbook before
// those bindings when it pins a task's workflow definition (t2db.23). The
// binding loaders themselves are untouched.
package playbook

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/samcharles93/archie-core/internal/domain/eda/expr"
	"github.com/samcharles93/archie-core/internal/domain/workintake"
)

// Store is the loaded set of playbooks, validated and compiled at load time.
type Store struct {
	Playbooks []*Playbook
	exprEnv   *expr.Env
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

// Action is one step in a playbook. Exactly one action per playbook in this
// slice, position MUST be "workflow".
type Action struct {
	Position string
	// ID is an optional stable identifier for this action. When present it
	// is the key later actions read this action's result under
	// (actions.<id>); the shape is the shared stable-identifier grammar
	// (internal/plugin/host.go, internal/domain/workflow/vocabulary.go).
	ID string
	// Workflow is the name of the workflow definition to dispatch to
	// (position: workflow only).
	Workflow string
	// When is a compiled CEL condition; nil means unconditional.
	When *expr.Program
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
	Position string `yaml:"position"`
	ID       string `yaml:"id"`
	Workflow string `yaml:"workflow"`
	Kind     string `yaml:"kind"`
	When     string `yaml:"when"`
}

// actionIDPattern is the stable-identifier shape an action id must match when
// declared: a lowercase, dotted/dashed identifier (shared with
// internal/plugin/host.go:21 and internal/domain/workflow/vocabulary.go:19),
// so an id has exactly one spelling and cannot smuggle whitespace or case into
// the vocabulary two processes compare.
var actionIDPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*$`)

// validateActionIDs enforces the id shape and uniqueness across a playbook's
// raw actions. An absent id is fine (ids are optional); a declared id must
// match the stable-identifier grammar and may not repeat. Uniqueness is
// unreachable through Load while the one-action boundary holds, so this helper
// is unit-tested directly for the duplicate case.
func validateActionIDs(actions []rawAction) error {
	seen := make(map[string]struct{}, len(actions))
	for _, a := range actions {
		id := a.ID
		if id == "" {
			continue
		}
		if !actionIDPattern.MatchString(id) {
			return fmt.Errorf("action id %q is not a valid stable identifier (want %s)", id, actionIDPattern.String())
		}
		if _, dup := seen[id]; dup {
			return fmt.Errorf("duplicate action id %q", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

// unknownActionReference returns the first statically-resolved action id a
// compiled `when` reads that is not declared on any action before index idx.
// The comparison is against earlier actions' declared ids (the general rule
// from J1), so it stays correct when the one-action boundary later relaxes;
// today idx is always 0, so any actions.<id> reference is unknown.
func unknownActionReference(raw []rawAction, idx int, ids []string) (string, bool) {
	declared := make(map[string]struct{}, idx)
	for _, prior := range raw[:idx] {
		if prior.ID != "" {
			declared[prior.ID] = struct{}{}
		}
	}
	for _, id := range ids {
		if _, ok := declared[id]; !ok {
			return id, true
		}
	}
	return "", false
}

// Load reads every *.yaml/*.yml playbook in dir, validates each against the
// hard boundary (exactly one action, position workflow), and compiles each
// when expression. ANY failure -- malformed YAML, a multi-action playbook, a
// non-workflow position, a when compile error -- fails the whole load: the
// reject-at-load philosophy of the parent design doc. A missing directory is
// an empty store (matching the flat binding loaders' convention).
func Load(dir string) (*Store, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return &Store{exprEnv: expr.NewEnv()}, nil
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

	store := &Store{exprEnv: expr.NewEnv()}
	for _, name := range names {
		path := filepath.Join(dir, name)
		pb, err := loadOne(dir, path, store.exprEnv)
		if err != nil {
			return nil, err
		}
		store.Playbooks = append(store.Playbooks, pb)
	}
	return store, nil
}

// loadOne loads and validates a single playbook file, deriving its stable ID
// (path relative to the configured root) and a content-hash Version.
func loadOne(dir, path string, env *expr.Env) (*Playbook, error) {
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

	// HARD BOUNDARY (t2db.15): exactly one action, position workflow only.
	if len(raw.Actions) != 1 {
		return nil, fmt.Errorf(
			"playbook %s: exactly one action is supported; got %d (multi-action playbooks are blocked on unresolved execution-time gaps)",
			path, len(raw.Actions),
		)
	}
	a := raw.Actions[0]
	b := "workflow"
	if a.Position == "" {
		// Default position for a single bare workflow name stays workflow for
		// the smallest-useful case.
		a.Position = b
	}
	if a.Position != b {
		return nil, fmt.Errorf(
			"playbook %s: action position %q is not supported; only %q actions ship (Module/Channel/Forge positions are blocked on unresolved execution-time gaps)",
			path, a.Position, b,
		)
	}
	if strings.TrimSpace(a.Workflow) == "" {
		return nil, fmt.Errorf("playbook %s: workflow-kind action must name a workflow", path)
	}
	if err := validateActionIDs(raw.Actions); err != nil {
		return nil, fmt.Errorf("playbook %s: %w", path, err)
	}

	action := Action{
		Position: a.Position,
		ID:       a.ID,
		Workflow: strings.TrimSpace(a.Workflow),
	}
	if strings.TrimSpace(a.When) != "" {
		prg, err := env.Compile(strings.TrimSpace(a.When))
		if err != nil {
			return nil, fmt.Errorf("playbook %s: when condition: %w", path, err)
		}
		ids, resolvable := prg.ActionReferences()
		if !resolvable {
			return nil, fmt.Errorf(
				"playbook %s: when condition contains an `actions` reference that cannot be statically resolved to an action id (the `actions` context root must be read as a prior action id)",
				path,
			)
		}
		if id, unknown := unknownActionReference(raw.Actions, 0, ids); unknown {
			return nil, fmt.Errorf("playbook %s: when condition references unknown action id %q", path, id)
		}
		action.When = prg
	}
	pb.Actions = []Action{action}
	return pb, nil
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
}

// Dispatch returns the workflow name the first matching playbook selects for
// the input, and whether any playbook matched. No match means trigger
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
	for _, pb := range s.Playbooks {
		if !pb.Match(input) {
			continue
		}
		a := pb.Actions[0]
		if a.When != nil {
			val, err := s.exprEnv.Eval(a.When, expr.Context{
				Event:   input.Event,
				Actions: map[string]map[string]any{},
			})
			if err != nil {
				// J3: evaluation error -> false (skip), caller logs.
				continue
			}
			b, ok := val.(bool)
			if !ok || !b {
				continue
			}
		}
		return Decision{PlaybookID: pb.ID, Version: pb.Version, Workflow: a.Workflow, ActionID: a.ID}, true
	}
	return Decision{}, false
}
