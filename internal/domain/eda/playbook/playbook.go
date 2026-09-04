// Package playbook is the EDA playbook document type and its event
// coordinator: the rich trigger+actions YAML shape (docs/prds/
// eda-playbook-engine.md) with CEL `when` conditions (open question 1,
// resolved to CEL). This slice is deliberately single-action,
// workflow-position only: multi-action playbooks and Module/Channel/Forge
// action positions remain blocked on the unresolved execution-time gaps
// (mid-run failure semantics, idempotency for non-workflow actions) and are
// rejected at load -- the hard boundary, not to be relaxed without sign-off.
//
// This is an ADDITIONAL loading path alongside the flat kind/label binding
// files (t2db.9/.10/.11). workflow.Route() and the existing binding loaders
// are untouched; migrating Route() onto this mechanism is a separate, later
// decision.
package playbook

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/samcharles93/archie-core/internal/domain/eda/expr"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
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
	// Workflow is the named workflow.Registry entry to dispatch to
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
	Workflow string `yaml:"workflow"`
	Kind     string `yaml:"kind"`
	When     string `yaml:"when"`
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

	action := Action{
		Position: a.Position,
		Workflow: strings.TrimSpace(a.Workflow),
	}
	if strings.TrimSpace(a.When) != "" {
		prg, err := env.Compile(strings.TrimSpace(a.When))
		if err != nil {
			return nil, fmt.Errorf("playbook %s: when condition: %w", path, err)
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
	// any store.Task row exists). It is NOT a store.Task.ID int64, which does
	// not exist until the task is persisted.
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

// Dispatch returns the workflow to run for the input, or nil if no playbook
// matches (trigger mismatch or a when condition evaluating false). It reuses
// the named-workflow lookup that workflow.Route() uses -- it does not
// duplicate dispatch logic.
//
// A when evaluation error follows the resolved doc's J3: the condition
// evaluates to false and dispatch is skipped (the caller logs).
func (s *Store) Dispatch(reg workflow.Registry, input DispatchInput) *workflow.Workflow {
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
		// Reuse the same named-workflow lookup Route() uses: this is the
		// workflow.Registry map access, not a second dispatch path.
		if wf, ok := reg[a.Workflow]; ok {
			// Return a copy to keep the registry immutable from callers.
			wfCopy := wf
			return &wfCopy
		}
		// Requested workflow unavailable: reported as nil (the caller's
		// Route()-equivalent handles the missing-workflow failure shape).
		return nil
	}
	return nil
}
