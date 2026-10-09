// Package kit decides whether archie can run a Kit v3 harness and with what
// capabilities.
package kit

import (
	"context"
	"errors"
	"fmt"

	"github.com/docker/sandbox-kit-spec/v3/fetch"
	"github.com/docker/sandbox-kit-spec/v3/spec"
)

const (
	typeCredential    = "com.docker.sandbox/credential@1"
	typeAgentSessions = "com.docker.sandbox/agent-sessions@1"
)

// supported is every capability type archie provides. Anything else is
// refused when required and skipped when optional: approximating a type
// archie does not implement would grant a Kit something nobody reviewed.
var supported = map[string]bool{
	typeCredential:                        true,
	"com.docker.sandbox/network-policy@1": true,
	"com.docker.sandbox/volume@1":         true,
	"com.docker.sandbox/lifecycle@1":      true,
	typeAgentSessions:                     true,
	"com.docker.sandbox/agent-skills@1":   true,
	"com.docker.sandbox/agent-skill@1":    true,
	"com.docker.sandbox/agent-context@1":  true,
	"com.docker.sandbox/resources@1":      true,
}

// forgeServices are credential services a harness never receives: the model
// never runs git, so it never holds a credential that could push.
var forgeServices = map[string]bool{
	"github":    true,
	"gitlab":    true,
	"gitea":     true,
	"forgejo":   true,
	"bitbucket": true,
}

// Plan is what archie launches: the composed descriptor, the capabilities
// it provides, and what it skipped.
type Plan struct {
	Descriptor     *spec.Descriptor
	Capabilities   []spec.Capability
	Skipped        []spec.SelectionRecord
	Sessions       *spec.AgentSessions
	ContextSources []spec.ContextSource
}

// DecodePublished reads the descriptor from an image's manifest annotations
// and holds it to the published form: strict decoding, full validation, and
// no unexpanded build-phase args.
func DecodePublished(annotations map[string]string) (*spec.Descriptor, error) {
	raw, ok := annotations[spec.AnnotationDescriptor]
	if !ok {
		return nil, fmt.Errorf("image is not a kit: no %s annotation", spec.AnnotationDescriptor)
	}
	d, err := spec.Decode([]byte(raw))
	if err != nil {
		return nil, err
	}
	if _, err := spec.ValidatePublished([]byte(raw), d); err != nil {
		return nil, err
	}
	return d, nil
}

// SelectCapability applies Archie's runtime policy before composition.
func SelectCapability(_ context.Context, _ spec.Descriptor, c spec.Capability) spec.CapabilityDecision {
	reason, err := refusalReason(c)
	if err != nil {
		return spec.CapabilityDecision{Message: err.Error()}
	}
	return spec.CapabilityDecision{Accepted: reason == "", Message: reason}
}

// FromResolved reads the profile's selected Kit declarations:
// a workload Kit or a published Kit set, never a bare mixin. archie drives a
// harness headlessly, so it must carry an agent-sessions prompt verb.
func FromResolved(resolved *fetch.Resolved) (*Plan, error) {
	if resolved.Descriptor.Kind != spec.KindWorkload {
		return nil, errors.New("kit is a mixin: publish a Kit set composing it with a workload Kit, and name the set")
	}
	plan := &Plan{Descriptor: resolved.Descriptor, Capabilities: resolved.Descriptor.Capabilities}
	for _, selection := range resolved.Selections {
		plan.Skipped = append(plan.Skipped, selection.Selection.Skipped...)
	}
	for _, unit := range resolved.Kits {
		contexts, err := spec.AgentContextsOf(unit.Descriptor.Capabilities)
		if err != nil {
			return nil, err
		}
		for _, ac := range contexts {
			if ac.ContentFile != "" || ac.Content != "" {
				plan.ContextSources = append(plan.ContextSources, spec.ContextSource{Reference: unit.Reference, Path: ac.ContentFile, Content: ac.Content})
			}
		}
	}
	sessions, err := spec.AgentSessionsOf(plan.Capabilities)
	if err != nil {
		return nil, err
	}
	if sessions == nil || len(sessions.Prompt) == 0 {
		return nil, fmt.Errorf("kit composition declares no %s prompt verb: archie cannot run it headlessly", typeAgentSessions)
	}
	plan.Sessions = sessions
	return plan, nil
}

// ValidateNeeds rejects a Plan without a resume verb when the gate may
// retry.
func ValidateNeeds(plan *Plan, gateRetries int) error {
	if gateRetries > 0 && (plan.Sessions == nil || len(plan.Sessions.Resume) == 0) {
		return fmt.Errorf("needs.gate_retries is %d, but the kit declares no %s resume verb to retry with", gateRetries, typeAgentSessions)
	}
	return nil
}

func refusalReason(c spec.Capability) (string, error) {
	if !supported[c.Type] {
		return "not provided by archie", nil
	}
	if c.Type != typeCredential {
		return "", nil
	}
	var cred spec.Credential
	if err := spec.DecodeCapabilityConfig(c, &cred); err != nil {
		return "", err
	}
	if forgeServices[cred.Service] {
		return fmt.Sprintf("service %q is a forge credential; harnesses never hold one", cred.Service), nil
	}
	if cred.OAuth != nil && cred.OAuth.Passthrough {
		return fmt.Sprintf("service %q asks for OAuth passthrough; real tokens never enter the container", cred.Service), nil
	}
	return "", nil
}
