package gateway

import (
	"testing"
)

func TestPersonaRegistryDefault(t *testing.T) {
	r := NewPersonaRegistry(DefaultPersonas())
	if len(r.List()) != 5 {
		t.Errorf("expected 5 default personas, got %d", len(r.List()))
	}
}

func TestPersonaRegistrySetAndGetActive(t *testing.T) {
	r := NewPersonaRegistry(DefaultPersonas())

	if !r.SetActive("session-1", "concise") {
		t.Error("SetActive should succeed for valid persona")
	}
	if r.SetActive("session-1", "nonexistent") {
		t.Error("SetActive should fail for unknown persona")
	}

	prompt := r.GetActive("session-1")
	if prompt == "" {
		t.Error("GetActive should return prompt for active persona")
	}

	if prompt := r.GetActive("session-2"); prompt != "" {
		t.Error("GetActive should return empty for unset session")
	}
}

func TestPersonaRegistryGet(t *testing.T) {
	r := NewPersonaRegistry(DefaultPersonas())
	p, ok := r.Get("helpful")
	if !ok || p.Name != "helpful" {
		t.Error("Get helpful should succeed")
	}
	_, ok = r.Get("nonexistent")
	if ok {
		t.Error("Get nonexistent should fail")
	}
}

func TestPersonaRegistryEmpty(t *testing.T) {
	r := NewPersonaRegistry(nil)
	if len(r.List()) != 0 {
		t.Error("empty registry should have no personas")
	}
}

func TestPersonaRegistryConcurrent(t *testing.T) {
	r := NewPersonaRegistry(DefaultPersonas())
	done := make(chan struct{})
	go func() {
		for range 100 {
			r.SetActive("s1", "concise")
			r.SetActive("s1", "helpful")
		}
		close(done)
	}()
	for range 50 {
		_ = r.GetActive("s1")
		_ = r.List()
	}
	<-done
}
