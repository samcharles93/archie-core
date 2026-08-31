package secretextract

import (
	"errors"
	"testing"
)

func TestEngineWrapperDelegatesToFields(t *testing.T) {
	w := _github_com_samcharles93_archie_core_internal_secret_Engine{
		WName:    func() string { return "vault" },
		WVersion: func() string { return "1.0" },
		WResolve: func(key string) (string, error) { return "resolved:" + key, nil },
	}

	if got, want := w.Name(), "vault"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
	if got, want := w.Version(), "1.0"; got != want {
		t.Errorf("Version() = %q, want %q", got, want)
	}
	got, err := w.Resolve("key")
	if err != nil {
		t.Fatalf("Resolve() error = %v, want nil", err)
	}
	if want := "resolved:key"; got != want {
		t.Errorf("Resolve() = %q, want %q", got, want)
	}
}

func TestEngineWrapperResolvePropagatesError(t *testing.T) {
	wantErr := errors.New("boom")
	w := _github_com_samcharles93_archie_core_internal_secret_Engine{
		WResolve: func(key string) (string, error) { return "", wantErr },
	}
	_, err := w.Resolve("key")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Resolve() error = %v, want %v", err, wantErr)
	}
}

func TestEngineWrapperNilGuards(t *testing.T) {
	// The zero-value wrapper (all function fields nil) must not panic; this
	// is the nil-guard behaviour TestGeneratedWrapperHasNilGuards protects
	// against regenerate wiping out (see package doc comment).
	var w _github_com_samcharles93_archie_core_internal_secret_Engine

	if got, want := w.Name(), ""; got != want {
		t.Errorf("Name() with nil WName = %q, want %q", got, want)
	}
	if got, want := w.Version(), ""; got != want {
		t.Errorf("Version() with nil WVersion = %q, want %q", got, want)
	}
	got, err := w.Resolve("key")
	if got != "" || err != nil {
		t.Errorf("Resolve() with nil WResolve = (%q, %v), want (\"\", nil)", got, err)
	}
}
