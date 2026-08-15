package plugin_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCapabilityHostContractExists(t *testing.T) {
	t.Parallel()

	pluginDir := filepath.Clean(".")
	entries, err := os.ReadDir(pluginDir)
	if err != nil {
		t.Fatal(err)
	}

	types := make(map[string]bool)
	hostMethods := make(map[string]bool)
	functions := make(map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(pluginDir, entry.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			switch decl := decl.(type) {
			case *ast.GenDecl:
				for _, spec := range decl.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if ok {
						types[typeSpec.Name.Name] = true
					}
				}
			case *ast.FuncDecl:
				if receiverNameForContract(decl) == "Host" {
					hostMethods[decl.Name.Name] = true
				}
				if decl.Recv == nil {
					functions[decl.Name.Name] = true
				}
			}
		}
	}

	for _, name := range []string{"Manifest", "Module", "LifecycleState", "Health", "Host"} {
		if !types[name] {
			t.Errorf("plugin package does not define %s", name)
		}
	}
	for _, name := range []string{"Register", "Start", "Health", "Stop", "Manifests"} {
		if !hostMethods[name] {
			t.Errorf("plugin.Host does not define %s", name)
		}
	}
	if !functions["AdaptLegacy"] {
		t.Error("plugin package does not define AdaptLegacy")
	}
}

func TestCapabilityHostContractHasNoGenericCapabilityHooks(t *testing.T) {
	t.Parallel()

	disallowed := []string{"AddHook", "RegisterHook", "RegisterCapability", "Service", "Services"}
	slices.Sort(disallowed)

	pluginType := parseNamedInterface(t, "plugin.go", "Plugin")
	for _, field := range pluginType.Methods.List {
		for _, name := range field.Names {
			if _, found := slices.BinarySearch(disallowed, name.Name); found {
				t.Errorf("plugin.Plugin contains forbidden generic hook %s", name.Name)
			}
		}
	}
}

func TestArchiedWiresCapabilityHostLifecycle(t *testing.T) {
	t.Parallel()

	// The daemon composition lives in cmd/archied, whose wiring is now
	// split across main.go (driver) and bootstrap.go (phase methods); the
	// contract reads the whole package so the enforcement survives that
	// split.
	archiedDir := filepath.Join("..", "..", "cmd", "archied")
	var text strings.Builder
	entries, err := os.ReadDir(archiedDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		source, err := os.ReadFile(filepath.Join(archiedDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		text.Write(source)
		text.WriteString("\n")
	}
	wiring := text.String()
	for _, required := range []string{
		"plugin.NewHost()",
		"plugin.AdaptLegacy(",
		"capabilityHost.Register(",
		"capabilityHost.Start(",
		"capabilityHost.Stop(",
		"CapabilityHost:",
	} {
		if !strings.Contains(wiring, required) {
			t.Errorf("cmd/archied does not contain capability-host wiring %q", required)
		}
	}
	before, _, ok := strings.Cut(wiring, "capabilityHost.Start(")
	if !ok {
		t.Fatal("capability host Start call not found")
	}
	if !strings.Contains(before, "capabilityHost.Stop(") {
		t.Error("capability host cleanup must be registered before Start so failed rollback cleanup is retried")
	}
	if strings.Contains(wiring, "PluginRegistry:") {
		t.Error("daemon composition exposes divergent legacy plugin inventory")
	}
}

func parseNamedInterface(t *testing.T, filename, typeName string) *ast.InterfaceType {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != typeName {
				continue
			}
			iface, ok := typeSpec.Type.(*ast.InterfaceType)
			if !ok {
				t.Fatalf("%s is not an interface", typeName)
			}
			return iface
		}
	}
	t.Fatalf("%s not found in %s", typeName, filename)
	return nil
}

func receiverNameForContract(decl *ast.FuncDecl) string {
	if decl.Recv == nil || len(decl.Recv.List) == 0 {
		return ""
	}
	switch receiver := decl.Recv.List[0].Type.(type) {
	case *ast.Ident:
		return receiver.Name
	case *ast.StarExpr:
		ident, _ := receiver.X.(*ast.Ident)
		if ident != nil {
			return ident.Name
		}
	}
	return ""
}
