package controlplane

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
)

type schemaFixtureNested struct {
	Prompt string `json:"prompt"`
}

type schemaFixtureEmbedded struct {
	Shared string `json:"shared"`
}

type schemaFixture struct {
	schemaFixtureEmbedded
	Name     string          `json:"name" title:"Display name"`
	Skipped  string          `json:"-"`
	Untagged string          // no json tag: the document key is the Go name
	Count    int             `json:"count"`
	Ratio    float64         `json:"ratio"`
	Enabled  bool            `json:"enabled"`
	Window   config.Duration `json:"window"`
	Lifetime channelDuration `json:"lifetime"`
	// A bare time.Duration marshals as its nanosecond count, unlike the two
	// named types above, which marshal as the string time.ParseDuration reads.
	// The schema describes the wire form, not the Go type's name, or a client
	// renders a text box for a number.
	Timeout time.Duration       `json:"timeout"`
	When    time.Time           `json:"when"`
	Tags    []string            `json:"tags"`
	Roles   map[string]string   `json:"roles"`
	Nested  schemaFixtureNested `json:"nested"`
	Hinted  string              `json:"hinted" format:"duration" doc:"A hint."`

	unexported string
}

// TestSchemaDerivesKeysTypesAndAnnotations pins the derivation itself, including
// the rules that are easy to get subtly wrong: a field with no json tag is
// described by the Go name the document actually stores, a named duration type
// is a string with a format because that is what it marshals as, and a bare
// time.Duration stays an integer for the same reason.
func TestSchemaDerivesKeysTypesAndAnnotations(t *testing.T) {
	expected := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"shared":   map[string]any{"type": "string"},
			"name":     map[string]any{"type": "string", "title": "Display name"},
			"Untagged": map[string]any{"type": "string"},
			"count":    map[string]any{"type": "integer"},
			"ratio":    map[string]any{"type": "number"},
			"enabled":  map[string]any{"type": "boolean"},
			"window":   map[string]any{"type": "string", "format": "duration"},
			"lifetime": map[string]any{"type": "string", "format": "duration"},
			"timeout":  map[string]any{"type": "integer"},
			"when":     map[string]any{"type": "string", "format": "date-time"},
			"tags":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"roles":    map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
			"nested":   map[string]any{"type": "object", "properties": map[string]any{"prompt": map[string]any{"type": "string"}}},
			"hinted":   map[string]any{"type": "string", "format": "duration", "description": "A hint."},
		},
	}
	var got map[string]any
	// The unexported field is populated rather than left blank, so the
	// assertion below proves it is omitted even when it carries a value.
	if err := json.Unmarshal([]byte(schemaJSON(schemaFixture{unexported: "not in the document"})), &got); err != nil {
		t.Fatalf("unmarshal derived schema: %v", err)
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("derived schema =\n%#v\nwant\n%#v", got, expected)
	}
	properties, ok := got["properties"].(map[string]any)
	if !ok {
		t.Fatalf("derived schema has no properties object: %#v", got)
	}
	if _, present := properties["Skipped"]; present {
		t.Error(`json:"-" field is described, so the schema names a key the document never stores`)
	}
	if _, present := properties["unexported"]; present {
		t.Error("unexported field is described")
	}
}

// TestEveryResourceDescriptorDerivesProperties is the regression guard for the
// defect this replaced: twelve definitions each advertised a bare
// {"type":"object"} or {"type":"array"}, so no descriptor carried a single field
// for the Web UI to label, describe or format. Every kind here must describe
// something, and this walks the catalogue the constructor production uses rather
// than the definitions in isolation.
func TestEveryResourceDescriptorDerivesProperties(t *testing.T) {
	server := testServer(t, nil)
	catalog, err := server.Catalog(t.Context(), nil)
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	describing := 0
	for _, descriptor := range catalog.Resources {
		if descriptor.SchemaJson == "" {
			// Domain-managed kinds (identities, captures, mappings, bindings)
			// carry commands rather than a document this side validates.
			continue
		}
		describing++
		var schema map[string]any
		if err := json.Unmarshal([]byte(descriptor.SchemaJson), &schema); err != nil {
			t.Errorf("%s: schema is not JSON: %v", descriptor.Kind, err)
			continue
		}
		switch schema["type"] {
		case "object":
			// A fixed document describes properties; a keyed collection
			// (providers, model roles) describes additionalProperties and has
			// no fixed keys by design. Either is a described document; neither
			// is the bare object this guard exists to catch.
			properties, hasProperties := schema["properties"].(map[string]any)
			_, hasAdditional := schema["additionalProperties"].(map[string]any)
			if (!hasProperties || len(properties) == 0) && !hasAdditional {
				t.Errorf("%s: object schema describes no fields: %s", descriptor.Kind, descriptor.SchemaJson)
			}
		case "array":
			if _, ok := schema["items"].(map[string]any); !ok {
				t.Errorf("%s: array schema describes no items: %s", descriptor.Kind, descriptor.SchemaJson)
			}
		default:
			t.Errorf("%s: schema type = %#v, want object or array", descriptor.Kind, schema["type"])
		}
	}
	if describing == 0 {
		t.Fatal("no descriptor advertised a schema: the guard asserted nothing")
	}
}

// TestChannelSettingsDescriptorFormatsTheRateLimitWindow is the concrete payoff:
// the field that asked an operator for a nanosecond count now carries a format a
// client can render an affordance for, and it arrives by derivation rather than
// by anyone remembering to write it down.
func TestChannelSettingsDescriptorFormatsTheRateLimitWindow(t *testing.T) {
	server := testServer(t, nil)
	catalog, err := server.Catalog(t.Context(), nil)
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	var schema struct {
		Properties struct {
			RateLimit struct {
				Properties struct {
					Window struct {
						Type   string `json:"type"`
						Format string `json:"format"`
					} `json:"window"`
					MaxRequests struct {
						Type string `json:"type"`
					} `json:"max_requests"`
				} `json:"properties"`
			} `json:"rate_limit"`
			Email struct {
				Properties map[string]struct {
					Type string `json:"type"`
				} `json:"properties"`
			} `json:"email"`
		} `json:"properties"`
	}
	for _, descriptor := range catalog.Resources {
		if descriptor.Kind != ChannelSettingsKind {
			continue
		}
		if err := json.Unmarshal([]byte(descriptor.SchemaJson), &schema); err != nil {
			t.Fatalf("unmarshal channel-settings schema: %v", err)
		}
	}
	if schema.Properties.RateLimit.Properties.Window.Format != "duration" {
		t.Errorf("rate_limit.window format = %q, want \"duration\"", schema.Properties.RateLimit.Properties.Window.Format)
	}
	if schema.Properties.RateLimit.Properties.MaxRequests.Type != "integer" {
		t.Errorf("rate_limit.max_requests type = %q, want \"integer\"", schema.Properties.RateLimit.Properties.MaxRequests.Type)
	}
	if len(schema.Properties.Email.Properties) == 0 {
		t.Error("email describes no properties, want listen_addr and relay_addr")
	}
}
