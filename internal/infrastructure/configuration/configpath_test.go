// This file fences the claim that every archie binary reads the same
// configuration file when the operator names none. The value is only
// observable by starting a process and reading which file it opened, but the
// failure being guarded against is a second derivation of that value, which is
// a shape in the source before it is a wrong path at runtime -- so the fence
// reads the source.
//
// Four binaries derived their default with os.UserConfigDir while the daemon
// resolved it from its own config home (archie-core-8i6l). The two agree on
// Linux with an absolute XDG_CONFIG_HOME and diverge everywhere else: darwin
// resolves os.UserConfigDir to ~/Library/Application Support, and a relative
// XDG_CONFIG_HOME is rejected outright rather than honoured. One deployment
// then boots from a different config file depending on which binary started it.

package configuration

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const moduleImportPrefix = "github.com/samcharles93/archie-core/"

// configPackageImportPath is this package as its importers name it, spelled
// out so a moved or renamed package fails this fence instead of matching
// nothing and passing.
const configPackageImportPath = moduleImportPrefix + "internal/infrastructure/configuration"

// helperName is the single helper that resolves the default configuration path.
const helperName = "DefaultConfigPath"

// configFlagDefault is one -config registration found by the fence: where it
// is, what it defaults to, and whether that default comes from the helper.
type configFlagDefault struct {
	at       token.Position
	expr     string
	resolved bool
}

// TestDefaultConfigPathFollowsTheConfigHome pins the precedence every archie
// binary shares: $XDG_CONFIG_HOME when it is set, ~/.config when it is not.
//
// The relative and unknowable-home cases are the ones a second derivation gets
// wrong, so they are the reason this table exists rather than a single
// assertion: os.UserConfigDir rejects a relative $XDG_CONFIG_HOME with an error
// rather than resolving it, and answers with ~/Library/Application Support on
// darwin, where this rule says ~/.config.
func TestDefaultConfigPathFollowsTheConfigHome(t *testing.T) {
	home := t.TempDir()
	tests := []struct {
		name    string
		xdg     string
		home    string // an empty home is an unknowable one: os.UserHomeDir fails
		wantDir string
	}{
		{
			name:    "an absolute XDG_CONFIG_HOME is used as given",
			xdg:     filepath.Join(home, "xdg"),
			home:    filepath.Join(home, "ignored"),
			wantDir: filepath.Join(home, "xdg", "archie"),
		},
		{
			name:    "a relative XDG_CONFIG_HOME is honoured rather than rejected",
			xdg:     filepath.Join("relative", "config"),
			home:    home,
			wantDir: filepath.Join("relative", "config", "archie"),
		},
		{
			// $XDG_CONFIG_HOME "either not set or empty" is one case to the code
			// under test, which reads it with os.Getenv: nothing downstream can
			// tell an absent variable from an empty one, so neither can this.
			name:    "an unset or empty XDG_CONFIG_HOME falls back to the home directory",
			home:    home,
			wantDir: filepath.Join(home, ".config", "archie"),
		},
		{
			name:    "an unknowable home leaves the path relative instead of empty",
			wantDir: filepath.Join(".config", "archie"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", tc.home)
			t.Setenv("XDG_CONFIG_HOME", tc.xdg)
			if got := DefaultConfigDir(); got != tc.wantDir {
				t.Errorf("DefaultConfigDir() = %q, want %q", got, tc.wantDir)
			}
			if got, want := DefaultConfigPath(), filepath.Join(tc.wantDir, "config.toml"); got != want {
				t.Errorf("DefaultConfigPath() = %q, want %q", got, want)
			}
		})
	}
}

