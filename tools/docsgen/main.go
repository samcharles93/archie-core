// Command docsgen generates reference schemas from Archie-owned Go contracts.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/invopop/jsonschema"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/gateway"
)

func main() {
	repoRoot := flag.String("repo-root", "..", "path to the Archie repository root")
	out := flag.String(
		"out",
		"docs/data/generated/contracts.json",
		"output path relative to the repository root",
	)
	flag.Parse()

	if err := run(*repoRoot, *out); err != nil {
		log.Fatalf("docsgen: %v", err)
	}
}

func run(repoRoot, outPath string) error {
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return fmt.Errorf("resolve repository root: %w", err)
	}

	reflector := &jsonschema.Reflector{
		ExpandedStruct: true,
		DoNotReference: false,
	}
	if err := loadGoComments(reflector, absRoot); err != nil {
		return fmt.Errorf("load Go comments: %w", err)
	}

	schemas := map[string]any{}
	types := currentContractTypes()
	names := make([]string, 0, len(types))
	for name := range types {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := collectSchema(reflector, schemas, name, types[name]); err != nil {
			return fmt.Errorf("reflect %s: %w", name, err)
		}
	}
	rewriteDefRefs(schemas)

	body, err := json.MarshalIndent(struct {
		GeneratedBy string         `json:"generatedBy"`
		Schemas     map[string]any `json:"schemas"`
	}{
		GeneratedBy: "tools/docsgen",
		Schemas:     schemas,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal documentation data: %w", err)
	}
	body = append(body, '\n')

	destination := outPath
	if !filepath.IsAbs(destination) {
		destination = filepath.Join(absRoot, destination)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	if err := os.WriteFile(destination, body, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", destination, err)
	}

	fmt.Printf("docsgen: wrote %d schemas to %s\n", len(schemas), destination)
	return nil
}

// currentContractTypes adapts the pre-domain-registry contract set during the
// architecture migration. Delete it when agent execution and messaging expose
// domain-owned documentation registries; docsgen must then consume those
// registries directly.
func currentContractTypes() map[string]reflect.Type {
	return map[string]reflect.Type{
		"AgentExecutionRequest": reflect.TypeFor[agentexec.Request](),
		"AgentExecutionResult":  reflect.TypeFor[agentexec.Result](),
		"MessageEvent":          reflect.TypeFor[gateway.MessageEvent](),
	}
}

func loadGoComments(reflector *jsonschema.Reflector, repoRoot string) error {
	const modulePath = "github.com/samcharles93/archie-core"
	if err := reflector.AddGoComments("", filepath.Join(repoRoot, "internal")); err != nil {
		return err
	}

	rootPrefix := filepath.ToSlash(repoRoot) + "/"
	comments := make(map[string]string, len(reflector.CommentMap))
	for name, comment := range reflector.CommentMap {
		relativeName := strings.TrimPrefix(filepath.ToSlash(name), rootPrefix)
		comments[modulePath+"/"+relativeName] = comment
	}
	reflector.CommentMap = comments
	return nil
}

func collectSchema(
	reflector *jsonschema.Reflector,
	schemas map[string]any,
	name string,
	t reflect.Type,
) error {
	if _, exists := schemas[name]; exists {
		return fmt.Errorf("duplicate published schema name %q", name)
	}
	raw, err := json.Marshal(reflector.ReflectFromType(t))
	if err != nil {
		return err
	}
	var tree map[string]any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return err
	}
	defs, _ := tree["$defs"].(map[string]any)
	delete(tree, "$defs")
	delete(tree, "$schema")
	delete(tree, "$id")
	schemas[name] = tree
	for defName, defSchema := range defs {
		if existing, exists := schemas[defName]; exists {
			if !reflect.DeepEqual(existing, defSchema) {
				return fmt.Errorf("conflicting schema definition %q", defName)
			}
			continue
		}
		schemas[defName] = defSchema
	}
	return nil
}

func rewriteDefRefs(schemas map[string]any) {
	for name, schema := range schemas {
		schemas[name] = rewriteNode(schema)
	}
}

func rewriteNode(value any) any {
	switch node := value.(type) {
	case map[string]any:
		for key, child := range node {
			if key == "$ref" {
				if ref, ok := child.(string); ok {
					node[key] = rewriteRef(ref)
					continue
				}
			}
			node[key] = rewriteNode(child)
		}
		return node
	case []any:
		for index, child := range node {
			node[index] = rewriteNode(child)
		}
		return node
	default:
		return value
	}
}

func rewriteRef(ref string) string {
	const prefix = "#/$defs/"
	if len(ref) > len(prefix) && ref[:len(prefix)] == prefix {
		return "#/schemas/" + ref[len(prefix):]
	}
	return ref
}
