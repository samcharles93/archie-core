package kit

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/docker/sandbox-kit-spec/v3/spec"
)

const sessions = `
  - type: com.docker.sandbox/agent-sessions@1
    config:
      prompt: [-p, "{{.Prompt}}"]
      resume: [--resume, "{{.SessionID}}"]`

func descriptor(t *testing.T, kind, capabilities string) *spec.Descriptor {
	t.Helper()
	d, err := spec.Decode([]byte("schemaVersion: \"3\"\nkind: " + kind + "\ncapabilities:" + capabilities + "\n"))
	if err != nil {
		t.Fatalf("fixture does not decode: %v", err)
	}
	return d
}

func refusedTypes(t *testing.T, err error) []string {
	t.Helper()
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("want *RefusedError, got %v", err)
	}
	var types []string
	for _, r := range refused.Refusals {
		types = append(types, r.Type)
	}
	return types
}

func TestAdmit(t *testing.T) {
	tests := []struct {
		name         string
		capabilities string
		wantRefused  []string
		wantSkipped  []string
		wantAdmitted int
	}{
		{
			name: "every supported type is admitted",
			capabilities: sessions + `
  - type: com.docker.sandbox/network-policy@1
    config: {runtime: {allow: [api.example.com:443]}}
  - type: com.docker.sandbox/credential@1
    config:
      service: example
      phase: runtime
      apiKey: {name: EXAMPLE_KEY, proxyManaged: true, inject: [{domain: api.example.com, header: Authorization, format: "Bearer %s"}]}
  - type: com.docker.sandbox/volume@1
    config: {path: /home/agent/.state, size: 1g}
  - type: com.docker.sandbox/lifecycle@1
    config: {startup: [{command: [true]}]}
  - type: com.docker.sandbox/agent-skills@1
    config: {path: /home/agent/.skills}
  - type: com.docker.sandbox/agent-context@1
    config: {content: be terse}
  - type: com.docker.sandbox/resources@1
    config: {memory: 2g}`,
			wantAdmitted: 8,
		},
		{
			name:         "a required unsupported type refuses the launch",
			capabilities: sessions + "\n  - type: com.docker.sandbox/privileged@1",
			wantRefused:  []string{"com.docker.sandbox/privileged@1"},
		},
		{
			name: "an optional unsupported type is skipped and recorded",
			capabilities: sessions + `
  - type: com.docker.sandbox/port@1
    optional: true
    config: {port: 8080}`,
			wantSkipped:  []string{"com.docker.sandbox/port@1"},
			wantAdmitted: 1,
		},
		{
			name: "network-policy@2 is refused",
			capabilities: sessions + `
  - type: com.docker.sandbox/network-policy@2
    config: {runtime: {allow: [api.example.com]}}`,
			wantRefused: []string{"com.docker.sandbox/network-policy@2"},
		},
		{
			name: "a host-specific type archie does not know is refused",
			capabilities: sessions + `
  - type: example.com/gpu-broker@1
    config: {pool: a}`,
			wantRefused: []string{"example.com/gpu-broker@1"},
		},
		{
			name: "a credential with passthrough is refused",
			capabilities: sessions + `
  - type: com.docker.sandbox/network-policy@1
    config: {runtime: {allow: [api.example.com]}}
  - type: com.docker.sandbox/credential@1
    config:
      service: example
      phase: runtime
      oauth: {tokenEndpoint: {host: api.example.com}, passthrough: true}`,
			wantRefused: []string{"com.docker.sandbox/credential@1"},
		},
		{
			name: "an optional forge credential is skipped, never bound",
			capabilities: sessions + `
  - type: com.docker.sandbox/network-policy@1
    config: {runtime: {allow: [api.github.com]}}
  - type: com.docker.sandbox/credential@1
    optional: true
    config:
      service: github
      phase: runtime
      apiKey: {name: GH_TOKEN, proxyManaged: true, inject: [{domain: api.github.com, header: Authorization, format: "Bearer %s"}]}`,
			wantSkipped:  []string{"com.docker.sandbox/credential@1"},
			wantAdmitted: 2,
		},
		{
			name: "every refusal is reported, not only the first",
			capabilities: sessions + `
  - type: com.docker.sandbox/privileged@1
  - type: com.docker.sandbox/long-running@1`,
			wantRefused: []string{"com.docker.sandbox/privileged@1", "com.docker.sandbox/long-running@1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := Admit(descriptor(t, spec.KindWorkload, tt.capabilities))
			if tt.wantRefused != nil {
				if got := refusedTypes(t, err); !slices.Equal(got, tt.wantRefused) {
					t.Fatalf("refused %v, want %v", got, tt.wantRefused)
				}
				return
			}
			if err != nil {
				t.Fatalf("Admit: %v", err)
			}
			var skipped []string
			for _, s := range plan.Skipped {
				skipped = append(skipped, s.Type)
			}
			if !slices.Equal(skipped, tt.wantSkipped) {
				t.Fatalf("skipped %v, want %v", skipped, tt.wantSkipped)
			}
			if len(plan.Capabilities) != tt.wantAdmitted {
				t.Fatalf("admitted %d capabilities, want %d", len(plan.Capabilities), tt.wantAdmitted)
			}
		})
	}
}

