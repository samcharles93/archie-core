// Package access owns the policy schema, the action vocabulary, how
// principals and resources are turned into policy entities, and the
// Authorizer contract (docs/prds/orgs-and-access.md).
//
// The policy chain is
//
//	instance ─▶ org ─▶ workspace ─▶ identity + workflow ─▶ object
//
// and the Cedar implementation lives in internal/infrastructure/access;
// internal/app wires it. Exactly two places call the Authorizer: the
// dashboard and API request path, and dispatch. Every other service verifies
// a credential and never evaluates policy.
//
// The chain's "identity + workflow" level is expressed with object policies:
// an identity or a workflow is one of the objects a policy can attach to
// (event source, secret, identity, binding, workflow), so the stored levels
// are instance, org, workspace and object.
package access

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/samcharles93/archie-core/internal/domain/org"
)

// Level is one level of the policy chain. A level's policies govern the
// requests the levels below it see: a lower level narrows and never widens.
type Level string

const (
	// LevelInstance holds across every org. The shipped cross-org forbid
	// lives here and cannot be removed.
	LevelInstance Level = "instance"
	// LevelOrg is an org's policies, including the shipped role policies.
	LevelOrg Level = "org"
	// LevelWorkspace is one workspace's stored policies.
	LevelWorkspace Level = "workspace"
	// LevelObject attaches to one object: an event source, a secret, an
	// identity, a binding, a workflow.
	LevelObject Level = "object"
)

// Validate reports whether l is one of the stored levels.
func (l Level) Validate() error {
	switch l {
	case LevelInstance, LevelOrg, LevelWorkspace, LevelObject:
		return nil
	}
	return fmt.Errorf("%w: %q", ErrInvalidLevel, l)
}

// Action is the access vocabulary: what a principal may do to a resource.
type Action string

const (
	ActionRead             Action = "read"
	ActionCreate           Action = "create"
	ActionUpdate           Action = "update"
	ActionDelete           Action = "delete"
	ActionApprove          Action = "approve"
	ActionRun              Action = "run"
	ActionReadLogs         Action = "read_logs"
	ActionReadSecret       Action = "read_secret"
	ActionManageMembers    Action = "manage_members"
	ActionManageIdentities Action = "manage_identities"
	ActionManagePolicies   Action = "manage_policies"
)

// Actions is every action, in vocabulary order. The engine and the shipped
// role policies iterate it.
func Actions() []Action {
	return []Action{
		ActionRead, ActionCreate, ActionUpdate, ActionDelete, ActionApprove,
		ActionRun, ActionReadLogs, ActionReadSecret,
		ActionManageMembers, ActionManageIdentities, ActionManagePolicies,
	}
}

// Validate reports whether a is one of the shipped actions.
func (a Action) Validate() error {
	if slices.Contains(Actions(), a) {
		return nil
	}
	return fmt.Errorf("%w: %q", ErrInvalidAction, a)
}

// ResourceKind is what a resource record is. The kind is the object a policy
// names and the vocabulary the dashboard's API path derives actions from.
type ResourceKind string

const (
	KindOrg         ResourceKind = "org"
	KindWorkspace   ResourceKind = "workspace"
	KindSource      ResourceKind = "source"
	KindEventType   ResourceKind = "event_type"
	KindMapping     ResourceKind = "mapping"
	KindBinding     ResourceKind = "binding"
	KindWorkflow    ResourceKind = "workflow"
	KindTask        ResourceKind = "task"
	KindCapture     ResourceKind = "capture"
	KindSecret      ResourceKind = "secret"
	KindIdentity    ResourceKind = "identity"
	KindPolicy      ResourceKind = "policy"
	KindMember      ResourceKind = "member"
	KindDashboard   ResourceKind = "dashboard"
	KindEventIngest ResourceKind = "event"
)

// Policy is one stored policy document. Its Text is Cedar policy language
// (one policy: an effect, a principal/action/resource scope and conditions).
//
// The scope fields say where the policy attaches. LevelInstance policies
// carry none; LevelOrg carries OrgID; LevelWorkspace adds WorkspaceID;
// LevelObject adds ObjectKind and ObjectID.
type Policy struct {
	ID    string `json:"id"`
	Level Level  `json:"level"`

	OrgID       org.OrgID       `json:"org_id,omitempty"`
	WorkspaceID org.WorkspaceID `json:"workspace_id,omitempty"`
	ObjectKind  ResourceKind    `json:"object_kind,omitempty"`
	ObjectID    string          `json:"object_id,omitempty"`

	Text string `json:"text"`
}

// Validate checks a policy's shape: a level-appropriate scope, a non-empty
// text and a non-empty ID. The text's Cedar syntax is checked by the engine
// (the Validator contract); this check is structural, so the store and the
// reset path can reject a malformed document without an engine.
func (p Policy) Validate() error {
	if strings.TrimSpace(p.ID) == "" {
		return fmt.Errorf("%w: no ID", ErrInvalidPolicy)
	}
	if err := p.Level.Validate(); err != nil {
		return err
	}
	switch p.Level {
	case LevelInstance:
	case LevelOrg:
		if p.OrgID == "" {
			return fmt.Errorf("%w: org policy carries no org", ErrInvalidPolicy)
		}
	case LevelWorkspace:
		if p.OrgID == "" || p.WorkspaceID == "" {
			return fmt.Errorf("%w: workspace policy carries no org or workspace", ErrInvalidPolicy)
		}
	case LevelObject:
		if p.OrgID == "" || p.WorkspaceID == "" || p.ObjectKind == "" || p.ObjectID == "" {
			return fmt.Errorf("%w: object policy carries no org, workspace, kind or ID", ErrInvalidPolicy)
		}
	}
	if strings.TrimSpace(p.Text) == "" {
		return fmt.Errorf("%w: no text", ErrInvalidPolicy)
	}
	return nil
}

// Governs reports whether the policy attaches to the level and scope of the
// given resource. An instance policy governs everything; an org policy
// governs the resources of its org; a workspace policy its workspace; an
// object policy the one record it names.
func (p Policy) Governs(r Resource) bool {
	if p.Level != LevelInstance && p.OrgID != r.Org {
		return false
	}
	switch p.Level {
	case LevelWorkspace, LevelObject:
		if p.WorkspaceID != r.Workspace {
			return false
		}
	}
	if p.Level == LevelObject && (p.ObjectKind != r.Kind || p.ObjectID != r.ID) {
		return false
	}
	return true
}

// Sentinel errors. The message strings are part of the store's wire
// contract: staterpc's mapError/unmapError matches them by (code, canonical
// message), so they must never change.
var (
	ErrInvalidLevel  = errors.New("access: invalid level")
	ErrInvalidAction = errors.New("access: invalid action")
	ErrInvalidPolicy = errors.New("access: invalid policy")
	// ErrPolicyNotFound is returned when a policy ID does not exist.
	ErrPolicyNotFound = errors.New("access: policy not found")
	// ErrPolicyInvalidText is returned when a policy's text does not parse.
	ErrPolicyInvalidText = errors.New("access: policy text does not parse")
)
