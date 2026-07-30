package secret

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvEngineResolve(t *testing.T) {
	t.Setenv("TEST_SECRET_KEY", "test-value")

	e := &envEngine{}
	if e.Name() != "env" {
		t.Errorf("Name = %q", e.Name())
	}
	if e.Version() != "1.0.0" {
		t.Errorf("Version = %q", e.Version())
	}
	v, err := e.Resolve("TEST_SECRET_KEY")
	if err != nil {
		t.Fatal(err)
	}
	if v != "test-value" {
		t.Errorf("got %q, want test-value", v)
	}
}

func TestEnvEngineResolveMissing(t *testing.T) {
	e := &envEngine{}
	_, err := e.Resolve("NONEXISTENT_VAR_12345")
	if err == nil {
		t.Error("expected error for missing env var")
	}
}

func TestSecretRefZeroValue(t *testing.T) {
	r := NewRegistry()
	var s SecretRef
	v, err := s.Resolve(r)
	if err != nil {
		t.Error(err)
	}
	if v != "" {
		t.Errorf("zero-value SecretRef should return empty string, got %q", v)
	}
}

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	e, ok := r.Get("env")
	if !ok {
		t.Fatal("env engine should be registered by default")
	}
	if e.Name() != "env" {
		t.Errorf("Name = %q", e.Name())
	}
}

func TestRegistryGetMissing(t *testing.T) {
	r := NewRegistry()
	_, ok := r.Get("nonexistent")
	if ok {
		t.Error("should not find nonexistent engine")
	}
}

func TestRegistryResolve(t *testing.T) {
	t.Setenv("TEST_SECRET", "resolved")

	r := NewRegistry()
	v, err := r.Resolve(SecretRef{Engine: "env", Key: "TEST_SECRET"})
	if err != nil {
		t.Fatal(err)
	}
	if v != "resolved" {
		t.Errorf("got %q, want resolved", v)
	}
}

func TestRegistryResolveUnknownEngine(t *testing.T) {
	r := NewRegistry()
	_, err := r.Resolve(SecretRef{Engine: "unknown", Key: "x"})
	if err == nil {
		t.Error("expected error for unknown engine")
	}
}

func TestRegistryGetenvFallsBackToNonEnvEngine(t *testing.T) {
	t.Setenv("ARCHIE_TEST_BWS_PROVIDER_KEY", "")
	r := NewRegistry()
	r.Register(testEngine{name: "test-source", values: map[string]string{
		"ARCHIE_TEST_BWS_PROVIDER_KEY": "resolved-value",
	}})

	if got := r.Getenv("ARCHIE_TEST_BWS_PROVIDER_KEY"); got != "resolved-value" {
		t.Fatalf("Getenv() = %q, want resolved value", got)
	}
	if got := os.Getenv("ARCHIE_TEST_BWS_PROVIDER_KEY"); got != "resolved-value" {
		t.Fatalf("resolved value was not exported for SDK/container consumers")
	}
}

type testEngine struct {
	name   string
	values map[string]string
}

func TestBWSEngineLoadsNamedSecretsOnce(t *testing.T) {
	dir := t.TempDir()
	command := filepath.Join(dir, "bws")
	countPath := filepath.Join(dir, "count")
	script := fmt.Sprintf(`#!/bin/sh
echo x >> %q
printf '%%s' '[{"key":"OPENAI_API_KEY","value":"from-bws"}]'
`, countPath)
	if err := os.WriteFile(command, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	engine := newBWSEngine()
	got, err := engine.Resolve("OPENAI_API_KEY")
	if err != nil {
		t.Fatal(err)
	}
	if got != "from-bws" {
		t.Fatalf("Resolve() = %q", got)
	}
	if _, err := engine.Resolve("OPENAI_API_KEY"); err != nil {
		t.Fatal(err)
	}
	count, err := os.ReadFile(countPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(count), "x"); got != 1 {
		t.Fatalf("bws invocation count = %d, want 1", got)
	}
}

func (e testEngine) Name() string    { return e.name }
func (e testEngine) Version() string { return "test" }
func (e testEngine) Resolve(key string) (string, error) {
	value, ok := e.values[key]
	if !ok {
		return "", fmt.Errorf("not found")
	}
	return value, nil
}

func TestLoadDirNonexistent(t *testing.T) {
	r := NewRegistry()
	n, err := r.LoadDir("/nonexistent/path/12345")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("expected 0 loaded, got %d", n)
	}
}

func TestNewRegistryHasEnv(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Get("env"); !ok {
		t.Error("NewRegistry must pre-register env engine")
	}
}
