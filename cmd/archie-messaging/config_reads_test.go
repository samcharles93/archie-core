// The link gate in architecture_test.go measures which packages the Messaging
// binary links. It cannot see a process that links nothing new but still reads
// a decoded configuration value it is not allowed to own -- which is exactly
// how voice transcription was wired into the Messaging Service while a green
// link gate watched: internal/app/archiemessaging/config.go built an LLM
// transcriber from [models].transcription and [providers.*], both of which
// docs/prds/messaging-service-boundary.md reserves to the model-owning process.
//
// This file closes that shape of hole: it scans the Messaging Service's own
// production sources for a read of a top-level config section the PRD forbids,
// so re-adding the projection field fails here rather than shipping green.

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// messagingAppDir is the Messaging Service's application package, relative to
// this package's directory (go test runs with the package dir as cwd).
const messagingAppDir = "../../internal/app/archiemessaging"

// bannedConfigFields are the top-level config.Config fields the Messaging
// Service must never read, by Go field name. The PRD's "Configuration
// Ownership" section permits [chat.*], [services.*], [secrets] and the
// path/identity keys the process needs to locate itself; every entry here is a
// capability another process owns. internal/config's field names are the
// concrete keys, because that is what a Go read names.
var bannedConfigFields = map[string]bool{
	"Forge":      true, // [forge]
	"Repos":      true, // [repos]
	"Models":     true, // [models]
	"Providers":  true, // [providers]
	"Containers": true, // [containers]
	"NATS":       true, // [nats]
	// [runners] has no top-level field in internal/config today; if one is
	// added its own name must be listed here with it.
}

// configReadOffenders returns the banned "<holder>.<Field>" config reads in
// src, in source order. A holder is anything the file gives the
// internal/config.Config type: an explicitly typed parameter, var, receiver or
// struct field, a short variable declaration or inferred var whose right-hand
// side resolves to one ("cfg := doc.Config"), or a range variable over a slice
// of them. The type is tracked by name because a name is what a Go read
// selects on. A config value returned by a call this file does not declare is
// outside a syntactic rule and stays untracked.
func configReadOffenders(src []byte) []string {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "config.go", src, parser.SkipObjectResolution)
	if err != nil {
		// A file that does not parse is reported as an offender rather than
		// ignored: silently skipping it would make this gate blind exactly
		// when the tree is broken.
		return []string{"parse error: " + err.Error()}
	}
	kinds := configKinds(file)

	var offenders []string
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || !bannedConfigFields[sel.Sel.Name] {
			return true
		}
		switch base := sel.X.(type) {
		case *ast.Ident:
			if kinds[base.Name] == configValue {
				offenders = append(offenders, base.Name+"."+sel.Sel.Name)
			}
		case *ast.SelectorExpr:
			// doc.Config, or the config-typed struct field h.cfg. Config is
			// named by configuration.Document in another package, so it
			// cannot come out of the kinds map; treat a selected Config as
			// a config holder unconditionally.
			if base.Sel.Name == "Config" || kinds[base.Sel.Name] == configValue {
				offenders = append(offenders, base.Sel.Name+"."+sel.Sel.Name)
			}
		}
		return true
	})
	return offenders
}

// configKind is how an identifier or expression reaches a config.Config value.
type configKind int

const (
	notConfig   configKind = iota // not a config value
	configValue                   // config.Config
	configSlice                   // []config.Config, [N]config.Config or map[K]config.Config
)

// configKinds resolves the identifiers in file that hold a config.Config, in
// two passes because an inferred holder's source must be classified before the
// holder's own name can be.
func configKinds(file *ast.File) map[string]configKind {
	kinds := map[string]configKind{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncDecl:
			classifyFields(node.Recv, kinds)
			classifyFields(node.Type.Params, kinds)
		case *ast.ValueSpec:
			classifyNames(node.Type, node.Names, kinds)
		case *ast.StructType:
			for _, field := range node.Fields.List {
				classifyNames(field.Type, field.Names, kinds)
			}
		}
		return true
	})
	// Inferred holders: a single source-order pass suffices because Go
	// requires a name to be declared before it is referenced.
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			if node.Tok != token.DEFINE {
				return true
			}
			classifyValues(node.Lhs, node.Rhs, kinds)
		case *ast.ValueSpec:
			// An explicit type was handled above; here only "var cfg =
			// doc.Config", which infers the same way a := does.
			if node.Type != nil {
				return true
			}
			names := make([]ast.Expr, len(node.Names))
			for i, name := range node.Names {
				names[i] = name
			}
			classifyValues(names, node.Values, kinds)
		case *ast.RangeStmt:
			if exprKind(node.X, kinds) != configSlice {
				return true
			}
			if id, ok := node.Value.(*ast.Ident); ok {
				kinds[id.Name] = configValue
			}
		}
		return true
	})
	return kinds
}

