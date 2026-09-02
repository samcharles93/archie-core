package module

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
