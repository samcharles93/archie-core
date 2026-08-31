package plugin_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	engineRuleHeading = "## Plugin engine rule (strict)"
	engineRuleLink    = "ARCHITECTURE.md#plugin-engine-rule-strict"
)

func TestPluginEngineRuleIsCanonicalAndSharedByAgentInstructions(t *testing.T) {
	t.Parallel()

	architecture := readTestFile(t, filepath.Join("..", "..", "ARCHITECTURE.md"))
	if !strings.Contains(architecture, engineRuleHeading) {
		t.Fatalf("ARCHITECTURE.md missing canonical heading %q", engineRuleHeading)
	}

	claudeMD := readTestFile(t, filepath.Join("..", "..", "CLAUDE.md"))
	if !strings.Contains(claudeMD, engineRuleLink) {
		t.Fatalf("CLAUDE.md missing reference to %q", engineRuleLink)
	}
}

func TestEngineFamilyInspectionRejectsInvalidShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		src   string
		valid bool
	}{
		{
			name: "typed family",
			src: `package fixture
type Engine interface { Name() string; Version() string; Resolve(string) (string, error) }
type Registry struct{}
func (*Registry) Register(Engine) {}
func (*Registry) Get(string) (Engine, bool) { return nil, false }
`,
			valid: true,
		},
		{
			name: "embedded typed family",
			src: `package fixture
type Resolver interface { Resolve(string) (string, error) }
type Engine interface { Resolver }
type Registry struct{}
func (*Registry) Register(Engine) {}
func (*Registry) Get(string) (Engine, bool) { return nil, false }
`,
			valid: true,
		},
		{
			name: "metadata only",
			src: `package fixture
type Engine interface { Name() string; Version() string }
type Registry struct{}
func (*Registry) Register(Engine) {}
func (*Registry) Get(string) (Engine, bool) { return nil, false }
`,
		},
		{
			name: "lifecycle only",
			src: `package fixture
type Engine interface {
	Name() string
	Version() string
	Start() error
	Health() string
	Stop() error
}
type Registry struct{}
func (*Registry) Register(Engine) {}
func (*Registry) Get(string) (Engine, bool) { return nil, false }
`,
		},
		{
			name: "embedded metadata only",
			src: `package fixture
type Metadata interface { Name() string; Version() string }
type Engine interface { Metadata }
type Registry struct{}
func (*Registry) Register(Engine) {}
func (*Registry) Get(string) (Engine, bool) { return nil, false }
`,
		},
		{
			name: "unrelated owner methods",
			src: `package fixture
type Engine interface { Resolve(string) (string, error) }
type Registry struct{}
func (*Registry) Register(string) {}
func (*Registry) Get(string) (string, bool) { return "", false }
`,
		},
		{
			name: "split owners",
			src: `package fixture
type Engine interface { Resolve(string) (string, error) }
type Registry struct{}
func (*Registry) Register(Engine) {}
type Manager struct{}
func (*Manager) Get(string) (Engine, bool) { return nil, false }
`,
		},
		{
			name: "pointer to engine interface",
			src: `package fixture
type Engine interface { Resolve(string) (string, error) }
type Registry struct{}
func (*Registry) Register(*Engine) {}
func (*Registry) Get(string) (*Engine, bool) { return nil, false }
`,
		},
		{
			name: "collection of engine interfaces",
			src: `package fixture
type Engine interface { Resolve(string) (string, error) }
type Registry struct{}
func (*Registry) Register([]Engine) {}
func (*Registry) Get(string) ([]Engine, bool) { return nil, false }
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), tt.name+".go", tt.src, 0)
			if err != nil {
				t.Fatal(err)
			}
			families := engineFamiliesInPackage(".", ".", []*ast.File{file})
			if len(families) != 1 {
				t.Fatalf("found %d engine families, want 1", len(families))
			}
			family := families[0]
			got := family.hasDomainMethod && family.hasOwner &&
				family.ownerRegisters && family.ownerManages && family.singleOwner
			if got != tt.valid {
				t.Fatalf("family validity = %t, want %t: %+v", got, tt.valid, family)
			}
		})
	}
}

type engineFamily struct {
	packagePath     string
	engineName      string
	hasDomainMethod bool
	hasOwner        bool
	ownerRegisters  bool
	ownerManages    bool
	singleOwner     bool
}

func inspectEngineFamilies(root string) ([]engineFamily, error) {
	dirs := make(map[string]struct{})
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			dirs[path] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	var families []engineFamily
	for dir := range dirs {
		packages, err := parsePackageFiles(dir)
		if err != nil {
			return nil, err
		}
		for _, files := range packages {
			families = append(families, engineFamiliesInPackage(root, dir, files)...)
		}
	}
	slices.SortFunc(families, func(a, b engineFamily) int {
		return strings.Compare(a.packagePath+"."+a.engineName, b.packagePath+"."+b.engineName)
	})
	return families, nil
}

func parsePackageFiles(dir string) (map[string][]*ast.File, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	packages := make(map[string][]*ast.File)
	fileSet := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fileSet, filepath.Join(dir, entry.Name()), nil, 0)
		if err != nil {
			return nil, err
		}
		packages[file.Name.Name] = append(packages[file.Name.Name], file)
	}
	return packages, nil
}

func engineFamiliesInPackage(root, dir string, files []*ast.File) []engineFamily {
	engines := make(map[string]bool)
	interfaces := make(map[string]*ast.InterfaceType)
	owners := make(map[string]bool)
	ownerMethods := make(map[string][]*ast.FuncDecl)

	for _, file := range files {
		for _, decl := range file.Decls {
			switch decl := decl.(type) {
			case *ast.GenDecl:
				for _, spec := range decl.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					if iface, ok := typeSpec.Type.(*ast.InterfaceType); ok {
						interfaces[typeSpec.Name.Name] = iface
					}
					if typeSpec.Name.Name == "Registry" || typeSpec.Name.Name == "Manager" {
						owners[typeSpec.Name.Name] = true
					}
				}
			case *ast.FuncDecl:
				receiver := receiverName(decl)
				if receiver != "Registry" && receiver != "Manager" {
					continue
				}
				ownerMethods[receiver] = append(ownerMethods[receiver], decl)
			}
		}
	}
	for name, iface := range interfaces {
		if strings.HasSuffix(name, "Engine") {
			engines[name] = hasDomainMethod(iface, interfaces, nil)
		}
	}

	packagePath, err := filepath.Rel(root, dir)
	if err != nil {
		packagePath = dir
	}
	out := make([]engineFamily, 0, len(engines))
	for name, domainMethod := range engines {
		registers := false
		manages := false
		singleOwner := false
		for owner := range owners {
			ownerRegisters := ownerRegistersEngine(ownerMethods[owner], name)
			ownerManages := ownerReturnsEngine(ownerMethods[owner], name)
			registers = registers || ownerRegisters
			manages = manages || ownerManages
			singleOwner = singleOwner || ownerRegisters && ownerManages
		}
		out = append(out, engineFamily{
			packagePath:     filepath.ToSlash(packagePath),
			engineName:      name,
			hasDomainMethod: domainMethod,
			hasOwner:        len(owners) > 0,
			ownerRegisters:  registers,
			ownerManages:    manages,
			singleOwner:     singleOwner,
		})
	}
	return out
}

func hasDomainMethod(
	iface *ast.InterfaceType,
	interfaces map[string]*ast.InterfaceType,
	visiting map[string]bool,
) bool {
	genericMethods := []string{
		"Health",
		"Manifest",
		"Name",
		"Start",
		"Stop",
		"Version",
	}
	for _, field := range iface.Methods.List {
		for _, name := range field.Names {
			if _, generic := slices.BinarySearch(genericMethods, name.Name); !generic {
				return true
			}
		}
		if len(field.Names) != 0 {
			continue
		}
		embedded, ok := field.Type.(*ast.Ident)
		if !ok || visiting[embedded.Name] {
			continue
		}
		embeddedInterface, ok := interfaces[embedded.Name]
		if !ok {
			continue
		}
		next := maps.Clone(visiting)
		if next == nil {
			next = make(map[string]bool)
		}
		next[embedded.Name] = true
		if hasDomainMethod(embeddedInterface, interfaces, next) {
			return true
		}
	}
	return false
}

func receiverName(decl *ast.FuncDecl) string {
	if decl.Recv == nil || len(decl.Recv.List) == 0 {
		return ""
	}
	switch receiver := decl.Recv.List[0].Type.(type) {
	case *ast.Ident:
		return receiver.Name
	case *ast.StarExpr:
		if ident, ok := receiver.X.(*ast.Ident); ok {
			return ident.Name
		}
	}
	return ""
}

func ownerRegistersEngine(methods []*ast.FuncDecl, engineName string) bool {
	for _, method := range methods {
		if method.Name.Name != "Register" || method.Type.Params == nil {
			continue
		}
		for _, field := range method.Type.Params.List {
			if expressionNamesType(field.Type, engineName) {
				return true
			}
		}
	}
	return false
}

func ownerReturnsEngine(methods []*ast.FuncDecl, engineName string) bool {
	for _, method := range methods {
		if method.Type.Results == nil {
			continue
		}
		for _, field := range method.Type.Results.List {
			if expressionNamesType(field.Type, engineName) {
				return true
			}
		}
	}
	return false
}

func expressionNamesType(expr ast.Expr, typeName string) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == typeName
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
