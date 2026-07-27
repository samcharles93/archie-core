package secret

import (
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
