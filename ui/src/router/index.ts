import { createRouter, createWebHistory } from "vue-router";

import DashboardPage from "@/dashboard/DashboardPage.vue";
import Placeholder from "@/views/Placeholder.vue";

// internal/gateway/dashboard_tools.go mirrors this table; its test parses this
// file, so keep one route per line with `path: "..."` on a single line, and
// `nav: false` on any entry the registry should skip.
//
// meta:
//   label       the navigation label, short because its group carries the context
//   description one line, shown as the item's tooltip and mirrored in the registry
//   section     the capability from GET /api/capabilities this route needs; a
//               route with no section is served in every composition
//   navPath     the navigation entry this route keeps current
//   soon        visible, greyed and non-navigable
const routes = [
  { path: "/", name: "dashboard", component: DashboardPage, meta: { label: "Dashboard", description: "What Archie is working on, what needs you, and token spend." } },
  { path: "/tasks", name: "tasks", component: Placeholder, meta: { label: "Tasks", description: "Issues Archie has picked up, and where each one stands." } },
  { path: "/tasks/:id", name: "task-detail", component: Placeholder, meta: { label: "Task run", nav: false, navPath: "/tasks" } },
  { path: "/workflows", name: "workflows", component: Placeholder, meta: { label: "Workflows", description: "The routed workflows and their run history.", section: "workflows" } },
  { path: "/skills", name: "skills", component: Placeholder, meta: { label: "Skills", description: "The SKILL.md capabilities Archie can activate.", section: "skills" } },
  { path: "/curators", name: "curators", component: Placeholder, meta: { label: "Curators", description: "Scheduled passes that curate Archie's memory and captures.", section: "curators" } },
  { path: "/captures", name: "captures", component: Placeholder, meta: { label: "Inspector", description: "Captured inbound events, as they were received.", section: "captures" } },
  { path: "/mappings", name: "mappings", component: Placeholder, meta: { label: "Mappings", description: "How inbound event fields map onto internal ones.", section: "mappings" } },
  { path: "/bindings", name: "bindings", component: Placeholder, meta: { label: "Bindings", description: "Which workflow a captured event starts.", section: "bindings" } },
  { path: "/system/status", name: "system-status", component: Placeholder, meta: { label: "Status", description: "Update state, configuration sources, and the listen address.", section: "settings" } },
  { path: "/logs", name: "logs", component: Placeholder, meta: { label: "Logs", description: "The daemon log stream, filterable by level and component.", section: "logs" } },
  { path: "/system/appearance", name: "system-appearance", component: Placeholder, meta: { label: "Appearance", description: "Theme and display preferences." } },
  { path: "/system/tasks", name: "system-tasks", component: Placeholder, meta: { label: "Task settings", description: "Work lifecycle: statuses, operator actions, and budgets.", section: "settings" } },
  { path: "/system/models", name: "system-models", component: Placeholder, meta: { label: "Models", description: "Model roles and the providers backing them.", section: "settings" } },
  { path: "/system/repos", name: "system-repos", component: Placeholder, meta: { label: "Repositories", description: "Repositories Archie watches, and their per-repo gate overrides.", section: "settings" } },
  { path: "/channels", name: "channels", component: Placeholder, meta: { label: "Channels", description: "Inbound chat and notification channels, and their state.", section: "channels" } },
  { path: "/system/advanced", name: "system-advanced", component: Placeholder, meta: { label: "Advanced", description: "Identity, storage, sandboxing, and dangerous actions.", section: "settings" } },
  { path: "/settings", redirect: "/system/status", meta: { nav: false } },
];

export { routes };

export const router = createRouter({
  history: createWebHistory(),
  routes,
});

export default router;
