// Package module is the Module position of the EDA playbook engine: operator-
// installed, in-process, daemon-privileged action implementations, interpreted
// via Yaegi (same trust tier as PluginDir and SecretEngineDir -- NOT
// repository-supplied task code).
//
// It generalizes the fixed-signature, resolved-by-name Yaegi pattern already
// shipped for custom gates (internal/gate/gateeval) and workflow stage plugins
// (internal/domain/workflow/skillbuild): read a .go file -> yaegiutil.New with
// that kind's symbol table -> yaegiutil.Resolve[func(Args) Result] ->
// yaegiutil.Safe-wrapped call.
//
// Per docs/prds/module-position.md, there is deliberately no generic Module
// interface with an any payload. Each action kind is its own tiny package
// with its own generated contract (internal/domain/eda/module/<kind>), and
// the registry's internal storage is the only place type erasure appears --
// exactly as it already does inside wfextract.
package module

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/traefik/yaegi/interp"

	"github.com/samcharles93/archie-core/internal/domain/eda/module/log"
	"github.com/samcharles93/archie-core/internal/domain/eda/module/log/logextract"
	"github.com/samcharles93/archie-core/internal/yaegiutil"
)

// Kind is a resolved module implementation: how to load a Yaegi file against
// this kind's generated contract, and how to invoke it.
type Kind struct {
	// loadSymbols returns the kind's generated Yaegi symbol table.
	loadSymbols func() map[string]map[string]reflect.Value
	// exportPath is the fixed exported function name the interpreted file
	// must define (e.g. "main.Run").
	exportPath string
}

// registry maps kind name to its one typed contract. Adding a new kind is a
// new entry here plus its own schema package -- no generic interface grows.
var registry = map[string]Kind{
	"log": {
		loadSymbols: func() map[string]map[string]reflect.Value { return logextract.Symbols },
		exportPath:  "main.Run",
	},
}

// ModuleRegistry maps kind names to their resolved, type-erased invokers.
// It is not a service locator: it owns one capability family (Module action
// kinds) and exposes Register/Invoke only.
type ModuleRegistry struct {
	// kinds holds the resolved invoker per kind. Erasure is internal-only:
	// the kind's interpreted contract remains fully typed.
	kinds map[string]invoker
}

// invoker is the type-erased callable the registry stores: it decodes raw
// args, invokes the interpreted function, and marshals the typed result.
type invoker func(ctx context.Context, rawArgs map[string]any) (map[string]any, error)

// New returns an empty registry.
func New() *ModuleRegistry {
	return &ModuleRegistry{kinds: make(map[string]invoker)}
}

// Kinds returns the known action-kind names, sorted for determinism. Only
// kinds with a registered contract can be loaded; the log kind is the
// shipped proof-of-concept (t2db.13).
func Kinds() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Len returns how many kinds were successfully registered (loaded).
func (r *ModuleRegistry) Len() int {
	return len(r.kinds)
}

// Register discovers <dir>/<kind>.go, interprets it against the kind's
// generated symbols, resolves its fixed export, and stores the invoker.
// A missing file, an unreadable file, a resolve failure, or an unknown kind
// is a reported error -- the daemon aborts startup on a broken module
// directory per the established routing-file pattern.
func (r *ModuleRegistry) Register(kind, dir string) error {
	k, ok := registry[kind]
	if !ok {
		return fmt.Errorf("module: unknown kind %q", kind)
	}
	path := filepath.Join(dir, kind+".go")
	src, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("module: read %s: %w", path, err)
	}

	inv, err := resolveInvoker(kind, path, string(src), k)
	if err != nil {
		return err
	}
	r.kinds[kind] = inv
	return nil
}

// resolveInvoker builds the type-erased invoker for one kind from its
// interpreted source. Each kind has its own typed decode/invoke path below;
// this switch is the only place the per-kind types are named, mirroring
// wfextract's closed symbol table.
func resolveInvoker(kind, label, src string, k Kind) (invoker, error) {
	switch kind {
	case "log":
		return logInvoker(label, src, k)
	default:
		return nil, fmt.Errorf("module: no invoker for kind %q", kind)
	}
}

// logInvoker resolves a log kind's main.Run func(log.Args) log.Result,
// decoding rawArgs strictly into log.Args first (a shape mismatch is a
// reported failure, never a silent zero-value fill).
func logInvoker(label, src string, k Kind) (invoker, error) {
	inv, err := yaegiutil.Safe(label, func() (invoker, error) {
		i, err := yaegiutil.New(interp.Options{}, k.loadSymbols())
		if err != nil {
			return nil, err
		}
		run, err := yaegiutil.Resolve[func(log.Args) log.Result](i, src, k.exportPath)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
		return func(ctx context.Context, rawArgs map[string]any) (map[string]any, error) {
			return runLog(ctx, label, run, rawArgs)
		}, nil
	})
	return inv, err
}

// runLog decodes rawArgs into log.Args, invokes the interpreted Run, and
// marshals the typed Result back to map[string]any. ctx is accepted for
// interface symmetry with future kinds that need cancellation; the log kind
// is side-effect-free and does not use it.
func runLog(_ context.Context, label string, run func(log.Args) log.Result, rawArgs map[string]any) (map[string]any, error) {
	args, err := decodeLogArgs(rawArgs)
	if err != nil {
		return nil, err
	}
	res, err := yaegiutil.Safe(label, func() (log.Result, error) {
		return run(args), nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"written": res.Written,
		"level":   res.Level,
	}, nil
}

// decodeLogArgs strictly decodes rawArgs into log.Args: a wrong-typed field
// or an unknown key is a reported error, never a silent zero-value fill --
// the "schema defines the accepted message" rule.
func decodeLogArgs(rawArgs map[string]any) (log.Args, error) {
	var args log.Args
	if rawArgs == nil {
		return args, nil
	}
	if msg, ok := rawArgs["message"]; ok {
		s, ok := msg.(string)
		if !ok {
			return args, fmt.Errorf("module log: args.message is %T, want string", msg)
		}
		args.Message = s
	}
	if lvl, ok := rawArgs["level"]; ok {
		s, ok := lvl.(string)
		if !ok {
			return args, fmt.Errorf("module log: args.level is %T, want string", lvl)
		}
		args.Level = s
	}
	for key := range rawArgs {
		if key != "message" && key != "level" {
			return args, fmt.Errorf("module log: unknown arg %q", key)
		}
	}
	return args, nil
}

// Invoke calls the registered kind with rawArgs and returns the marshaled
// result. An unregistered kind is a reported error.
func (r *ModuleRegistry) Invoke(ctx context.Context, kind string, rawArgs map[string]any) (map[string]any, error) {
	inv, ok := r.kinds[kind]
	if !ok {
		return nil, fmt.Errorf("module: kind %q is not registered", kind)
	}
	res, err := inv(ctx, rawArgs)
	if err != nil {
		return nil, fmt.Errorf("module %s: %w", kind, err)
	}
	return res, nil
}
