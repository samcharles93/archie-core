package agentexec

import (
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/samcharles93/archie-core/internal/domain/agentrun"
)

// toolCallDetailBytes caps the summary ToolCallReport.Detail carries. This
// rides on every task's observability event stream, so one large file read
// must not dominate it the way baselineMissionBytes-sized content is allowed
// to dominate a builder's own context.
const toolCallDetailBytes = 300

func ClipToolCallDetail(s string) string {
	if len(s) <= toolCallDetailBytes {
		return s
	}
	return s[:toolCallDetailBytes] + "…"
}

func ProjectScopedRules(workspace, extra string) string {
	rule := "Confine discovery to " + workspace + ". Do not inspect home directories, dependency/module caches, or unrelated projects unless the mission explicitly requests that external path."
	if strings.TrimSpace(extra) == "" {
		return rule
	}
	return rule + "\n" + extra
}

func ProtectionMatcher(p agentrun.Protection, readOnly bool) func(string) bool {
	if readOnly || len(p.Suffixes)+len(p.Globs) == 0 {
		return nil
	}
	return func(path string) bool {
		for _, suffix := range p.Suffixes {
			if strings.HasSuffix(path, suffix) {
				return true
			}
		}
		for _, glob := range p.Globs {
			matchPath := path
			if !strings.ContainsAny(glob, `/\`) {
				matchPath = filepath.Base(path)
			}
			if matched, err := filepath.Match(glob, matchPath); err == nil && matched {
				return true
			}
		}
		return false
	}
}

// ValidateCaptureArgs checks that the capture tool arguments satisfy all
// constraints. Returns a rejection message and false on failure, or "" and
// true on success. Each constraint is its own check so adding one does not
// grow a single branching function.
func ValidateCaptureArgs(spec agentrun.CaptureTool, value json.RawMessage) (string, bool) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(value, &object); err != nil {
		return spec.Name + " rejected: arguments must be a JSON object", false // the agent loop must see malformed tool arguments as feedback it can correct, not as a failed tool call
	}
	for _, check := range []func(agentrun.CaptureTool, map[string]json.RawMessage) (string, bool){
		checkRequiredFields,
		checkNonEmptyStrings,
		checkBooleanFields,
		checkRequiredWhenTrue,
	} {
		if rejection, ok := check(spec, object); !ok {
			return rejection, false
		}
	}
	// The derived checks above cover the common cases with friendly wording;
	// the schema is what enforces the rest of what the tool advertised --
	// enums, numeric types, nested objects, array shapes -- so a call the
	// agent could only make valid is rejected with the reason, not recorded.
	return checkSchema(spec, value)
}

// checkSchema validates the arguments against the tool's advertised JSON
// Schema. A tool with no schema, or one that will not resolve, is accepted
// here: a schema that never resolves is a definition bug, refused where the
// tool is built (workflow's agentResultTool), so the agent is never blamed
// for it.
func checkSchema(spec agentrun.CaptureTool, value json.RawMessage) (string, bool) {
	if len(spec.Parameters) == 0 {
		return "", true
	}
	resolved, err := resolveCaptureSchema(spec.Parameters)
	if err != nil {
		return "", true
	}
	var instance any
	if err := json.Unmarshal(value, &instance); err != nil {
		return spec.Name + " rejected: arguments must be JSON", false
	}
	if err := resolved.Validate(instance); err != nil {
		return fmt.Sprintf("%s rejected: %v", spec.Name, err), false
	}
	return "", true
}

// resolveCaptureSchema parses and resolves a capture tool's JSON Schema. The
// workflow parser resolves the same schema when it builds the tool, so an
// unusable schema is refused at definition time rather than silently skipped
// here.
func resolveCaptureSchema(params json.RawMessage) (*jsonschema.Resolved, error) {
	var schema jsonschema.Schema
	if err := json.Unmarshal(params, &schema); err != nil {
		return nil, fmt.Errorf("capture tool schema: %w", err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return nil, fmt.Errorf("capture tool schema: %w", err)
	}
	return resolved, nil
}

func checkRequiredFields(spec agentrun.CaptureTool, object map[string]json.RawMessage) (string, bool) {
	for _, field := range spec.RequiredFields {
		if _, ok := object[field]; !ok {
			return fmt.Sprintf("%s rejected: %s is required", spec.Name, field), false
		}
	}
	return "", true
}

func checkNonEmptyStrings(spec agentrun.CaptureTool, object map[string]json.RawMessage) (string, bool) {
	for _, field := range spec.NonEmptyStrings {
		if !isNonEmptyString(object, field) {
			return fmt.Sprintf("%s rejected: %s must be a non-empty string", spec.Name, field), false
		}
	}
	return "", true
}

func checkBooleanFields(spec agentrun.CaptureTool, object map[string]json.RawMessage) (string, bool) {
	for _, field := range spec.BooleanFields {
		var val bool
		if raw, ok := object[field]; !ok || json.Unmarshal(raw, &val) != nil {
			return fmt.Sprintf("%s rejected: %s must be a boolean", spec.Name, field), false
		}
	}
	return "", true
}

// checkRequiredWhenTrue enforces the conditional requirements. Triggers are
// visited in sorted order so a call violating two of them names the same one
// every run, rather than a different one on each retry.
func checkRequiredWhenTrue(spec agentrun.CaptureTool, object map[string]json.RawMessage) (string, bool) {
	for _, trigger := range slices.Sorted(maps.Keys(spec.RequiredWhenTrue)) {
		var on bool
		if raw, ok := object[trigger]; !ok || json.Unmarshal(raw, &on) != nil || !on {
			continue
		}
		for _, field := range spec.RequiredWhenTrue[trigger] {
			if !isNonEmptyString(object, field) {
				return fmt.Sprintf("%s rejected: %s is required when %s is true", spec.Name, field, trigger), false
			}
		}
	}
	return "", true
}

func isNonEmptyString(object map[string]json.RawMessage, field string) bool {
	var text string
	raw, ok := object[field]
	return ok && json.Unmarshal(raw, &text) == nil && strings.TrimSpace(text) != ""
}
