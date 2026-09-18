package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/invopop/jsonschema"
)

type contractWithConflictingDefinition struct {
	Value conflictingDefinition `json:"value"`
}

type conflictingDefinition struct {
	Value string `json:"value"`
}

func TestRunWritesDeterministicContractData(t *testing.T) {
	repoRoot := filepath.Clean("../..")
	outputDir := t.TempDir()
	outputs := []string{
		filepath.Join(outputDir, "first.json"),
		filepath.Join(outputDir, "second.json"),
	}

	for _, output := range outputs {
		if err := run(repoRoot, output); err != nil {
			t.Fatalf("run(%q) error = %v", output, err)
		}
	}

	first, err := os.ReadFile(outputs[0])
	if err != nil {
		t.Fatalf("read first output: %v", err)
	}
	second, err := os.ReadFile(outputs[1])
	if err != nil {
		t.Fatalf("read second output: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("repeated generation produced different bytes")
	}

	var data struct {
		GeneratedBy string                     `json:"generatedBy"`
		Schemas     map[string]json.RawMessage `json:"schemas"`
	}
	if err := json.Unmarshal(first, &data); err != nil {
		t.Fatalf("decode generated data: %v", err)
	}
	if data.GeneratedBy != "tools/docsgen" {
		t.Fatalf("generatedBy = %q, want %q", data.GeneratedBy, "tools/docsgen")
	}
	for _, name := range []string{
		"AgentExecutionRequest",
		"AgentExecutionResult",
		"MessageEvent",
	} {
		if _, ok := data.Schemas[name]; !ok {
			t.Errorf("schemas[%q] is missing", name)
		}
	}
	for name, schema := range data.Schemas {
		assertSchemaRefsResolve(t, name, schema, data.Schemas)
	}
}

func TestRunHonoursAbsoluteOutputPath(t *testing.T) {
	repoRoot := filepath.Clean("../..")
	outputDir := t.TempDir()
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}

	for _, name := range []string{"first.yaml", "second.yaml"} {
		output := filepath.Join(outputDir, name)
		if err := run(repoRoot, output); err != nil {
			t.Fatalf("run(%q) error = %v", name, err)
		}
		if _, err := os.Stat(output); err != nil {
			t.Fatalf("generated output %q: %v", name, err)
		}
	}
	currentDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get restored working directory: %v", err)
	}
	if currentDir != originalDir {
		t.Fatalf("working directory = %q, want %q", currentDir, originalDir)
	}
}

func TestCollectSchemaRejectsDuplicatePublishedName(t *testing.T) {
	reflector := &jsonschema.Reflector{ExpandedStruct: true}
	schemas := map[string]any{}
	const name = "PublishedContract"

	if err := collectSchema(reflector, schemas, name, reflect.TypeFor[struct {
		First string `json:"first"`
	}]()); err != nil {
		t.Fatalf("collect first schema: %v", err)
	}
	err := collectSchema(reflector, schemas, name, reflect.TypeFor[struct {
		Second int `json:"second"`
	}]())
	if err == nil {
		t.Fatal("collect duplicate schema returned nil error")
	}
}

func TestCollectSchemaRejectsConflictingDefinition(t *testing.T) {
	reflector := &jsonschema.Reflector{ExpandedStruct: true}
	schemas := map[string]any{
		"conflictingDefinition": map[string]any{"type": "integer"},
	}

	err := collectSchema(
		reflector,
		schemas,
		"PublishedContract",
		reflect.TypeFor[contractWithConflictingDefinition](),
	)
	if err == nil {
		t.Fatal("collect conflicting definition returned nil error")
	}
	if !strings.Contains(err.Error(), `conflicting schema definition "conflictingDefinition"`) {
		t.Fatalf("collect conflicting definition error = %q", err)
	}
}

func TestRunReportsFilesystemAndSourceErrors(t *testing.T) {
	repoRoot := filepath.Clean("../..")
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("occupied"), 0o600); err != nil {
		t.Fatalf("create blocker: %v", err)
	}

	tests := []struct {
		name     string
		root     string
		output   string
		contains string
	}{
		{
			name:     "missing source tree",
			root:     t.TempDir(),
			output:   filepath.Join(t.TempDir(), "contracts.json"),
			contains: "load Go comments",
		},
		{
			name:     "output parent is a file",
			root:     repoRoot,
			output:   filepath.Join(blocker, "contracts.json"),
			contains: "create output directory",
		},
		{
			name:     "output is a directory",
			root:     repoRoot,
			output:   t.TempDir(),
			contains: "write ",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := run(test.root, test.output)
			if err == nil {
				t.Fatal("run() returned nil error")
			}
			if !bytes.Contains([]byte(err.Error()), []byte(test.contains)) {
				t.Fatalf("run() error = %q, want substring %q", err, test.contains)
			}
		})
	}
}

func TestCheckAcceptsTheCommittedArtifact(t *testing.T) {
	repoRoot := filepath.Clean("../..")

	if err := check(repoRoot, defaultOutputPath); err != nil {
		t.Fatalf("check(%q) error = %v; committed generated data is stale", defaultOutputPath, err)
	}
}

