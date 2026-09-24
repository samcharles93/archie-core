package archied

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEmbeddedNATSEndpointRoundTripIsPrivateAndAtomic(t *testing.T) {
	stateDir := t.TempDir()
	if err := writeEmbeddedNATSEndpoint(stateDir, "nats://127.0.0.1:4222", "secret"); err != nil {
		t.Fatal(err)
	}
	got, err := readEmbeddedNATSEndpoint(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "nats://127.0.0.1:4222" || got.Token != "secret" {
		t.Fatalf("endpoint = %+v", got)
	}
	if want := filepath.Join(stateDir, "nats", "endpoint.json"); embeddedNATSEndpointPath(stateDir) != want {
		t.Fatalf("endpoint path = %q, want %q", embeddedNATSEndpointPath(stateDir), want)
	}
	info, err := os.Stat(embeddedNATSEndpointPath(stateDir))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("endpoint permissions = %o, want 600", info.Mode().Perm())
	}
}
