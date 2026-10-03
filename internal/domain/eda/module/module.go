// Package module is the registry of EDA playbook action kinds. Each kind is
// built in and has one typed contract: Args and Result struct types the
// playbook loader declares to CEL, so a typo in an arg key or result field is a
// load failure rather than a dispatch failure.
package module

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"sort"
	"strings"

	"github.com/samcharles93/archie-core/internal/domain/eda/module/log"
)

// invoker decodes raw args, runs the kind, and marshals its typed result.
type invoker func(ctx context.Context, rawArgs map[string]any) (map[string]any, error)

// Kind is one built-in action kind: its schema and how to run it.
type Kind struct {
	argsType   reflect.Type
	resultType reflect.Type
	invoke     invoker
}

// registry maps kind name to its one typed contract. Adding a kind is a new
// entry here plus its own schema package.
var registry = map[string]Kind{
	"log": {
		argsType:   reflect.TypeFor[log.Args](),
		resultType: reflect.TypeFor[log.Result](),
		invoke:     runLog,
	},
}

// ModuleRegistry answers the playbook engine's questions about action kinds.
// It holds no state: every kind is built in.
type ModuleRegistry struct{}

// New returns the registry.
func New() *ModuleRegistry { return &ModuleRegistry{} }

// KindSchema returns a known kind's Args and Result types.
func (*ModuleRegistry) KindSchema(kind string) (reflect.Type, reflect.Type, bool) {
	k, ok := registry[kind]
	if !ok {
		return nil, nil, false
	}
	return k.argsType, k.resultType, true
}

// runLog writes the message to the daemon's log at the requested level
// (default info) and reports what it wrote.
func runLog(ctx context.Context, rawArgs map[string]any) (map[string]any, error) {
	args, err := decodeLogArgs(rawArgs)
	if err != nil {
		return nil, err
	}
	level, name, err := logLevel(args.Level)
	if err != nil {
		return nil, err
	}
	if args.Message != "" {
		slog.Default().Log(ctx, level, args.Message)
	}
	return map[string]any{"written": args.Message != "", "level": name}, nil
}

func logLevel(name string) (slog.Level, string, error) {
	switch strings.ToLower(name) {
	case "", "info":
		return slog.LevelInfo, "info", nil
	case "debug":
		return slog.LevelDebug, "debug", nil
	case "warn":
		return slog.LevelWarn, "warn", nil
	case "error":
		return slog.LevelError, "error", nil
	}
	return 0, "", fmt.Errorf("module log: unknown level %q", name)
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

// Invoke calls kind with rawArgs and returns its result map.
func (*ModuleRegistry) Invoke(ctx context.Context, kind string, rawArgs map[string]any) (map[string]any, error) {
	k, ok := registry[kind]
	if !ok {
		return nil, fmt.Errorf("module: unknown kind %q", kind)
	}
	res, err := k.invoke(ctx, rawArgs)
	if err != nil {
		return nil, fmt.Errorf("module %s: %w", kind, err)
	}
	return res, nil
}

// DecodeResult converts Invoke's result map into the kind's Result struct.
// Unknown, missing or mistyped fields are errors.
func (*ModuleRegistry) DecodeResult(kind string, raw map[string]any) (any, error) {
	k, ok := registry[kind]
	if !ok {
		return nil, fmt.Errorf("module: unknown kind %q", kind)
	}
	return decodeResultStruct(kind, k.resultType, raw)
}

// decodeResultStruct builds a Result value from raw. Every field must be
// present with the exact type.
func decodeResultStruct(kind string, t reflect.Type, raw map[string]any) (any, error) {
	if t == nil {
		return nil, fmt.Errorf("module %s: result schema is not set", kind)
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("module %s: result type %s is not a struct", kind, t)
	}

	out := reflect.New(t).Elem()
	type declaredField struct {
		name  string
		value reflect.Value
	}
	declared := make([]declaredField, 0, t.NumField())
	byName := make(map[string]reflect.Value, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		if !out.Field(i).CanSet() {
			continue
		}
		name := strings.ToLower(t.Field(i).Name)
		declared = append(declared, declaredField{name: name, value: out.Field(i)})
		byName[name] = out.Field(i)
	}

	keys := make([]string, 0, len(raw))
	for key := range raw {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, ok := byName[key]; !ok {
			return nil, fmt.Errorf("module %s: unknown result field %q", kind, key)
		}
	}
	for _, f := range declared {
		val, ok := raw[f.name]
		if !ok {
			return nil, fmt.Errorf("module %s: result field %q is missing, want %s", kind, f.name, f.value.Type())
		}
		if err := setResultField(kind, f.name, f.value, val); err != nil {
			return nil, err
		}
	}
	return out.Interface(), nil
}

// setResultField assigns val to fv only when val's type is the field's type. A
// conversion would change the value's representation -- a number silently
// becoming a string is the named case -- so it is refused, as are a nil and a
// wrong-typed value. Nothing here zero-fills.
func setResultField(kind, field string, fv reflect.Value, val any) error {
	if val == nil {
		return fmt.Errorf("module %s: result.%s is nil, want %s", kind, field, fv.Type())
	}
	rv := reflect.ValueOf(val)
	if !rv.Type().AssignableTo(fv.Type()) {
		return fmt.Errorf("module %s: result.%s is %T, want %s", kind, field, val, fv.Type())
	}
	fv.Set(rv)
	return nil
}
