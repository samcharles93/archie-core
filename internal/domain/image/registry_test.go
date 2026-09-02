package image

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeProvider struct {
	name  string
	class ProviderClass
	cap   Capability
}

func (f fakeProvider) Name() string           { return f.name }
func (f fakeProvider) Class() ProviderClass   { return f.class }
func (f fakeProvider) Capability() Capability { return f.cap }

func (f fakeProvider) Generate(context.Context, GenerateRequest) (Result, error) {
	return Result{Provider: f.name, CreatedAt: time.Unix(0, 0)}, nil
}

func (f fakeProvider) Edit(context.Context, EditRequest) (Result, error) {
	if !f.cap.Edit {
		return Result{}, ErrUnsupported
	}
	return Result{Provider: f.name, CreatedAt: time.Unix(0, 0)}, nil
}

func TestRegistryRegisterAndGet(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	p := fakeProvider{name: "hosted-a", class: ClassHosted, cap: Capability{Generate: true}}
	if err := r.Register(p); err != nil {
		t.Fatalf("Register() = %v", err)
	}
	got, ok := r.Get("hosted-a")
	if !ok || got.Name() != "hosted-a" {
		t.Fatalf("Get() = %v, %v, want hosted-a", got, ok)
	}
	if _, ok := r.Get("missing"); ok {
		t.Fatal("Get(missing) = true, want false")
	}
}

func TestRegistryRegisterRejectsDuplicate(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	p := fakeProvider{name: "hosted-a", class: ClassHosted, cap: Capability{Generate: true}}
	if err := r.Register(p); err != nil {
		t.Fatalf("Register() = %v", err)
	}
	err := r.Register(p)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("Register(dup) = %v, want ErrDuplicate", err)
	}
}

func TestRegistryRegisterRejectsNilProvider(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	var p Provider
	if err := r.Register(p); err == nil {
		t.Fatal("Register(nil) = nil, want error")
	}
}

func TestRegistryRegisterRejectsInvalidCapability(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	p := fakeProvider{name: "bad", class: ClassHosted, cap: Capability{Mask: true}}
	if err := r.Register(p); err == nil {
		t.Fatal("Register(invalid capability) = nil, want error")
	}
}

func TestRegistryNamesSorted(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	for _, name := range []string{"zeta", "alpha", "mid"} {
		if err := r.Register(fakeProvider{name: name, class: ClassHosted, cap: Capability{Generate: true}}); err != nil {
			t.Fatalf("Register(%s) = %v", name, err)
		}
	}
	got := r.Names()
	want := []string{"alpha", "mid", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Names() = %v, want %v", got, want)
		}
	}
}

func TestRegistryByClassFilters(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if err := r.Register(fakeProvider{name: "hosted-a", class: ClassHosted, cap: Capability{Generate: true}}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(fakeProvider{name: "local-a", class: ClassLocal, cap: Capability{Generate: true}}); err != nil {
		t.Fatal(err)
	}
	hosted := r.ByClass(ClassHosted)
	if len(hosted) != 1 || hosted[0] != "hosted-a" {
		t.Fatalf("ByClass(hosted) = %v, want [hosted-a]", hosted)
	}
	local := r.ByClass(ClassLocal)
	if len(local) != 1 || local[0] != "local-a" {
		t.Fatalf("ByClass(local) = %v, want [local-a]", local)
	}
}

func TestFakeProviderEditRespectsCapability(t *testing.T) {
	t.Parallel()
	p := fakeProvider{name: "generate-only", class: ClassHosted, cap: Capability{Generate: true}}
	_, err := p.Edit(context.Background(), EditRequest{Prompt: "x", Inputs: []ImageData{{Bytes: []byte("x")}}})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Edit() = %v, want ErrUnsupported", err)
	}
}
