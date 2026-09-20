import { createRouter, createWebHistory } from "vue-router";

import Placeholder from "@/views/Placeholder.vue";

// internal/gateway/dashboard_tools.go mirrors this table; its test parses this file.
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
  history: createWebHistory(),
  routes,
});

export default router;
