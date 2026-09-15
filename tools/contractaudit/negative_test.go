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
// under-report. A path built from a bare /api fragment must be an error, never
// a silently smaller consumer set.
func TestNegativeUnparsableCallShapeFailsLoudly(t *testing.T) {
	body := `
const base = "/api";
const url = base + "/things";
`
	_, err := scanFile("ui/src/base/exotic.jsx", body)
	if err == nil {
		t.Fatal("a path built from a bare /api fragment was accepted; the extractor would under-report")
	}
	if !strings.Contains(err.Error(), "would under-report") {
		t.Errorf("error = %v, want the under-report explanation", err)
	}
}

// TestNegativeCommentedOutCallIsStillReported is the anti-vacuity test at the
// surface level. Commenting out a real call leaves the path text in the file,
// so an extractor that credits any quoted path reports the route as healthy
// while nothing calls it -- a silent hole rather than a loud failure. The route
// must become an undeclared finding.
func TestNegativeCommentedOutCallIsStillReported(t *testing.T) {
	root := fixture(t)

	apiPath := filepath.Join(root, "ui/src/base/api.jsx")
	body, err := os.ReadFile(apiPath)
	if err != nil {
		t.Fatal(err)
	}
	live := `summary: () => request("/api/summary"),`
	if !strings.Contains(string(body), live) {
		t.Fatal("fixture does not contain the expected live call; the client changed shape")
	}
	commented := strings.Replace(string(body), live, `// retired: () => request("/api/summary"),`, 1)
	if err := os.WriteFile(apiPath, []byte(commented), 0o644); err != nil {
		t.Fatal(err)
	}

	finding, ok := find(auditOf(t, root), "GET /api/summary")
	if !ok {
		t.Fatal("a route whose only remaining mention is a comment was not reported")
	}
	if finding.Class != Undeclared {
		t.Errorf("class = %q, want %q: a comment cannot prove a route is reachable", finding.Class, Undeclared)
	}
}

// TestScanFileCreditsAnyCallSignature pins the lesson from a real regression:
// the first version of this extractor matched the call by name (`req|fetch`),
// and rewriting the dashboard's helper from `req(` to `request(` silently
// reported 16 live routes as unconsumed. Extraction must key on the path
// literal as a call argument, not on the name of the function carrying it.
func TestScanFileCreditsAnyCallSignature(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "the original helper name",
			body: `const a = req("/api/summary");`,
			want: []string{"/api/summary"},
		},
		{
			name: "a renamed helper",
			body: `const a = request("/api/summary");`,
			want: []string{"/api/summary"},
		},
		{
			name: "a name the extractor has never heard of",
			body: `const a = someOtherTransport("/api/summary");`,
			want: []string{"/api/summary"},
		},
		{
			name: "an interpolated path in backticks",
			body: "const a = request(`/api/tasks/${id}`);",
			want: []string{"/api/tasks/${id}"},
		},
		{
			name: "an EventSource subscription",
			body: `const s = new EventSource("/events");`,
			want: []string{"/events"},
		},
		{
			name: "a path with a query string",
			body: `const a = request("/api/logs" + qs(params));`,
			want: []string{"/api/logs"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := scanFile("ui/src/base/client.jsx", tc.body)
			if err != nil {
				t.Fatalf("scanFile: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("scanFile(%q) = %v, want %v", tc.body, got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("scanFile(%q)[%d] = %q, want %q", tc.body, i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestScanFileIgnoresNonCallLiterals is the anti-vacuity half, and the more
// important one. Crediting any quoted path -- rather than a path passed to a
// call -- makes the whole surface pass on a file that issues no requests at
// all: a comment, a retired-route array, or a display label would each count as
// proof of consumption. That failure is silent, which makes it worse than a
// false finding.
func TestScanFileIgnoresNonCallLiterals(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"a constant that is never passed to anything", `const p = "/api/summary";`},
		{"a comment quoting a path", `// the route "/api/summary" is dead`},
		{"an array of retired paths", `const RETIRED = ["/api/summary", "/api/tasks"];`},
		{"an object property", `const cfg = { path: "/api/summary" };`},
		{"a template constant", "const p = `/api/summary`;"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := scanFile("ui/src/base/client.jsx", tc.body)
			if err != nil {
				t.Fatalf("scanFile: %v", err)
			}
			if len(got) != 0 {
				t.Errorf(
					"scanFile(%q) = %v, want no consumers: a literal that is not a call argument cannot prove a route is reachable",
					tc.body, got,
				)
			}
		})
	}
}
