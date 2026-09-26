package controlplane

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
)

// containerPoliciesDefinition returns the registered definition, so a test that
// pins the document's shape also pins its wiring: Seed, Validate and Normalize
// are the three functions the registry actually runs.
func containerPoliciesDefinition(t *testing.T) Definition {
	t.Helper()
	for _, definition := range operationalDefinitions() {
		if definition.Kind == ContainerRuntimePoliciesKind {
			return definition
		}
	}
	t.Fatal("container-runtime-policies is not registered")
	return Definition{}
}

// TestContainerRuntimePoliciesSeedIsTheDocumentShape pins what an operator sees
// in the Web UI: snake_case keys and durations they can read, matching every
// sibling resource in the registry. The Go-cased keys this replaced are asserted
// absent, because the defect was invisible to a test that only compared decoded
// values -- encoding/json round-trips the wrong spelling perfectly happily.
func TestContainerRuntimePoliciesSeedIsTheDocumentShape(t *testing.T) {
	cfg := config.Config{Containers: config.ContainerConfig{
		Image: "archie-agent:test", MaxConcurrency: 4,
		MaxUptime: config.Duration(2 * time.Hour), VolumeTTL: config.Duration(24 * time.Hour),
		PullPolicy: "missing", Network: "bridge",
	}}
	encoded, err := json.Marshal(containerPoliciesDefinition(t).Seed(cfg))
	if err != nil {
		t.Fatalf("marshal seed: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("unmarshal seed: %v", err)
	}
	want := map[string]any{
		"image": "archie-agent:test", "max_concurrency": float64(4),
		"max_uptime": "2h0m0s", "volume_ttl": "24h0m0s",
		"pull_policy": "missing", "network": "bridge",
	}
	for key, value := range want {
		if document[key] != value {
			t.Errorf("seed %s = %#v, want %#v", key, document[key], value)
		}
	}
	// The Go field names encoding/json fell back to when the document was the
	// internal config struct.
	for _, legacy := range []string{"Image", "MaxConcurrency", "MaxUptime", "VolumeTTL", "PullPolicy", "Network"} {
		if _, present := document[legacy]; present {
			t.Errorf("seed carries the Go-cased key %q", legacy)
		}
	}
	// Profiles is its own resource (AgentProfileKind, applying live where the
	// rest of this document is restart-required), so it never appears here at
	// all, under either casing.
	if _, present := document["profiles"]; present {
		t.Error("seed carries profiles: it belongs to AgentProfileKind now")
	}
	if _, present := document["Profiles"]; present {
		t.Error("seed carries Profiles: it belongs to AgentProfileKind now")
	}
}

// legacyContainerDocument is a document as an earlier revision stored it: the
// Go field names encoding/json fell back to while the document was the internal
// config struct, LegacyEnabled from the short window in which the removed
// containers.enabled switch still decoded, and Profiles from before it moved to
// its own resource, AgentProfileKind. Every store written before either reshape
// holds a document like this one, so it has to keep working -- Profiles simply
// drops, the way LegacyEnabled already does.
const legacyContainerDocument = `{"Image":"archie-agent:test","MaxConcurrency":4,"MaxUptime":"2h0m0s","VolumeTTL":"24h0m0s","PullPolicy":"missing","Network":"bridge","LegacyEnabled":false,"Profiles":{"net":{"Image":"proxy:1","Tools":["whois"]}}}`

// TestContainerRuntimePoliciesReadsADocumentWrittenBeforeTheReshape covers the
// two halves the change needs: a legacy document still validates and still
// reads, and writing it back (Definition.Decode is what every write path runs)
// rewrites it into the current shape. The control plane fails closed, so a
// shape change without this tolerance stops archied starting on a store that
// already holds the old document.
func TestContainerRuntimePoliciesReadsADocumentWrittenBeforeTheReshape(t *testing.T) {
	definition := containerPoliciesDefinition(t)

	if err := definition.Validate([]byte(legacyContainerDocument)); err != nil {
		t.Fatalf("Validate(legacy) = %v, want it accepted: it is what existing stores hold", err)
	}

	decoded, err := definition.Decode([]byte(legacyContainerDocument))
	if err != nil {
		t.Fatalf("Decode(legacy) = %v", err)
	}
	for _, legacy := range []string{`"Image"`, `"MaxConcurrency"`, `"LegacyEnabled"`, `"Profiles"`, `"profiles"`} {
		if strings.Contains(string(decoded), legacy) {
			t.Fatalf("decoded document = %s, want the canonical keys without %q", decoded, legacy)
		}
	}
	var document struct {
		Image          string `json:"image"`
		MaxConcurrency int    `json:"max_concurrency"`
		MaxUptime      string `json:"max_uptime"`
		VolumeTTL      string `json:"volume_ttl"`
		PullPolicy     string `json:"pull_policy"`
		Network        string `json:"network"`
	}
	if err := json.Unmarshal(decoded, &document); err != nil {
		t.Fatalf("unmarshal decoded document: %v", err)
	}
	if document.Image != "archie-agent:test" || document.MaxConcurrency != 4 ||
		document.MaxUptime != "2h0m0s" || document.VolumeTTL != "24h0m0s" ||
		document.PullPolicy != "missing" || document.Network != "bridge" {
		t.Errorf("document = %+v, want the legacy values preserved in the current shape", document)
	}
}

// TestContainerRuntimePoliciesKeepsDisallowUnknownFields: the legacy tolerance
// renames the keys an existing store holds and nothing else (plus dropping
// profiles/Profiles, which no longer belongs to this document at all), because
// swallowing any other unknown key decodes as "unset" and an unset image is a
// broken worker.
func TestContainerRuntimePoliciesKeepsDisallowUnknownFields(t *testing.T) {
	if err := containerPoliciesDefinition(t).Validate([]byte(`{"image":"archie-agent:test","maxconcurrency":4}`)); err == nil {
		t.Error("Validate accepted a misspelled key")
	}
}

// TestContainerRuntimePoliciesLayersLegacyDocuments walks the stored forms to
// config.Containers: the snake_case document and the Go-cased one an existing
// store holds. Both have to reach the running config, or a deployment's
// container policies silently stop applying the next time it boots.
func TestContainerRuntimePoliciesLayersLegacyDocuments(t *testing.T) {
	for _, tt := range []struct {
		name   string
		stored string
	}{
		{
			name:   "the current shape",
			stored: `{"image":"archie-agent:test","max_concurrency":4,"max_uptime":"2h0m0s","volume_ttl":"24h0m0s","pull_policy":"missing","network":"bridge"}`,
		},
		{
			name:   "the Go-cased shape an existing store holds",
			stored: legacyContainerDocument,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := NewRPCClient(&runtimeConfigClient{values: map[string]any{
				SchedulingPolicyKind:         map[string]any{"poll_interval": "2m", "dispatch": map[string]any{"trigger": "assignee"}},
				ContainerRuntimePoliciesKind: json.RawMessage(tt.stored),
			}})
			got, _, err := client.RuntimeConfig(t.Context(), config.Config{})
			if err != nil {
				t.Fatalf("RuntimeConfig: %v", err)
			}
			if got.Containers.Image != "archie-agent:test" || got.Containers.MaxConcurrency != 4 ||
				got.Containers.MaxUptime != config.Duration(2*time.Hour) || got.Containers.Network != "bridge" {
				t.Fatalf("Containers = %+v, want the stored document applied", got.Containers)
			}
			// Profiles is AgentProfileKind's document now, not this one's; this
			// resource carries none, under either casing, so it never reaches
			// out.Containers.Profiles.
			if len(got.Containers.Profiles) != 0 {
				t.Fatalf("Profiles = %+v, want none: container-runtime-policies carries no profiles", got.Containers.Profiles)
			}
		})
	}
}
