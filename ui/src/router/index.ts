import { createRouter, createWebHistory } from "vue-router";

import ChannelsPage from "@/channels/ChannelsPage.vue";
import CuratorsPage from "@/curators/CuratorsPage.vue";
import DashboardPage from "@/dashboard/DashboardPage.vue";
import EventsPage from "@/events/EventsPage.vue";
import LogsPage from "@/logs/LogsPage.vue";
import OrgShell from "@/org/OrgShell.vue";
import OrgMembersPage from "@/org/OrgMembersPage.vue";
import OrgAgentsPage from "@/org/OrgAgentsPage.vue";
import OrgWorkspacesPage from "@/org/OrgWorkspacesPage.vue";
import OrgAccessPage from "@/org/OrgAccessPage.vue";
import OrgTokensPage from "@/org/OrgTokensPage.vue";
import SchedulingPolicyPage from "@/settings/SchedulingPolicyPage.vue";
import ReviewSettingsPage from "@/settings/ReviewSettingsPage.vue";
import ToolsPage from "@/settings/ToolsPage.vue";
import HistoryPage from "@/settings/HistoryPage.vue";
import PersonasPage from "@/settings/PersonasPage.vue";
import SoulPage from "@/settings/SoulPage.vue";
import SchedulesPage from "@/settings/SchedulesPage.vue";
import ExtensionsPage from "@/settings/ExtensionsPage.vue";
import PluginsPage from "@/settings/PluginsPage.vue";
import ContainerRuntimePage from "@/settings/ContainerRuntimePage.vue";
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
    path: "/org",
    component: OrgShell,
    meta: {
      label: "Org",
      description: "Your org: members, agents, workspaces, access and tokens.",
      navPath: "/org",
    },
    children: [
      {
        path: "",
        name: "org-members",
        component: OrgMembersPage,
        meta: { label: "Members", navPath: "/org" },
      },
      {
        path: "agents",
        name: "org-agents",
        component: OrgAgentsPage,
        meta: { label: "Agents", navPath: "/org" },
      },
      {
        path: "workspaces",
        name: "org-workspaces",
        component: OrgWorkspacesPage,
        meta: { label: "Workspaces", navPath: "/org" },
      },
      {
        path: "access",
        name: "org-access",
        component: OrgAccessPage,
        meta: { label: "Access", navPath: "/org" },
      },
      {
        path: "tokens",
        name: "org-tokens",
        component: OrgTokensPage,
        meta: { label: "Tokens", navPath: "/org" },
      },
    ],
  },
  // Identities and access policies moved into Org; old bookmarks still resolve.
  { path: "/settings/identities", redirect: "/org/agents", meta: { nav: false } },
  { path: "/settings/access-policies", redirect: "/org/access", meta: { nav: false } },
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
    path: "/settings/personas",
    name: "settings-personas",
    component: PersonasPage,
    meta: {
      navPath: "/settings",
      settings: true,
      label: "Personas",
      description: "The system prompts chat runs under, and the default.",
      section: "settings",
    },
  },
  {
    path: "/settings/soul",
    name: "settings-soul",
    component: SoulPage,
    meta: {
      navPath: "/settings",
      settings: true,
      label: "SOUL",
      description: "Archie's user-authored identity and tone.",
      section: "settings",
    },
  },
  {
    path: "/settings/schedules",
    name: "settings-schedules",
    component: SchedulesPage,
    meta: {
      navPath: "/settings",
      settings: true,
      label: "Schedules",
      description: "Work Archie starts on its own, on a timetable.",
      section: "settings",
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
    path: "/settings/review",
    name: "settings-review",
    component: ReviewSettingsPage,
    meta: {
      navPath: "/settings",
      settings: true,
      label: "Review",
      description: "How Archie reviews pull requests: precision filtering and operator approval before posting.",
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
    path: "/settings/extensions",
    name: "settings-extensions",
    component: ExtensionsPage,
    meta: {
      navPath: "/settings",
      settings: true,
      label: "Extensions",
      description: "Installed extensions and their grants.",
      section: "settings",
    },
  },
  {
    path: "/settings/plugins",
    name: "settings-plugins",
    component: PluginsPage,
    meta: {
      navPath: "/settings",
      settings: true,
      label: "Plugins",
      description: "Where extension directories are loaded from.",
      section: "settings",
    },
  },
  {
    path: "/settings/container-runtime",
    name: "settings-container-runtime",
    component: ContainerRuntimePage,
    meta: {
      navPath: "/settings",
      settings: true,
      label: "Container runtime",
      description: "The containers agents run in.",
      section: "settings",
    },
  },
  {
    path: "/settings/harness",
    name: "settings-harness",
    // Lazy so xterm.js stays out of the shell every page loads.
    component: () => import("@/harness/HarnessPage.vue"),
    meta: {
      navPath: "/settings",
      settings: true,
      label: "Harness",
      description: "OAuth credential bindings and the Kit setup terminal.",
      section: "settings",
    },
  },
  {
    path: "/settings/history",
    name: "settings-history",
    component: HistoryPage,
    meta: {
      navPath: "/settings",
      settings: true,
      wide: true,
      label: "History",
      description: "Every settings change: who made it, when, and what it was before.",
      section: "settings",
    },
  },
  {
    path: "/settings",
    redirect: "/settings/personas",
    meta: {
      label: "Settings",
    },
  },
  // Paths from before Settings existed, kept for bookmarks and chat history.
  ...Object.entries({
    "/system/status": "/settings/status",
    "/system/appearance": "/settings/appearance",
    "/system/tasks": "/settings/task-execution",
    "/system/models": "/settings/models",
    "/system/repos": "/settings/repositories",
    "/system/identities": "/org/agents",
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
