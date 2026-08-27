package gateway

import (
	"context"
	"testing"
)

// quietContext is the tool execution context; the tools never use it beyond
// the signature, so a background context is safe and deterministic.
func quietContext() context.Context { return context.Background() }

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
	// The dashboard's primary surfaces must be present so the agent can point
	// the operator at the control room and the work queue.
	for _, want := range []string{"/", "/tasks", "/logs"} {
		if !seen[want] {
			t.Errorf("page index does not cover %q", want)
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