func TestRefusalNamesTheCapability(t *testing.T) {
	_, err := Admit(descriptor(t, spec.KindWorkload, sessions+"\n  - type: com.docker.sandbox/usb-device@1\n    config: {class: hid}"))
	if err == nil || !strings.Contains(err.Error(), "com.docker.sandbox/usb-device@1") {
		t.Fatalf("error %v does not name the refused capability", err)
	}
}

// admit reads one Kit the way the launcher does: merged alone, then admitted.
func admit(t *testing.T, c spec.Contribution) (*Plan, error) {
	t.Helper()
	merged, err := spec.Merge([]spec.Contribution{c}, MergeOptions)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	return FromMerge(merged)
}

func TestFromMerge(t *testing.T) {
	workload := spec.Contribution{Reference: "workload", Descriptor: descriptor(t, spec.KindWorkload, sessions)}
	mixin := spec.Contribution{Reference: "mixin", Descriptor: descriptor(t, spec.KindMixin, `
  - type: com.docker.sandbox/network-policy@1
    config: {runtime: {allow: [api.example.com]}}`)}
	bareWorkload := spec.Contribution{Reference: "bare", Descriptor: descriptor(t, spec.KindWorkload, `
  - type: com.docker.sandbox/resources@1
    config: {memory: 1g}`)}

	plan, err := admit(t, workload)
	if err != nil || plan.Sessions == nil || len(plan.Sessions.Prompt) == 0 {
		t.Fatalf("admit(workload) = %+v, %v; want a plan with a headless prompt verb", plan, err)
	}

	tests := []struct {
		name string
		kit  spec.Contribution
		want string
	}{
		{"a bare mixin", mixin, "publish a Kit set"},
		{"no headless prompt verb", bareWorkload, "agent-sessions"},
	}
	for _, tt := range tests {
		t.Run(tt.name+" is refused", func(t *testing.T) {
			_, err := admit(t, tt.kit)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %v, want one mentioning %q", err, tt.want)
			}
		})
	}
}

// TestValidateNeeds pins the harness PRD's "a gate retry without a resume
// verb" refusal (Contract): archie resumes the harness's own session with the
// gate's output rather than starting a fresh one, so a gated stage's retry
// budget is unmeetable by a Kit whose agent-sessions capability names no
// resume verb.
func TestValidateNeeds(t *testing.T) {
	promptOnly := descriptor(t, spec.KindWorkload, `
  - type: com.docker.sandbox/agent-sessions@1
    config:
      prompt: [-p, "{{.Prompt}}"]`)
	withResume := descriptor(t, spec.KindWorkload, sessions)

	tests := []struct {
		name        string
		descriptor  *spec.Descriptor
		gateRetries int
		wantErr     bool
	}{
		{"no gated stage, no resume verb", promptOnly, 0, false},
		{"gated stage, no resume verb", promptOnly, 2, true},
		{"gated stage, a resume verb", withResume, 2, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := Admit(tt.descriptor)
			if err != nil {
				t.Fatalf("Admit: %v", err)
			}
			sessions, err := spec.AgentSessionsOf(plan.Capabilities)
			if err != nil {
				t.Fatalf("AgentSessionsOf: %v", err)
			}
			plan.Sessions = sessions

			err = ValidateNeeds(plan, tt.gateRetries)
			if tt.wantErr && err == nil {
				t.Fatal("ValidateNeeds accepted a gate retry budget the kit cannot resume")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("ValidateNeeds: %v", err)
			}
		})
	}
}

func TestDecodePublished(t *testing.T) {
	published := "schemaVersion: \"3\"\nkind: workload\ncapabilities:" + sessions + "\n"
	tests := []struct {
		name        string
		annotations map[string]string
		wantErr     string
	}{
		{"a published descriptor decodes", map[string]string{spec.AnnotationDescriptor: published}, ""},
		{"an image without the annotation is not a kit", map[string]string{"org.opencontainers.image.title": "x"}, "not a kit"},
		{"an unknown field is an error", map[string]string{spec.AnnotationDescriptor: published + "surprise: true\n"}, "surprise"},
		{"an unexpanded build arg is not a published form", map[string]string{spec.AnnotationDescriptor: "schemaVersion: \"3\"\nkind: workload\nversion: \"${{ kit.args.version }}\"\n"}, "version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := DecodePublished(tt.annotations)
			if tt.wantErr == "" {
				if err != nil || d == nil {
					t.Fatalf("DecodePublished: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %v, want one mentioning %q", err, tt.wantErr)
			}
		})
	}
}
