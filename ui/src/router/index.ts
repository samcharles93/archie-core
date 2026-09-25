import { createRouter, createWebHistory } from "vue-router";

import ChannelsPage from "@/channels/ChannelsPage.vue";
import CuratorsPage from "@/curators/CuratorsPage.vue";
import DashboardPage from "@/dashboard/DashboardPage.vue";
import EventsPage from "@/events/EventsPage.vue";
import LogsPage from "@/logs/LogsPage.vue";
import SchedulingPolicyPage from "@/settings/SchedulingPolicyPage.vue";
import ToolsPage from "@/settings/ToolsPage.vue";
import SystemAdvancedPage from "@/settings/SystemAdvancedPage.vue";
import SystemIdentitiesPage from "@/settings/SystemIdentitiesPage.vue";
import SystemAppearancePage from "@/settings/SystemAppearancePage.vue";
import SystemModelsPage from "@/settings/SystemModelsPage.vue";
import SystemReposPage from "@/settings/SystemReposPage.vue";
import SystemStatusPage from "@/settings/SystemStatusPage.vue";
import SystemTasksPage from "@/settings/SystemTasksPage.vue";
import SkillsPage from "@/skills/SkillsPage.vue";
import TaskDetailPage from "@/tasks/TaskDetailPage.vue";
import TasksPage from "@/tasks/TasksPage.vue";
import WorkflowsPage from "@/workflows/WorkflowsPage.vue";

// internal/gateway/dashboard_tools.go mirrors this table.
//
// meta:
//   label       the navigation label, short because its group carries the context
//   description one line mirrored in the messaging page registry
//   section     the capability from GET /api/capabilities this route needs; a
//               route with no section is served in every composition
//   navPath     the navigation entry this route keeps current
//   soon        visible, greyed and non-navigable
const routes = [
  {
    path: "/",
    name: "dashboard",
    component: DashboardPage,
    meta: {
      label: "Dashboard",
      description:
        "What Archie is working on, what needs you, and token spend.",
    },
  },
  {
    path: "/tasks",
    name: "tasks",
    component: TasksPage,
    meta: {
      label: "Tasks",
      description: "Issues Archie has picked up, and where each one stands.",
    },
  },
  {
    path: "/tasks/:id",
    name: "task-detail",
    component: TaskDetailPage,
    meta: { label: "Task run", nav: false, navPath: "/tasks" },
  },
  {
    path: "/workflows",
    name: "workflows",
    component: WorkflowsPage,
    meta: {
      label: "Workflows",
      description: "The routed workflows and their run history.",
      section: "workflows",
    },
  },
  {
    path: "/settings/skills",
    name: "skills",
    component: SkillsPage,
    meta: {
      navPath: "/settings",
      settings: true,
      label: "Skills",
      description: "The SKILL.md capabilities Archie can activate.",
      section: "skills",
    },
  },
  {
    path: "/settings/curators",
    name: "curators",
    component: CuratorsPage,
    meta: {
      navPath: "/settings",
      settings: true,
      label: "Curators",
      description: "Scheduled passes that curate Archie's memory and captures.",
      section: "curators",
    },
  },
  {
    path: "/events",
    name: "events",
    component: EventsPage,
    meta: {
      label: "Events",
      description:
        "Captured inbound events, how their fields map, and which workflow they start.",
    },
  },
  {
    path: "/settings/status",
    name: "system-status",
    component: SystemStatusPage,
    meta: {
      navPath: "/settings",
      settings: true,
      label: "Status",
      description:
        "Update state, configuration sources, and the listen address.",
      section: "settings",
    },
  },
  {
    path: "/logs",
    name: "logs",
    component: LogsPage,
    meta: {
      label: "Logs",
      description: "The daemon log stream, filterable by level and component.",
      section: "logs",
    },
  },
  {
    path: "/settings/appearance",
    name: "system-appearance",
    component: SystemAppearancePage,
    meta: {
      navPath: "/settings",
      settings: true,
      label: "Appearance",
      description: "Theme and display preferences.",
    },
  },
  {
    path: "/settings/task-execution",
    name: "system-tasks",
    component: SystemTasksPage,
    meta: {
      navPath: "/settings",
      settings: true,
      label: "Task execution",
      description: "Work lifecycle: statuses, operator actions, and budgets.",
      section: "settings",
    },
  },
  {
    path: "/settings/models",
    name: "system-models",
    component: SystemModelsPage,
    meta: {
      navPath: "/settings",
      settings: true,
      label: "Models",
      description: "Model roles and the providers backing them.",
      section: "settings",
    },
  },
  {
    path: "/settings/repositories",
    name: "system-repos",
    component: SystemReposPage,
    meta: {
      navPath: "/settings",
      settings: true,
      label: "Repositories",
      description:
        "Repositories Archie watches, and their per-repo gate overrides.",
      section: "settings",
    },
  },
  {
    path: "/settings/identities",
    name: "system-identities",
    component: SystemIdentitiesPage,
    meta: {
      navPath: "/settings",
      settings: true,
      label: "Identities",
      description: "Persistent actors and lifecycle.",
      section: "settings",
    },
  },
  {
    path: "/settings/channels",
    name: "channels",
    component: ChannelsPage,
    meta: {
      navPath: "/settings",
      settings: true,
      label: "Channels",
      description: "Inbound chat and notification channels, and their state.",
      section: "channels",
    },
  },
  {
    path: "/settings/scheduling-policy",
    name: "settings-scheduling-policy",
    component: SchedulingPolicyPage,
    meta: {
      navPath: "/settings",
      settings: true,
      label: "Scheduling policy",
      description: "How Archie finds work and labels its state on the forge.",
      section: "settings",
    },
  },
  {
    path: "/settings/tools",
    name: "settings-tools",
    component: ToolsPage,
    meta: {
      navPath: "/settings",
      settings: true,
      label: "Tools & MCP",
      description: "The tools agents can call, and the MCP servers that add more.",
      section: "settings",
    },
  },
  {
    path: "/settings/advanced",
    name: "system-advanced",
    component: SystemAdvancedPage,
    meta: {
      navPath: "/settings",
      settings: true,
      label: "Advanced",
      description: "Identity, storage, sandboxing, and dangerous actions.",
      section: "settings",
    },
  },
  {
    path: "/settings",
    redirect: "/settings/models",
    meta: {
      label: "Settings",
      description: "Everything you configure: agents, integrations, runtime.",
    },
  },
  // Paths from before Settings existed, kept for bookmarks and chat history.
  ...Object.entries({
    "/system/status": "/settings/status",
    "/system/appearance": "/settings/appearance",
    "/system/tasks": "/settings/task-execution",
    "/system/models": "/settings/models",
    "/system/repos": "/settings/repositories",
    "/system/identities": "/settings/identities",
    "/system/advanced": "/settings/advanced",
    "/channels": "/settings/channels",
    "/skills": "/settings/skills",
    "/curators": "/settings/curators",
  }).map(([path, redirect]) => ({ path, redirect, meta: { nav: false } })),
];

export { routes };

export const router = createRouter({
  history: createWebHistory(),
  routes,
});

export default router;
