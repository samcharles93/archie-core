import { createRouter, createWebHashHistory } from "vue-router";

import Placeholder from "@/views/Placeholder.vue";

// The dashboard's route table, and the single source of truth for what the
// dashboard exposes.
//
// internal/gateway/dashboard_tools.go hand-copies this list so the chat agent
// can navigate the operator, and TestDashboardPagesRegistryCoversEveryRoute
// parses this file to hold the two in step. That test reads `const routes = [`
// through to `];`, one route per line, skipping `nav: false` entries -- keep
// the shape below literal enough for it to parse.
//
// Hash history, not web history: the dashboard is served from a path the build
// does not know (ui/embed.go embeds a relative-base bundle), and every existing
// link, bookmark and agent-issued navigation is of the form #/tasks.
const routes = [
  { path: "/", name: "dashboard", label: "Dashboard", component: Placeholder },
  { path: "/tasks", name: "tasks", label: "Tasks", component: Placeholder },
  { path: "/tasks/:id", name: "task-detail", label: "Task run", component: Placeholder, nav: false },
  { path: "/logs", name: "logs", label: "Logs", component: Placeholder, section: "logs" },
  { path: "/captures", name: "captures", label: "Event inspector", component: Placeholder, section: "captures" },
  { path: "/mappings", name: "mappings", label: "Field mappings", component: Placeholder, section: "mappings" },
  { path: "/bindings", name: "bindings", label: "Playbook bindings", component: Placeholder, section: "bindings" },
  { path: "/skills", name: "skills", label: "Skills", component: Placeholder, section: "skills" },
  { path: "/workflows", name: "workflows", label: "Workflows", component: Placeholder, section: "workflows" },
  { path: "/curators", name: "curators", label: "Curators", component: Placeholder, section: "curators" },
  { path: "/channels", name: "channels", label: "Channels", component: Placeholder, section: "channels" },
  { path: "/settings", name: "settings", label: "Configuration", component: Placeholder, section: "settings" },
];

export const dashboardRoutes = routes;

export const router = createRouter({
  history: createWebHashHistory(),
  routes,
});

export default router;
