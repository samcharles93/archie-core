package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/samcharles93/archie-core/internal/tools"
)

// DashboardPage describes one dashboard surface so the chat agent can both
// understand what each place is for and point the operator there.
type DashboardPage struct {
	// Path is the route (e.g. "/tasks"). It is what the browser hash becomes.
	Path string
	// Label is the human name shown in navigation (e.g. "Tasks").
	Label string
	// Description is a one-line plain-language explanation of what the page
	// shows, so the agent can answer "where do I go for that?" correctly.
	Description string
}

// dashboardPages is the single source of truth for what the dashboard exposes.
// It is deliberately a package-level registry rather than a per-router field:
// the same pages are shown regardless of identity, and the prompt's current
// page line and the dashboard_navigate tool both draw from it, so the agent
// never points at a page it was not told about.
var dashboardPages = []DashboardPage{
	{Path: "/", Label: "Dashboard", Description: "The control room: what Archie is working on, what needs you, and token spend."},
	{Path: "/chat", Label: "Chat", Description: "Conversations with Archie and the web chat session tools."},
	{Path: "/tasks", Label: "Tasks", Description: "Issues Archie has picked up and where they stand: the work queue."},
	{Path: "/logs", Label: "Logs", Description: "The daemon log stream, filterable by level and component."},
	{Path: "/captures", Label: "Event inspector", Description: "Captured inbound events for inspection."},
	{Path: "/mappings", Label: "Field mappings", Description: "How inbound event fields map onto internal ones."},
	{Path: "/skills", Label: "Skills", Description: "The SKILL.md capabilities Archie can activate."},
	{Path: "/workflows", Label: "Workflows", Description: "The routed workflows (bootstrap, implement, tdd, feasibility) and their run history."},
	{Path: "/memory", Label: "Memory", Description: "What Archie carries between conversations and the tools that read it."},
	{Path: "/channels", Label: "Channels", Description: "Inbound chat and notification channels (e.g. Telegram) and their state."},
	{Path: "/settings", Label: "Configuration", Description: "Daemon configuration editable from the dashboard."},
}

// DashboardPages returns a copy of the dashboard page registry.
func DashboardPages() []DashboardPage {
	out := make([]DashboardPage, len(dashboardPages))
	copy(out, dashboardPages)
	return out
}

// PageIndexResult is what page_index returns: the full list of dashboard pages
// and what each one shows, so the agent can answer where to go.
type PageIndexResult struct {
	Pages []DashboardPage `json:"pages"`
}

// pageIndexTool returns the tool that lists every dashboard page. It is only
// registered for the web channel (see PageIndexTools), because pointing a
// non-web operator at an internal dashboard route is meaningless.
func pageIndexTool() (tools.ToolEntry, bool) {
	entry := tools.ToolEntry{
		Name:           "page_index",
		Toolset:        "dashboard",
		Description:    "List every dashboard page and, in one line, what it shows. Use it to tell the operator where to go for something rather than guessing.",
		Classification: tools.ClassIdempotent,
		Handler: func(_ context.Context, _ map[string]any) (any, error) {
			return PageIndexResult{Pages: DashboardPages()}, nil
		},
	}
	return entry, true
}

// DashboardNavigateResult is what dashboard_navigate returns. The web UI
// renders it as a clickable chip that routes the operator to the page.
type DashboardNavigateResult struct {
	Path  string `json:"path"`
	Label string `json:"label"`
}

// dashboardNavigateTool resolves a page path and returns the navigation the UI
// can follow. It validates against the registry and refuses an unknown route
// rather than guessing, so a chip is only ever produced for a real page.
func dashboardNavigateTool() (tools.ToolEntry, bool) {
	entry := tools.ToolEntry{
		Name:           "dashboard_navigate",
		Toolset:        "dashboard",
		Description:    "Point the operator at a dashboard page. Pass the page path (e.g. /tasks). Returns the page to navigate to; the web chat renders it as a clickable chip. Never guess a path, look it up with page_index first.",
		Classification: tools.ClassIdempotent,
		Schema: tools.JSONSchema{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "The dashboard route to navigate to, e.g. /tasks. Must match a page_index entry.",
				},
			},
			"required": []any{"path"},
		},
		Handler: func(_ context.Context, input map[string]any) (any, error) {
			path := strings.TrimSpace(asString(input["path"]))
			if path == "" {
				return nil, errors.New("dashboard_navigate: path is required")
			}
			for _, p := range dashboardPages {
				if p.Path == path {
					return DashboardNavigateResult{Path: p.Path, Label: p.Label}, nil
				}
			}
			return nil, fmt.Errorf("dashboard_navigate: unknown page %s", path)
		},
	}
	return entry, true
}

// PageIndexTools returns the dashboard tools for a channel. Only the web UI
// has a dashboard (and only the web chat sends a Current page), so a non-web
// channel (e.g. telegram) gets none.
func PageIndexTools(channel string) []tools.ToolEntry {
	if !strings.EqualFold(channel, "web") {
		return nil
	}
	pageIndex, _ := pageIndexTool()
	navigate, _ := dashboardNavigateTool()
	return []tools.ToolEntry{pageIndex, navigate}
}
