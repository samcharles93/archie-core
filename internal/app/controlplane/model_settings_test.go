package controlplane

import (
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
)

// TestImportConfigRefusesAResolvedProviderSnapshot pins the last line of
// defence behind the boot seeding order: resolveProviderSecrets exports each
// key under a process-local ARCHIE_PROVIDER_<hash> env name and clears the
// source reference, so a provider-settings seed built from an already-resolved
// snapshot (archie-core-1000's failure shape) carries no reference a later
// boot could resolve. The seed runs through the same Decode a write applies,
// so the validation refusal makes ImportConfig skip the kind and store
// nothing -- the State Store still starts, the file document stays in force,
// and the skip is reported -- rather than persisting a name that dies with
// the process that minted it.
func TestImportConfigRefusesAResolvedProviderSnapshot(t *testing.T) {
	resolved := config.Config{Providers: map[string]config.Provider{
		"openai": {Class: "openai", APIKeyEnv: "ARCHIE_PROVIDER_6986D5A05E1DF674_API_KEY"},
	}}
	resources := pgstore.Open(t)
	defer resources.Close()

	versions, skipped, err := testServer(t, resources).ImportConfig(t.Context(), resolved)
	if err != nil {
		t.Fatalf("ImportConfig: %v", err)
	}
	if _, stored := versions[ProviderSettingsKind]; stored {
		t.Errorf("provider-settings imported from a resolved snapshot: version %d", versions[ProviderSettingsKind])
	}
	var reported bool
	for _, skip := range skipped {
		if skip.Kind == ProviderSettingsKind {
			reported = true
			if !strings.Contains(skip.Err.Error(), "boot-derived") {
				t.Errorf("skip error = %v, want the boot-derived-name refusal", skip.Err)
			}
		}
	}
	if !reported {
		t.Errorf("skipped = %+v, want provider-settings reported", skipped)
	}
	if _, err := resources.Resource(t.Context(), ProviderSettingsKind); err == nil {
		t.Fatal("provider-settings was stored from a resolved snapshot")
	} else if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("Resource = %v, want %v", err, storecontract.ErrResourceNotFound)
	}
}
