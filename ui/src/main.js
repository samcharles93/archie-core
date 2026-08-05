import "./css/_main.css";
import { el, mount } from "./base/dom.js";
import { icon } from "./base/icons.js";
import { dashboardPage } from "./dashboard/dashboard.js";
import { tasksPage } from "./tasks/tasks.js";
import { skillsPage } from "./skills/skills.js";
import { workflowsPage } from "./workflows/workflows.js";
import { channelsPage } from "./channels/channels.js";
import { settingsPage } from "./settings/settings.js";
import { logsPage } from "./logs/logs.js";
import { memoryPage } from "./memory/memory.js";
import { chatPage } from "./chat/chat.js";

/**
 * Routes are declared once here. Adding a section means adding a feature
 * folder and one entry -- there is no switch statement to hunt for.
 *
 * `soon: true` sections are deliberately visible before they are built: a
 * greyed, labelled entry tells you the capability exists and is coming, which
 * is more honest than hiding it and more useful than a 404.
 */
const routes = [
  { path: "/", label: "Dashboard", icon: "dashboard", view: dashboardPage },
  { path: "/chat", label: "Chat", icon: "chat", view: chatPage },
  { path: "/tasks", label: "Tasks", icon: "tasks", view: tasksPage },
  { path: "/logs", label: "Logs", icon: "logs", view: logsPage },
  { path: "/skills", label: "Skills", icon: "skills", view: skillsPage },
  { path: "/workflows", label: "Workflows", icon: "workflows", view: workflowsPage },
  { path: "/memory", label: "Memory", icon: "memory", view: memoryPage },
  { path: "/channels", label: "Channels", icon: "channels", view: channelsPage },
  { path: "/settings", label: "Configuration", icon: "settings", view: settingsPage },
];

const THEME_KEY = "archie.theme";

function applyTheme(theme) {
  document.documentElement.dataset.theme = theme;
  localStorage.setItem(THEME_KEY, theme);
}

function currentTheme() {
  return localStorage.getItem(THEME_KEY) || "dark";
}


function commandBar(onNavigate) {
  const items = new Map();

  const nav = el(
    "nav.nav",
    ...[null].flatMap(() => [
      ...routes
        .map((route) => {
          const item = el(
            "a.nav-item",
            {
              href: `#${route.path}`,
              onclick: (e) => {
                if (route.soon) e.preventDefault();
                else onNavigate(route.path);
              },
              "aria-disabled": route.soon || undefined,
              title: route.soon ? "Coming soon" : undefined,
            },
            icon(route.icon),
            el("span.nav-label", route.label),
            route.soon && el("span.nav-soon", "soon"),
          );
          items.set(route.path, item);
          return item;
        }),
    ]),
  );

  const themeBtn = el("button.icon-btn", {
    title: "Toggle light and dark",
    "aria-label": "Toggle light and dark",
    onclick: () => {
      applyTheme(currentTheme() === "dark" ? "light" : "dark");
      mount(themeBtn, icon(currentTheme() === "dark" ? "moon" : "sun"));
    },
  });
  mount(themeBtn, icon(currentTheme() === "dark" ? "moon" : "sun"));

  // Search jumps between sections. It is deliberately not a data search: the
  // sections own their own filtering, and a second search that means something
  // different in each place is worse than none.
  const search = el("input", {
    type: "search",
    placeholder: "Jump to\u2026",
    "aria-label": "Jump to a section",
    onkeydown: (e) => {
      if (e.key !== "Enter") return;
      const q = e.target.value.trim().toLowerCase();
      const hit = routes.find((r) => !r.soon && r.label.toLowerCase().startsWith(q));
      if (hit) {
        onNavigate(hit.path);
        e.target.value = "";
        e.target.blur();
      }
    },
  });

  return {
    node: el(
      "header.topbar",
      el("div.brand", el("span.brand-mark", "A"), "Archie"),
      nav,
      el(
        "div.topbar-end",
        el("div.topbar-search", icon("search", { size: 15 }), search),
        el("button.icon-btn", { title: "Documentation", "aria-label": "Documentation" }, icon("help")),
        themeBtn,
        el("div.avatar", { title: "Signed in locally" }, "A"),
      ),
    ),
    highlight(path) {
      for (const [p, node] of items) {
        if (p === path) node.setAttribute("aria-current", "page");
        else node.removeAttribute("aria-current");
      }
    },
  };
}

function start() {
  applyTheme(currentTheme());

  const outlet = el("main.main");
  const bar = commandBar(navigate);
  mount(document.getElementById("app"), el("div.shell", bar.node, outlet));

  function navigate(path) {
    if (location.hash !== `#${path}`) location.hash = path;
    else show(path);
  }

  function show(rawPath) {
    // Let a page release its subscriptions before it is replaced, so the SSE
    // stream does not leak a connection per navigation.
    outlet.firstElementChild?.dispatchEvent(new CustomEvent("archie:teardown"));
    const [path, query = ""] = rawPath.split("?", 2);
    const route = routes.find((r) => r.path === path) || routes[0];
    bar.highlight(route.path);
    mount(outlet, route.view ? route.view(new URLSearchParams(query)) : comingSoon(route));
  }

  window.addEventListener("hashchange", () => show(location.hash.slice(1) || "/"));
  show(location.hash.slice(1) || "/");
}

function comingSoon(route) {
  return el(
    "div",
    el("div.page-head", el("div", el("h1.page-title", route.label))),
    el(
      "div.card",
      el(
        "div.empty",
        el("div.empty-title", `${route.label} is not built yet`),
        el("div", "This section is next up. Nothing is broken."),
      ),
    ),
  );
}

start();
