package gateway

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// quietContext is the tool execution context; the tools never use it beyond
// the signature, so a background context is safe and deterministic.
func quietContext() context.Context { return context.Background() }

// routePathRe matches one route entry's path in the dashboard's route table.
var routePathRe = regexp.MustCompile(`path:\s*"([^"]+)"`)

// dashboardRoutesFromSource reads the dashboard's route table out of the UI
// source.
//
// The page registry is what the agent is told the dashboard exposes, and it is
// a hand-copy of that table. Hand-copies drift: /memory stayed in the registry
// after the page was deleted, and /bindings and /curators were never added --
// so the agent offered the operator a route that no longer existed and was
// blind to two that did. Reading the table back is what turns the registry's
// "single source of truth" claim into something a test can hold.
func dashboardRoutesFromSource(t *testing.T) []string {
	t.Helper()
	path := filepath.Join("..", "..", "ui", "src", "router", "index.ts")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	block := string(src)
	start := strings.Index(block, "const routes = [")
	if start < 0 {
		t.Fatalf("%s: no routes table", path)
	}
	block = block[start:]
	if end := strings.Index(block, "\n];"); end >= 0 {
		block = block[:end]
	}

	// One route per line. A detail route (nav: false) is addressed by URL but is
	// not a navigation entry, so the registry does not carry it.
	var paths []string
	for line := range strings.SplitSeq(block, "\n") {
		match := routePathRe.FindStringSubmatch(line)
		if match == nil || strings.Contains(line, "nav: false") {
			continue
		}
		paths = append(paths, match[1])
	}
	if len(paths) == 0 {
		t.Fatalf("%s: parsed no routes", path)
	}
	return paths
}

func TestDashboardPagesRegistryCoversEveryRoute(t *testing.T) {
	pages := DashboardPages()
	if len(pages) == 0 {
		t.Fatal("DashboardPages() returned no pages")
	}
	seen := map[string]bool{}
	for _, p := range pages {
		if p.Path == "" || p.Label == "" || p.Description == "" {
			t.Fatalf("page %#v is missing path/label/description", p)
		}
		if seen[p.Path] {
			t.Fatalf("duplicate page path %q", p.Path)
		}
		seen[p.Path] = true
	}

	routes := dashboardRoutesFromSource(t)
	inDashboard := map[string]bool{}
	for _, route := range routes {
		inDashboard[route] = true
		if !seen[route] {
			t.Errorf("route %q is a dashboard page the registry does not list, so the agent cannot point at it", route)
		}
	}
	for _, p := range pages {
		if !inDashboard[p.Path] {
			t.Errorf("registry lists %q (%s), which is not a dashboard route: the agent would send the operator somewhere that does not exist", p.Path, p.Label)
		}
	}
}

func TestPageIndexToolReturnsEveryPage(t *testing.T) {
	entry, ok := pageIndexTool()
	if !ok {
		t.Fatal("page_index tool not available")
	}
	if entry.Name != "page_index" {
		t.Fatalf("tool name = %q", entry.Name)
	}
	out, err := entry.Handler(quietContext(), map[string]any{})
	if err != nil {
		t.Fatalf("page_index handler: %v", err)
	}
	res, ok := out.(PageIndexResult)
	if !ok {
		t.Fatalf("page_index returned %T, want PageIndexResult", out)
	}
	if len(res.Pages) != len(DashboardPages()) {
		t.Fatalf("page_index returned %d pages, want %d", len(res.Pages), len(DashboardPages()))
	}
}

func TestDashboardNavigateResolvesKnownRoute(t *testing.T) {
	entry, ok := dashboardNavigateTool()
	if !ok {
		t.Fatal("dashboard_navigate tool not available")
	}
	out, err := entry.Handler(quietContext(), map[string]any{"path": "/tasks"})
	if err != nil {
		t.Fatalf("dashboard_navigate(/tasks): %v", err)
	}
	res, ok := out.(DashboardNavigateResult)
	if !ok {
		t.Fatalf("dashboard_navigate returned %T, want DashboardNavigateResult", out)
	}
	if res.Path != "/tasks" {
		t.Fatalf("resolved path = %q, want /tasks", res.Path)
	}
	if res.Label == "" {
		t.Fatal("resolved label is empty")
	}
}

func TestDashboardNavigateRejectsUnknownRoute(t *testing.T) {
	entry, ok := dashboardNavigateTool()
	if !ok {
		t.Fatal("dashboard_navigate tool not available")
	}
	_, err := entry.Handler(quietContext(), map[string]any{"path": "/not-a-page"})
	if err == nil {
		t.Fatal("dashboard_navigate(/not-a-page) returned no error")
	}
}

func TestDashboardNavigateRejectsEmptyPath(t *testing.T) {
	entry, _ := dashboardNavigateTool()
	if _, err := entry.Handler(quietContext(), map[string]any{}); err == nil {
		t.Fatal("dashboard_navigate with no path returned no error")
	}
}

func TestDashboardToolsBindWebOnly(t *testing.T) {
	// Only the web channel should carry dashboard tooling: pointing a Telegram
	// operator at an internal dashboard route is meaningless.
	web := PageIndexTools("web")
	if len(web) == 0 {
		t.Fatal("web channel should get dashboard tools")
	}
	names := map[string]bool{}
	for _, e := range web {
		names[e.Name] = true
	}
	if !names["page_index"] || !names["dashboard_navigate"] {
		t.Fatalf("web dashboard tools missing page_index/dashboard_navigate: %v", names)
	}

	// A non-web channel gets none.
	if got := PageIndexTools("telegram"); len(got) != 0 {
		t.Fatalf("telegram channel got %d dashboard tools, want 0", len(got))
	}
}
