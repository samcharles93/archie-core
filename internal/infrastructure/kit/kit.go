// Package kit decides whether archie can run a harness packaged as Kit v3
// images, and what it runs with. Descriptor grammar, validation and
// composition belong to the Kit spec package; this package owns only the
// runtime's half of the contract: which capability types archie implements,
// and the refusals that keep the host's guarantees intact.
package kit

import (
	"errors"
	"fmt"
	"strings"

	"github.com/docker/sandbox-kit-spec/v3/spec"
)

const (
	typeCredential    = "com.docker.sandbox/credential@1"
	typeAgentSessions = "com.docker.sandbox/agent-sessions@1"

	// contextPath is where the composed agent-context body is staged.
	contextPath = "/usr/share/archie/agent-context.md"
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

// Skip is an optional capability archie does not provide, recorded so the
// step that ran without it can say so.
type Skip struct {
	Type   string
	Reason string
}

// Refusal is a required capability archie will not provide.
type Refusal struct {
	Type   string
	Reason string
}

// RefusedError carries every refusal, so an operator fixes a Kit in one pass.
type RefusedError struct {
	Refusals []Refusal
}

func (e *RefusedError) Error() string {
	parts := make([]string, len(e.Refusals))
	for i, r := range e.Refusals {
		parts[i] = r.Type + ": " + r.Reason
	}
	return "kit refused: " + strings.Join(parts, "; ")
}

// Plan is what archie launches: the composed descriptor, the capabilities
// it provides, and what it skipped.
type Plan struct {
	Descriptor     *spec.Descriptor
	Capabilities   []spec.Capability
	Skipped        []Skip
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

// Admit applies archie's capability support to one descriptor. Every
// required capability archie will not provide is collected into a
// RefusedError; optional ones are skipped.
func Admit(d *spec.Descriptor) (*Plan, error) {
	plan := &Plan{Descriptor: d}
	var refusals []Refusal
	for _, c := range d.Capabilities {
		reason, err := refusalReason(c)
		if err != nil {
			return nil, err
		}
		switch {
		case reason == "":
			plan.Capabilities = append(plan.Capabilities, c)
		case c.Optional:
			plan.Skipped = append(plan.Skipped, Skip{Type: c.Type, Reason: reason})
		default:
			refusals = append(refusals, Refusal{Type: c.Type, Reason: reason})
		}
	}
	if len(refusals) > 0 {
		return nil, &RefusedError{Refusals: refusals}
	}
	return plan, nil
}

// MergeOptions are the options every composition archie runs is merged with.
var MergeOptions = spec.MergeOptions{ContextPath: contextPath}

// FromMerge admits the profile's Kit as the Kit spec's fetch package read it:
// a workload Kit or a published Kit set, never a bare mixin. archie drives a
// harness headlessly, so it must carry an agent-sessions prompt verb.
func FromMerge(merged *spec.MergeResult) (*Plan, error) {
	if merged.Descriptor.Kind != spec.KindWorkload {
		return nil, errors.New("kit is a mixin: publish a Kit set composing it with a workload Kit, and name the set")
	}
	plan, err := Admit(merged.Descriptor)
	if err != nil {
		return nil, err
	}
	sessions, err := spec.AgentSessionsOf(plan.Capabilities)
	if err != nil {
		return nil, err
	}
	if sessions == nil || len(sessions.Prompt) == 0 {
		return nil, fmt.Errorf("kit composition declares no %s prompt verb: archie cannot run it headlessly", typeAgentSessions)
	}
	plan.Sessions = sessions
	plan.ContextSources = merged.ContextSources
	return plan, nil
}

// ValidateNeeds rejects a Plan that cannot meet what the workflow's stages
// declared they need: a
// gate that may retry needs the Kit's agent-sessions resume verb, because
// archie resumes the session with the gate's output rather than starting
// over. gateRetries is zero for a workflow with no gated stage, which no
// Kit composition can fail to meet.
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
