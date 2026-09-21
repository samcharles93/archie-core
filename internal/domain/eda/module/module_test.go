package module

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/eda/expr"
	"github.com/samcharles93/archie-core/internal/domain/eda/module/log"
)

func writeModule(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "log.go")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestRegisterAndInvokeLogKind(t *testing.T) {
	dir := t.TempDir()
	writeModule(t, dir, `package main

import "github.com/samcharles93/archie-core/internal/domain/eda/module/log"

func Run(a log.Args) log.Result {
	return log.Result{Written: a.Message != "", Level: a.Level}
}
`)

	r := New()
	if err := r.Register("log", dir); err != nil {
		t.Fatalf("Register: %v", err)
	}
	res, err := r.Invoke(context.Background(), "log", map[string]any{"message": "hello", "level": "info"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if res["written"] != true {
		t.Errorf("result written = %v, want true", res["written"])
	}
	if res["level"] != "info" {
		t.Errorf("result level = %q, want info", res["level"])
	}
}

func TestInvokeUnknownKindIsError(t *testing.T) {
	r := New()
	if _, err := r.Invoke(context.Background(), "notify", nil); err == nil {
		t.Fatal("Invoke(unknown kind) = nil, want error")
	}
}

func TestRegisterUnknownKindIsError(t *testing.T) {
	r := New()
	if err := r.Register("notify", t.TempDir()); err == nil {
		t.Fatal("Register(unknown kind) = nil, want error")
	}
}

func TestKindSchema(t *testing.T) {
	r := New()
	argsType, resultType, ok := r.KindSchema("log")
	if !ok {
		t.Fatal("KindSchema(log) = not ok, want the built-in log contract")
	}
	if want := reflect.TypeFor[log.Args](); argsType != want {
		t.Errorf("KindSchema(log) args type = %v, want %v", argsType, want)
	}
	if want := reflect.TypeFor[log.Result](); resultType != want {
		t.Errorf("KindSchema(log) result type = %v, want %v", resultType, want)
	}
	if _, _, ok := r.KindSchema("notify"); ok {
		t.Fatal("KindSchema(notify) = ok, want false for an unregistered kind")
	}
}

func TestRegisterMissingFileIsError(t *testing.T) {
	r := New()
	if err := r.Register("log", t.TempDir()); err == nil {
		t.Fatal("Register(missing file) = nil, want error")
	}
}

func TestInvokeWrongShapeIsReportedError(t *testing.T) {
	dir := t.TempDir()
	writeModule(t, dir, `package main

import "github.com/samcharles93/archie-core/internal/domain/eda/module/log"

func Run(a log.Args) log.Result {
	return log.Result{Written: true}
}
`)

	r := New()
	if err := r.Register("log", dir); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// Unknown arg key: shape mismatch, reported error, not silent ignore.
	if _, err := r.Invoke(context.Background(), "log", map[string]any{"message": "hi", "bogus": 1}); err == nil {
		t.Fatal("Invoke(unknown arg) = nil, want error")
	}
	// Wrong type for a known arg: reported error.
	if _, err := r.Invoke(context.Background(), "log", map[string]any{"message": 42}); err == nil {
		t.Fatal("Invoke(wrong type) = nil, want error")
	}
}

func TestInvokeMalformedFileIsReported(t *testing.T) {
	dir := t.TempDir()
	writeModule(t, dir, `package main

func Run( {{{`)

	r := New()
	if err := r.Register("log", dir); err == nil {
		t.Fatal("Register(malformed) = nil, want error")
	}
}

// TestDecodeResultEndToEnd ties Invoke's real flat result map through the
// schema-aware conversion into the typed expr environment: the value the
// checker validates and the value the run reads have one definition.
func TestDecodeResultEndToEnd(t *testing.T) {
	dir := t.TempDir()
	writeModule(t, dir, `package main

import "github.com/samcharles93/archie-core/internal/domain/eda/module/log"

func Run(a log.Args) log.Result {
	return log.Result{Written: a.Message != "", Level: a.Level}
}
`)

	r := New()
	if err := r.Register("log", dir); err != nil {
		t.Fatalf("Register: %v", err)
	}
	flat, err := r.Invoke(context.Background(), "log", map[string]any{"message": "hello", "level": "info"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	decoded, err := r.DecodeResult("log", flat)
	if err != nil {
		t.Fatalf("DecodeResult: %v", err)
	}

	env := expr.NewEnv(expr.DeclaredResult{ID: "build", Type: reflect.TypeFor[log.Result]()})
	prg, err := env.Compile(`actions.build.result.written`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	got, err := env.Eval(prg, expr.Context{
		Actions: map[string]map[string]any{
			"build": {"result": decoded},
		},
	})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if b, ok := got.(bool); !ok || !b {
		t.Fatalf("Eval(actions.build.result.written) = %#v, want true", got)
	}
}

func TestDecodeResultUnknownFieldIsError(t *testing.T) {
	r := New()
	_, err := r.DecodeResult("log", map[string]any{"written": true, "bogus": "x"})
	if err == nil {
		t.Fatal("DecodeResult(unknown field) = nil, want error")
	}
	for _, want := range []string{"bogus", "unknown result field"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("DecodeResult error = %q, want it to contain %q", err.Error(), want)
		}
	}
}

func TestInvokePanicRecovered(t *testing.T) {
	dir := t.TempDir()
	writeModule(t, dir, `package main

import "github.com/samcharles93/archie-core/internal/domain/eda/module/log"

func Run(a log.Args) log.Result {
	var s []string
	return log.Result{Level: s[5]}
}
`)

	r := New()
	if err := r.Register("log", dir); err != nil {
		t.Fatalf("Register: %v", err)
	}
	_, err := r.Invoke(context.Background(), "log", map[string]any{"message": "boom"})
	if err == nil {
		t.Fatal("Invoke(panicking module) = nil, want recovered error")
	}
	if !strings.Contains(err.Error(), "panicked") {
		t.Errorf("Invoke panic error = %q, want recovered-panic text", err.Error())
	}
}