// classifyValues records each lhs identifier that resolves to a config kind of
// the rhs expression in the same position. A shorter lhs or rhs is fine;
// position is what pairs a name with its value.
func classifyValues(lhs, rhs []ast.Expr, kinds map[string]configKind) {
	for i, target := range lhs {
		if i >= len(rhs) {
			return
		}
		id, ok := target.(*ast.Ident)
		if !ok {
			continue
		}
		if kind := exprKind(rhs[i], kinds); kind != notConfig {
			kinds[id.Name] = kind
		}
	}
}

// classifyFields records every named field in fields whose type holds a
// config.Config.
func classifyFields(fields *ast.FieldList, kinds map[string]configKind) {
	if fields == nil {
		return
	}
	for _, field := range fields.List {
		classifyNames(field.Type, field.Names, kinds)
	}
}

// classifyNames records names as holding the config kind of typ.
func classifyNames(typ ast.Expr, names []*ast.Ident, kinds map[string]configKind) {
	kind := typeKind(typ)
	if kind == notConfig {
		return
	}
	for _, name := range names {
		kinds[name.Name] = kind
	}
}

// exprKind resolves the config kind of an expression from the identifiers
// already classified in kinds.
func exprKind(expr ast.Expr, kinds map[string]configKind) configKind {
	switch e := expr.(type) {
	case *ast.Ident:
		return kinds[e.Name]
	case *ast.SelectorExpr:
		// doc.Config of a configuration.Document, or a struct field the file
		// declared as config.Config.
		if e.Sel.Name == "Config" {
			return configValue
		}
		return kinds[e.Sel.Name]
	case *ast.CompositeLit:
		return typeKind(e.Type)
	case *ast.StarExpr:
		return exprKind(e.X, kinds)
	case *ast.UnaryExpr:
		return exprKind(e.X, kinds)
	case *ast.ParenExpr:
		return exprKind(e.X, kinds)
	case *ast.IndexExpr:
		if exprKind(e.X, kinds) == configSlice {
			return configValue
		}
	}
	return notConfig
}

// typeKind reports the config kind a type expression names: config.Config, or
// a slice, array or map whose element is one.
func typeKind(expr ast.Expr) configKind {
	switch t := expr.(type) {
	case *ast.ArrayType:
		if isConfigType(t.Elt) {
			return configSlice
		}
	case *ast.MapType:
		// A map's range value is its element; classify the map itself as a
		// slice-like source so a range over it yields config values.
		if isConfigType(t.Value) {
			return configSlice
		}
	case *ast.StarExpr:
		return typeKind(t.X)
	case *ast.ParenExpr:
		return typeKind(t.X)
	}
	if isConfigType(expr) {
		return configValue
	}
	return notConfig
}

// isConfigType reports whether expr names internal/config's Config type,
// directly or behind a pointer.
func isConfigType(expr ast.Expr) bool {
	switch t := expr.(type) {
	case *ast.SelectorExpr:
		id, ok := t.X.(*ast.Ident)
		return ok && id.Name == "config" && t.Sel.Name == "Config"
	case *ast.StarExpr:
		return isConfigType(t.X)
	}
	return false
}

// TestMessagingReadsNoBannedConfigSection is the live gate: every production
// source file in the Messaging Service's application package is scanned, and a
// read of a PRD-forbidden section fails the build.
func TestMessagingReadsNoBannedConfigSection(t *testing.T) {
	entries, err := os.ReadDir(messagingAppDir)
	if err != nil {
		t.Fatalf("read %s: %v", messagingAppDir, err)
	}
	scanned := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(messagingAppDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		scanned++
		for _, offender := range configReadOffenders(src) {
			t.Errorf("%s reads %s; the Messaging Service must not read or decode that section (docs/prds/messaging-service-boundary.md, Configuration Ownership) -- the capability belongs to the model-owning process, reached over a contract", name, offender)
		}
	}
	// A gate that read no files would pass silently. Pin that it scanned the
	// package it names.
	if scanned == 0 {
		t.Fatalf("no production .go files found in %s: the gate asserted nothing", messagingAppDir)
	}
}

