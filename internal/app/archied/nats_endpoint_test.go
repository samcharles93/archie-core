package archied

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEmbeddedNATSEndpointRoundTripIsPrivateAndAtomic(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "archie.db")
	if err := writeEmbeddedNATSEndpoint(dbPath, "nats://127.0.0.1:4222", "secret"); err != nil {
		t.Fatal(err)
	}
	got, err := readEmbeddedNATSEndpoint(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "nats://127.0.0.1:4222" || got.Token != "secret" {
		t.Fatalf("endpoint = %+v", got)
	}
	info, err := os.Stat(embeddedNATSEndpointPath(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("endpoint permissions = %o, want 600", info.Mode().Perm())
	}
}
