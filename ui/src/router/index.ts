import { createRouter, createWebHistory } from "vue-router";

import ChannelsPage from "@/channels/ChannelsPage.vue";
import CuratorsPage from "@/curators/CuratorsPage.vue";
import DashboardPage from "@/dashboard/DashboardPage.vue";
import EventsPage from "@/events/EventsPage.vue";
import LogsPage from "@/logs/LogsPage.vue";
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

// internal/gateway/dashboard_tools.go mirrors this table; its test parses this
// file, so keep one route per line with `path: "..."` on a single line, and
// `nav: false` on any entry the registry should skip.
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
    path: "/skills",
    name: "skills",
    component: SkillsPage,
    meta: {
      label: "Skills",
      description: "The SKILL.md capabilities Archie can activate.",
      section: "skills",
    },
  },
  {
    path: "/curators",
    name: "curators",
    component: CuratorsPage,
    meta: {
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
  // The three former destinations, kept as bookmarks rather than removed: a
  // link, a bookmark or an agent that learned these URLs still lands somewhere
  // that works. `nav: false` is what the page registry's test reads to skip
  // them -- they are addressed by URL, not offered as destinations.
  {
    path: "/system/status",
    name: "system-status",
    component: SystemStatusPage,
    meta: {
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
    path: "/system/appearance",
    name: "system-appearance",
    component: SystemAppearancePage,
    meta: {
      label: "Appearance",
      description: "Theme and display preferences.",
    },
  },
  {
    path: "/system/tasks",
    name: "system-tasks",
    component: SystemTasksPage,
    meta: {
      label: "Task settings",
      description: "Work lifecycle: statuses, operator actions, and budgets.",
      section: "settings",
    },
  },
  {
    path: "/system/models",
    name: "system-models",
    component: SystemModelsPage,
    meta: {
      label: "Models",
      description: "Model roles and the providers backing them.",
      section: "settings",
    },
  },
  {
    path: "/system/repos",
    name: "system-repos",
    component: SystemReposPage,
    meta: {
      label: "Repositories",
      description:
        "Repositories Archie watches, and their per-repo gate overrides.",
      section: "settings",
    },
  },
  {
    path: "/system/identities",
    name: "system-identities",
    component: SystemIdentitiesPage,
    meta: {
      label: "Identities",
      description: "Persistent actors and lifecycle.",
      section: "settings",
    },
  },
  {
    path: "/channels",
    name: "channels",
    component: ChannelsPage,
    meta: {
      label: "Channels",
      description: "Inbound chat and notification channels, and their state.",
      section: "channels",
    },
  },
  {
    path: "/system/advanced",
    name: "system-advanced",
    component: SystemAdvancedPage,
    meta: {
      label: "Advanced",
      description: "Identity, storage, sandboxing, and dangerous actions.",
      section: "settings",
    },
  },
  { path: "/settings", redirect: "/system/status", meta: { nav: false } },
];

export { routes };

export const router = createRouter({
  history: createWebHistory(),
  routes,
});

export default router;
