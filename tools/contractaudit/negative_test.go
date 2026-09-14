package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture copies the repository subtrees the audit reads into a temporary
// directory, so a test can inject a violation without touching the working
// tree. The audit reads only these paths -- if a surface starts reading
// something else, a test here fails rather than silently auditing a fixture
// that no longer matches the real layout.
func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{
		"proto/state/v1",
		"proto/gateway/v1",
		"internal/infrastructure/staterpc",
		"internal/infrastructure/gatewayrpc",
		"internal/webui",
		"ui/src",
	} {
		copyTree(t, filepath.Join("../..", dir), filepath.Join(root, dir))
	}
	return root
}

// copyTree copies a directory recursively. It must recurse: the dashboard's
// call sites live in nested feature folders (ui/src/base, ui/src/chat), so a
// flat copy yields a fixture with zero consumers and every negative test then
// passes for the wrong reason.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("fixture: read %s: %v", src, err)
	}
	for _, entry := range entries {
		from := filepath.Join(src, entry.Name())
		to := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			copyTree(t, from, to)
			continue
		}
		body, err := os.ReadFile(from)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(to, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func auditOf(t *testing.T, root string) []Finding {
	t.Helper()
	audit, err := New(root, "docs/contract-declarations.json")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	findings, err := audit.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return findings
}

func find(findings []Finding, subject string) (Finding, bool) {
	for _, f := range findings {
		if f.Subject == subject {
			return f, true
		}
	}
	return Finding{}, false
}

// TestNegativeInjectedRouteIsReported proves the webui check catches a route
// nobody consumes. Without this the check is decoration: a gate that has never
// failed is a gate nobody can trust.
func TestNegativeInjectedRouteIsReported(t *testing.T) {
	root := fixture(t)

	// A new route with no consumer anywhere in ui/src.
	serverPath := filepath.Join(root, "internal/webui/server.go")
	body, err := os.ReadFile(serverPath)
	if err != nil {
		t.Fatal(err)
	}
	injected := strings.Replace(
		string(body),
		`mux.HandleFunc("GET /api/summary"`,
		`mux.HandleFunc("GET /api/injected-orphan", s.handleSummary)
	mux.HandleFunc("GET /api/summary"`,
		1,
	)
	if injected == string(body) {
		t.Fatal("could not inject the route; the registration shape changed")
	}
	if err := os.WriteFile(serverPath, []byte(injected), 0o644); err != nil {
		t.Fatal(err)
	}

	finding, ok := find(auditOf(t, root), "GET /api/injected-orphan")
	if !ok {
		t.Fatal("injected unconsumed route was not reported")
	}
	if finding.Class != Undeclared {
		t.Errorf("class = %q, want %q", finding.Class, Undeclared)
	}
}

// TestNegativeRemovedConsumerIsReported proves the same check fails in the
// other direction: remove the dashboard's only call to a route and the route
// becomes an undeclared finding.
func TestNegativeRemovedConsumerIsReported(t *testing.T) {
	root := fixture(t)

	apiPath := filepath.Join(root, "ui/src/base/api.jsx")
	body, err := os.ReadFile(apiPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "/api/summary") {
		t.Fatal("fixture does not contain the expected consumer")
	}
	trimmed := strings.ReplaceAll(string(body), "/api/summary", "/api/renamed-by-test")
	if err := os.WriteFile(apiPath, []byte(trimmed), 0o644); err != nil {
		t.Fatal(err)
	}

	finding, ok := find(auditOf(t, root), "GET /api/summary")
	if !ok {
		t.Fatal("route whose consumer was removed was not reported")
	}
	if finding.Class != Undeclared {
		t.Errorf("class = %q, want %q", finding.Class, Undeclared)
	}
}

// TestNegativeInjectedRPCIsReported proves the proto check catches an RPC no
// adapter reaches.
func TestNegativeInjectedRPCIsReported(t *testing.T) {
	root := fixture(t)

	protoPath := filepath.Join(root, "proto/state/v1/state.proto")
	body, err := os.ReadFile(protoPath)
	if err != nil {
		t.Fatal(err)
	}
	injected := strings.Replace(
		string(body),
		"  rpc RegisterTaskGrant(",
		"  rpc InjectedOrphanRPC(InjectedOrphanRPCRequest) returns (InjectedOrphanRPCResponse);\n  rpc RegisterTaskGrant(",
		1,
	)
	if injected == string(body) {
		t.Fatal("could not inject the rpc; the declaration shape changed")
	}
	if err := os.WriteFile(protoPath, []byte(injected), 0o644); err != nil {
		t.Fatal(err)
	}

	finding, ok := find(auditOf(t, root), "StateStoreService/InjectedOrphanRPC")
	if !ok {
		t.Fatal("injected unconsumed RPC was not reported")
	}
	if finding.Class != Undeclared {
		t.Errorf("class = %q, want %q", finding.Class, Undeclared)
	}
}

// TestNegativeStaleDeclarationIsReported proves a declaration that outlived its
// subject fails. This is the direction people forget, and it is what stops an
// allowlist from quietly permitting something nobody checks.
func TestNegativeStaleDeclarationIsReported(t *testing.T) {
	findings := classify("s", []Subject{
		{Name: "Svc/NowConsumed", Consumed: true, Consumer: "internal/x/y.go:1"},
	}, Declarations{Surfaces: map[string]map[string]Declaration{
		"s": {"Svc/NowConsumed": {Reason: "was unconsumed", Tracker: "t"}},
	}})

	finding, ok := find(findings, "Svc/NowConsumed")
	if !ok {
		t.Fatal("stale declaration was not reported")
	}
	if finding.Class != Stale {
		t.Errorf("class = %q, want %q", finding.Class, Stale)
	}
}

// TestNegativeUnparsableCallShapeFailsLoudly proves the extractor refuses to
// under-report. A call shape it cannot parse must be an error, never a
// silently smaller consumer set -- under-reporting is how a gate stops
// checking without anyone noticing.
func TestNegativeUnparsableCallShapeFailsLoudly(t *testing.T) {
	body := `
export const api = {
  exotic: (p) => fetch(buildPath("/api/things"), { method: "GET" }),
};
`
	_, err := scanFile("ui/src/base/exotic.jsx", body)
	if err == nil {
		t.Fatal("unparsable call shape was accepted; the extractor would under-report")
	}
	if !strings.Contains(err.Error(), "would under-report") {
		t.Errorf("error = %v, want the under-report explanation", err)
	}
}
