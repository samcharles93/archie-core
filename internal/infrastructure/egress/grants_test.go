package egress

import "testing"

// TestGrantResolverOnlyResolvesWhatWasGranted is the mutation-checked
// isolation proof: a service never granted for a run, a service granted only
// to a different run, and a service already revoked must all answer
// ErrUnbound -- there is no path from "asked" to "returned" that does not go
// through an explicit Grant.
func TestGrantResolverOnlyResolvesWhatWasGranted(t *testing.T) {
	g := NewGrantResolver()
	g.Grant("run-a", map[string]string{"openai": "sk-a"})
	g.Grant("run-b", map[string]string{"anthropic": "sk-b"})

	tests := []struct {
		name    string
		run     string
		service string
		want    string
		wantErr bool
	}{
		{name: "a granted service resolves", run: "run-a", service: "openai", want: "sk-a"},
		{name: "a service granted to a different run never resolves here", run: "run-a", service: "anthropic", wantErr: true},
		{name: "a service never granted to any run", run: "run-a", service: "unknown", wantErr: true},
		{name: "an unknown run resolves nothing", run: "run-c", service: "openai", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := g.Resolve(t.Context(), tt.run, tt.service)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Resolve(%q, %q) = (%q, nil), want ErrUnbound", tt.run, tt.service, got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("Resolve(%q, %q) = (%q, %v), want (%q, nil)", tt.run, tt.service, got, err, tt.want)
			}
		})
	}
}

func TestGrantResolverRevokeRemovesEveryGrantForTheRun(t *testing.T) {
	g := NewGrantResolver()
	g.Grant("run-a", map[string]string{"openai": "sk-a", "anthropic": "sk-b"})
	g.RevokeGrant("run-a")

	if _, err := g.Resolve(t.Context(), "run-a", "openai"); err == nil {
		t.Fatal("Resolve after RevokeGrant returned a value, want ErrUnbound")
	}
	if _, err := g.Resolve(t.Context(), "run-a", "anthropic"); err == nil {
		t.Fatal("Resolve after RevokeGrant returned a value, want ErrUnbound")
	}
}

// TestGrantResolverGrantReplacesNotMerges: a run re-granted with a narrower
// set (a Kit that stopped declaring a service it once did, or a retry
// dispatched after a grant changed) must lose the dropped service, not keep
// it from the previous Grant call.
func TestGrantResolverGrantReplacesNotMerges(t *testing.T) {
	g := NewGrantResolver()
	g.Grant("run-a", map[string]string{"openai": "sk-a", "anthropic": "sk-b"})
	g.Grant("run-a", map[string]string{"openai": "sk-a2"})

	got, err := g.Resolve(t.Context(), "run-a", "openai")
	if err != nil || got != "sk-a2" {
		t.Fatalf("Resolve(openai) = (%q, %v), want (sk-a2, nil)", got, err)
	}
	if _, err := g.Resolve(t.Context(), "run-a", "anthropic"); err == nil {
		t.Fatal("anthropic still resolves after a narrower Grant replaced it, want ErrUnbound")
	}
}