// TestConfigReadRuleCatchesTheOldWiring proves the rule fails on a synthetic
// copy of the shape that shipped: a projection built by reading
// [models].transcription and [providers.*]. Without this, the live gate's rule
// could rot into a no-op and still report a pass.
func TestConfigReadRuleCatchesTheOldWiring(t *testing.T) {
	const reverted = `package archiemessaging

import (
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/transcription"
)

func project(cfg config.Config) projection {
	return projection{
		models:    cfg.Models,
		providers: cfg.Providers,
	}
}

func setupTranscriber(cfg config.Config) {
	_, _ = transcription.New(cfg.Models, cfg.Providers, transcription.Options{})
}
`
	got := configReadOffenders([]byte(reverted))
	want := []string{"cfg.Models", "cfg.Providers", "cfg.Models", "cfg.Providers"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("configReadOffenders() = %v, want %v", got, want)
	}
}

// TestConfigReadRuleCatchesInferredHolders covers the holder shapes the first
// cut of this rule missed. It only saw a config.Config written out as a
// parameter or var type, so a value reached through an inferred short
// variable declaration ("cfg := doc.Config") or held in a struct field
// ("h.cfg.Models") left the forbidden [models]/[providers] read intact while
// the rule saw no holder to blame. Each case here fails red against that rule:
// the live gate would have reported a pass with the violation still shipping.
func TestConfigReadRuleCatchesInferredHolders(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "short var decl from a Document's Config field",
			src: `package archiemessaging

import "github.com/samcharles93/archie-core/internal/infrastructure/configuration"

func f(doc configuration.Document) {
	cfg := doc.Config
	_ = cfg.Models
}
`,
			want: []string{"cfg.Models"},
		},
		{
			name: "inferred var without an explicit type",
			src: `package archiemessaging

import "github.com/samcharles93/archie-core/internal/infrastructure/configuration"

func f(doc configuration.Document) {
	var cfg = doc.Config
	_ = cfg.Models
}
`,
			want: []string{"cfg.Models"},
		},
		{
			name: "short var decl inherits config type from another holder",
			src: `package archiemessaging

import "github.com/samcharles93/archie-core/internal/config"

func f(cfg config.Config) {
	other := cfg
	_ = other.Providers
}
`,
			want: []string{"other.Providers"},
		},
		{
			name: "config held in a struct field",
			src: `package archiemessaging

import "github.com/samcharles93/archie-core/internal/config"

type holder struct {
	cfg config.Config
}

func f(h holder) {
	_ = h.cfg.Providers
}
`,
			want: []string{"cfg.Providers"},
		},
		{
			name: "range variable over a slice of config",
			src: `package archiemessaging

import "github.com/samcharles93/archie-core/internal/config"

func f(cfgs []config.Config) {
	for _, cfg := range cfgs {
		_ = cfg.Models
	}
}
`,
			want: []string{"cfg.Models"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := configReadOffenders([]byte(tc.src))
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("configReadOffenders() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestConfigReadRuleAllowsThePermittedKeys proves the rule is not a blanket
// ban on config access: the sections the PRD does permit keep passing, and a
// nested chat field is not mistaken for a top-level one.
func TestConfigReadRuleAllowsThePermittedKeys(t *testing.T) {
	const permitted = `package archiemessaging

import "github.com/samcharles93/archie-core/internal/config"

func project(cfg config.Config) projection {
	return projection{
		telegram:  cfg.Chat.Telegram,
		email:     cfg.Chat.Email,
		gateway:   cfg.Services.Get("gateway").Target,
		workDir:   cfg.WorkDir,
		botUser:   cfg.BotUser,
		healthURL: cfg.Health.URL(),
	}
}
`
	if got := configReadOffenders([]byte(permitted)); len(got) != 0 {
		t.Fatalf("configReadOffenders(permitted) = %v, want none", got)
	}
}
