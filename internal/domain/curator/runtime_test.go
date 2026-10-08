package curator

import (
	"context"
	"testing"
	"time"
)

func TestReplaceLiveDefinitions(t *testing.T) {
	ctx := t.Context()
	registry := NewRegistry(Registrar{})
	runtime := NewRuntime(registry, RuntimeConfig{})
	if err := runtime.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := runtime.Stop(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	for _, tt := range []struct {
		name    string
		defs    []Definition
		present bool
		wantErr bool
	}{
		{"add without restart", []Definition{{Name: "custom", Enabled: true, Manifest: Manifest{Interval: time.Hour}}}, true, false},
		{"undeclared host refuses replacement", []Definition{{Name: "custom", Enabled: true, Manifest: Manifest{Interval: time.Hour, Tools: []string{"secret"}}}}, true, true},
		{"disable removes loop", []Definition{{Name: "custom", Enabled: false, Manifest: Manifest{Interval: time.Hour}}}, false, false},
		{"re-enable", []Definition{{Name: "custom", Enabled: true, Manifest: Manifest{Interval: 2 * time.Hour}}}, true, false},
		{"delete", nil, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := runtime.ReplaceDefinitions(ctx, tt.defs)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v", err)
			}
			if _, ok := registry.Get("custom"); ok != tt.present {
				t.Fatalf("registered = %v, want %v", ok, tt.present)
			}
		})
	}
}
