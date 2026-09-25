package egress

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
)

func verifyLeaf(t *testing.T, ca *CA, leaf *tls.Certificate, name string) error {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca.CertPEM()) {
		t.Fatal("CertPEM is not a PEM certificate")
	}
	cert, err := x509.ParseCertificate(leaf.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	_, err = cert.Verify(x509.VerifyOptions{DNSName: name, Roots: pool})
	return err
}

func TestCAPersistsAcrossLoads(t *testing.T) {
	dir := t.TempDir()
	first, err := LoadOrCreateCA(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreateCA(dir)
	if err != nil {
		t.Fatal(err)
	}
	if string(first.CertPEM()) != string(second.CertPEM()) {
		t.Fatal("reloading the CA produced a different certificate; every container would stop trusting the proxy")
	}
	info, err := os.Stat(filepath.Join(dir, caKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("CA key is mode %o, want 600", perm)
	}
}

func TestCAIssuesVerifiableLeaves(t *testing.T) {
	ca, err := LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"api.example.com", "10.0.0.5"} {
		leaf, err := ca.leaf(host)
		if err != nil {
			t.Fatalf("leaf(%q): %v", host, err)
		}
		if err := verifyLeaf(t, ca, leaf, host); err != nil {
			t.Fatalf("leaf for %q does not verify: %v", host, err)
		}
		if err := verifyLeaf(t, ca, leaf, "other.example.com"); err == nil {
			t.Fatalf("leaf for %q also verifies for another host", host)
		}
		again, err := ca.leaf(host)
		if err != nil {
			t.Fatal(err)
		}
		if again != leaf {
			t.Fatalf("leaf for %q is re-signed on every connection", host)
		}
	}
}

func TestCARejectsATamperedKey(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadOrCreateCA(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, caKeyFile), []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateCA(dir); err == nil {
		t.Fatal("a corrupt CA key loaded; the proxy must fail closed rather than mint a new CA silently")
	}
}
