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
// src, in source order. A holder is any identifier declared with type
// config.Config (or *config.Config) in the same file, plus the .Config field
// of a configuration.Document, because those are the shapes the Messaging
// Service reads a decoded configuration through.
func configReadOffenders(src []byte) []string {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "config.go", src, parser.SkipObjectResolution)
	if err != nil {
		// A file that does not parse is reported as an offender rather than
		// ignored: silently skipping it would make this gate blind exactly
		// when the tree is broken.
		return []string{"parse error: " + err.Error()}
	}
	holders := configHolders(file)

	var offenders []string
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || !bannedConfigFields[sel.Sel.Name] {
			return true
		}
		switch base := sel.X.(type) {
		case *ast.Ident:
			if holders[base.Name] {
				offenders = append(offenders, base.Name+"."+sel.Sel.Name)
			}
		case *ast.SelectorExpr:
			if base.Sel.Name == "Config" {
				offenders = append(offenders, "Config."+sel.Sel.Name)
			}
		}
		return true
	})
	return offenders
}

// configHolders collects the identifiers in file declared with a
// config.Config type, in a function parameter or a var.
func configHolders(file *ast.File) map[string]bool {
	holders := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncDecl:
			if node.Type.Params == nil {
				return true
			}
			for _, field := range node.Type.Params.List {
				if !isConfigType(field.Type) {
					continue
				}
				for _, name := range field.Names {
					holders[name.Name] = true
				}
			}
		case *ast.ValueSpec:
			if !isConfigType(node.Type) {
				return true
			}
			for _, name := range node.Names {
				holders[name.Name] = true
			}
		}
		return true
	})
	return holders
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