func TestBinariesTakeTheDefaultConfigPathFromHere(t *testing.T) {
	fset := token.NewFileSet()
	var defaults []configFlagDefault

	root := moduleRoot(t)

	for _, pattern := range []string{
		filepath.Join(root, "cmd", "*", "*.go"),
		filepath.Join(root, "internal", "app", "archied", "*.go"),
	} {
		paths, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("glob %s: %v", pattern, err)
		}
		if len(paths) == 0 {
			t.Fatalf("glob %s matched no file, so this fence checked nothing", pattern)
		}
		for _, path := range paths {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			helper := importedName(file, configPackageImportPath)
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				if isSelectorCall(call, "os", "UserConfigDir") {
					t.Errorf("%s: resolves the config directory itself with os.UserConfigDir; it rejects a relative XDG_CONFIG_HOME and resolves to ~/Library/Application Support on darwin, so this process would boot from a different file than the daemon (%s.%s resolves it once)",
						fset.Position(call.Pos()), lastElement(configPackageImportPath), helperName)
				}
				defaultExpr, ok := configFlagDefaultExpr(call)
				if !ok {
					return true
				}
				defaults = append(defaults, configFlagDefault{
					at:       fset.Position(call.Pos()),
					expr:     renderExpr(fset, defaultExpr),
					resolved: callsHelper(defaultExpr, helper),
				})
				return true
			})
		}
	}

	if len(defaults) == 0 {
		t.Fatalf("no -config registration found under %s; this fence matched nothing and would pass vacuously", root)
	}
	for _, d := range defaults {
		if !d.resolved {
			t.Errorf("%s: -config defaults to %s instead of %s.%s(); every binary reads the same file only while they all take the path from that one helper (archie-core-8i6l)",
				d.at, d.expr, lastElement(configPackageImportPath), helperName)
		}
	}
}

// configFlagDefaultExpr returns the default expression of a call registering a
// flag named "config", and whether the call registers one at all. Both
// flag.StringVar(target, name, value, usage) and flag.String(name, value,
// usage) are recognised, whatever name the FlagSet is bound to -- the callers
// use flag.CommandLine, fs and flags interchangeably.
func configFlagDefaultExpr(call *ast.CallExpr) (ast.Expr, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil, false
	}
	switch sel.Sel.Name {
	case "StringVar":
		if len(call.Args) >= 3 && isStringLiteral(call.Args[1], "config") {
			return call.Args[2], true
		}
	case "String":
		if len(call.Args) >= 2 && isStringLiteral(call.Args[0], "config") {
			return call.Args[1], true
		}
	}
	return nil, false
}

// isSelectorCall reports whether call is qualifier.name(...).
func isSelectorCall(call *ast.CallExpr, qualifier, name string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == qualifier && sel.Sel.Name == name
}

// callsHelper reports whether expr is helper.name(), where helper is the local
// name this file's imports give the configuration package ("" when the file
// does not import it).
func callsHelper(expr ast.Expr, helper string) bool {
	if helper == "" {
		return false
	}
	call, ok := expr.(*ast.CallExpr)
	return ok && isSelectorCall(call, helper, helperName)
}

// isStringLiteral reports whether expr is the string literal want.
func isStringLiteral(expr ast.Expr, want string) bool {
	literal, ok := expr.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return false
	}
	value, err := strconv.Unquote(literal.Value)
	return err == nil && value == want
}

// importedName returns the name file's imports bind path to, or "" when the
// file does not import it.
func importedName(file *ast.File, path string) string {
	for _, imported := range file.Imports {
		if imported.Path == nil {
			continue
		}
		importedPath, err := strconv.Unquote(imported.Path.Value)
		if err != nil || importedPath != path {
			continue
		}
		if name := imported.Name; name != nil && name.Name != "" && name.Name != "_" && name.Name != "." {
			return name.Name
		}
		return lastElement(path)
	}
	return ""
}

// renderExpr renders expr as source text, for a failure message that names the
// derivation instead of only its position.
func renderExpr(fset *token.FileSet, expr ast.Expr) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, expr); err != nil {
		return "<unrenderable expression>"
	}
	return buf.String()
}

func lastElement(path string) string {
	return path[strings.LastIndex(path, "/")+1:]
}

// moduleRoot walks up from this test's source to the directory holding go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test's source file")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above this test's source file")
		}
		dir = parent
	}
}