func TestCheckReportsChangedSchema(t *testing.T) {
	repoRoot := filepath.Clean("../..")
	output := filepath.Join(t.TempDir(), "contracts.json")
	if err := run(repoRoot, output); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	mutateGeneratedData(t, output, func(data map[string]any) {
		schemas := data["schemas"].(map[string]any)
		schemas["AgentExecutionResult"].(map[string]any)["title"] = "tampered"
	})

	err := check(repoRoot, output)
	if err == nil {
		t.Fatal("check() returned nil error for a changed artifact")
	}
	for _, want := range []string{"changed", "AgentExecutionResult", "agentexec.Result"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("check() error = %q, want substring %q", err, want)
		}
	}
}

func TestCheckReportsRemovedSchema(t *testing.T) {
	repoRoot := filepath.Clean("../..")
	output := filepath.Join(t.TempDir(), "contracts.json")
	if err := run(repoRoot, output); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	mutateGeneratedData(t, output, func(data map[string]any) {
		delete(data["schemas"].(map[string]any), "MessageEvent")
	})

	err := check(repoRoot, output)
	if err == nil {
		t.Fatal("check() returned nil error for a removed schema")
	}
	if !strings.Contains(err.Error(), "MessageEvent") {
		t.Errorf("check() error = %q, want the removed schema named", err)
	}
}

func TestCheckReportsMissingArtifact(t *testing.T) {
	repoRoot := filepath.Clean("../..")
	output := filepath.Join(t.TempDir(), "contracts.json")

	err := check(repoRoot, output)
	if err == nil {
		t.Fatal("check() returned nil error for a missing artifact")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Fatalf("check() error = %q, want it to report a missing artifact", err)
	}
	if _, statErr := os.Stat(output); statErr == nil {
		t.Fatal("check() created the missing artifact instead of reporting it")
	}
}

func TestCheckReportsObsoleteArtifact(t *testing.T) {
	repoRoot := filepath.Clean("../..")
	outputDir := t.TempDir()
	if err := run(repoRoot, filepath.Join(outputDir, "contracts.json")); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	obsolete := filepath.Join(outputDir, "orphaned.json")
	if err := os.WriteFile(obsolete, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write obsolete artifact: %v", err)
	}

	err := check(repoRoot, filepath.Join(outputDir, "contracts.json"))
	if err == nil {
		t.Fatal("check() returned nil error for an obsolete artifact")
	}
	if !strings.Contains(err.Error(), "obsolete") || !strings.Contains(err.Error(), "orphaned.json") {
		t.Fatalf("check() error = %q, want the obsolete file named", err)
	}
}

func TestCheckNeverModifiesTheWorkingTree(t *testing.T) {
	repoRoot := filepath.Clean("../..")
	outputDir := t.TempDir()
	output := filepath.Join(outputDir, "contracts.json")
	if err := run(repoRoot, output); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	mutateGeneratedData(t, output, func(data map[string]any) {
		data["generatedBy"] = "tampered"
	})
	before, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read before: %v", err)
	}
	beforeInfo, err := os.Stat(output)
	if err != nil {
		t.Fatalf("stat before: %v", err)
	}

	if err := check(repoRoot, output); err == nil {
		t.Fatal("check() returned nil error for a changed artifact")
	}

	after, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("check() rewrote the artifact under test")
	}
	afterInfo, err := os.Stat(output)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if beforeInfo.Mode() != afterInfo.Mode() {
		t.Fatalf("check() changed the artifact mode: %v -> %v", beforeInfo.Mode(), afterInfo.Mode())
	}
	if entries, err := os.ReadDir(outputDir); err != nil {
		t.Fatalf("read output dir: %v", err)
	} else if len(entries) != 1 {
		t.Fatalf("check() left %d files in the output dir, want 1", len(entries))
	}
}

func mutateGeneratedData(t *testing.T, path string, mutate func(map[string]any)) {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	mutate(data)
	body, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestRewriteRef(t *testing.T) {
	tests := map[string]string{
		"definition": "#/schemas/Contract",
		"external":   "https://example.com/schema.json",
		"empty name": "#/$defs/",
	}

	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			input := want
			if name == "definition" {
				input = "#/$defs/Contract"
			}
			if got := rewriteRef(input); got != want {
				t.Fatalf("rewriteRef(%q) = %q, want %q", input, got, want)
			}
		})
	}
}

func assertSchemaRefsResolve(
	t *testing.T,
	name string,
	raw json.RawMessage,
	schemas map[string]json.RawMessage,
) {
	t.Helper()

	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode schema %q: %v", name, err)
	}
	walkJSON(value, func(ref string) {
		const prefix = "#/schemas/"
		if !strings.HasPrefix(ref, "#") {
			return
		}
		if !strings.HasPrefix(ref, prefix) {
			t.Errorf("schema %q contains unresolved local reference %q", name, ref)
			return
		}
		target := ref[len(prefix):]
		if _, ok := schemas[target]; !ok {
			t.Errorf("schema %q references missing schema %q", name, target)
		}
	})
}

func walkJSON(value any, visitRef func(string)) {
	switch node := value.(type) {
	case map[string]any:
		for key, child := range node {
			if key == "$ref" {
				if ref, ok := child.(string); ok {
					visitRef(ref)
				}
				continue
			}
			walkJSON(child, visitRef)
		}
	case []any:
		for _, child := range node {
			walkJSON(child, visitRef)
		}
	}
}
