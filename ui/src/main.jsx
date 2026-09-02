import "./css/_main.css";
import { el, mount } from "./base/dom.jsx";
import { icon } from "./base/icons.jsx";
import { dashboardPage } from "./dashboard/dashboard.jsx";
import { tasksPage } from "./tasks/tasks.jsx";
import { skillsPage } from "./skills/skills.jsx";
import { workflowsPage } from "./workflows/workflows.jsx";
import { channelsPage } from "./channels/channels.jsx";
import { curatorsPage } from "./curators/curators.jsx";
import { settingsPage } from "./settings/settings.jsx";
import { logsPage } from "./logs/logs.jsx";
import { capturesPage } from "./captures/captures.jsx";
import { mappingsPage } from "./mappings/mappings.jsx";
import { memoryPage } from "./memory/memory.jsx";
import { chatPage } from "./chat/chat.jsx";

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
  { path: "/captures", label: "Event inspector", icon: "captures", view: capturesPage },
  { path: "/mappings", label: "Field mappings", icon: "mappings", view: mappingsPage },
  { path: "/skills", label: "Skills", icon: "skills", view: skillsPage },
  { path: "/workflows", label: "Workflows", icon: "workflows", view: workflowsPage },
  { path: "/memory", label: "Memory", icon: "memory", view: memoryPage },
  { path: "/curators", label: "Curators", icon: "curators", view: curatorsPage },
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

function commandBar(onNavigate, onToggleChat) {
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
  let searchWrap;
  const closeMobileSearch = () => {
    searchWrap?.classList.remove("is-open");
    searchToggle.setAttribute("aria-expanded", "false");
  };
  const searchToggle = el(
    "button.icon-btn.mobile-search-toggle",
    {
      type: "button",
      "aria-label": "Open Jump to navigation",
      "aria-expanded": "false",
      onclick: () => {
        searchWrap.classList.add("is-open");
        searchToggle.setAttribute("aria-expanded", "true");
        search.focus();
      },
    },
    icon("search", { size: 15 }),
  );
  const search = el("input", {
    type: "search",
    placeholder: "Jump to\u2026",
    "aria-label": "Jump to a section",
    onkeydown: (e) => {
      if (e.key === "Escape") {
        // preventDefault so the window-level handler (chat drawer close) does
        // not also fire: dismissing the search should not dismiss the drawer.
        e.preventDefault();
        closeMobileSearch();
        e.target.blur();
        return;
      }
      if (e.key !== "Enter") return;
      const q = e.target.value.trim().toLowerCase();
      const hit = routes.find((r) => !r.soon && r.label.toLowerCase().startsWith(q));
      if (hit) {
        onNavigate(hit.path);
        e.target.value = "";
        e.target.blur();
        closeMobileSearch();
      }
    },
  });
  searchWrap = el(
    "div.topbar-search",
    el("span.topbar-search-icon", { "aria-hidden": "true" }, icon("search", { size: 15 })),
    searchToggle,
    search,
  );

  return {
    node: el(
      "header.topbar",
      el("div.brand", el("span.brand-mark", "A"), "Archie"),
      nav,
      el(
        "div.topbar-end",
        searchWrap,
        el("button.icon-btn.icon-btn-chat", {
          "aria-label": "Open chat",
          title: "Chat with Archie",
          "aria-expanded": "false",
          onclick: () => onToggleChat?.(),
        }, icon("chat")),
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
  // The chat drawer lives beside the outlet, mounted once. It is closed by
  // default and opened from the topbar launcher; because it is a slide-over
  // rather than a route, the operator can talk to Archie from any page
  // without leaving the work that prompted the question. It hosts a single
  // chatPage() instance, so session state and the stream survive navigation
  // and are not duplicated per page.
  const chatDrawer = el("aside.chat-drawer", { "aria-label": "Chat with Archie" });
  const chatClose = el("button.icon-btn.chat-drawer-close", {
    "aria-label": "Close chat",
    title: "Close chat",
    onclick: () => toggleChat(false),
  }, icon("close"));
  const drawerHead = el("div.chat-drawer-head", el("strong", "Archie"), chatClose);
  chatDrawer.append(el("div.chat-drawer-panel", drawerHead, chatPage()),
    el("div.chat-scrim", { onclick: () => toggleChat(false) }));
  const bar = commandBar(navigate, () => toggleChat());
  const shell = el("div.shell", bar.node, outlet, chatDrawer);
  mount(document.getElementById("app"), shell);

  function toggleChat(open) {
    const shouldOpen = open === undefined ? !chatDrawer.classList.contains("is-open") : open;
    chatDrawer.classList.toggle("is-open", shouldOpen);
    document.body.classList.toggle("chat-open", shouldOpen);
    const btn = shell.querySelector(".icon-btn-chat");
    if (btn) btn.setAttribute("aria-expanded", String(shouldOpen));
  }
  window.__archieToggleChat = toggleChat;

  function navigate(path) {
    if (location.hash !== '#' + path) location.hash = path;
    else show(path);
  }

  function show(rawPath) {
    // Let a page release its subscriptions before it is replaced, so the SSE
    // stream does not leak a connection per navigation.
    outlet.firstElementChild?.dispatchEvent(new CustomEvent("archie:teardown"));
    
    // Unmount Preact cleanly if a Preact component was mounted here
    import("preact").then(({ render, isValidElement }) => {
      render(null, outlet);

      const [path, query = ""] = rawPath.split("?", 2);
      const route = routes.find((r) => r.path === path) || routes[0];
      bar.highlight(route.path);
      // The chat is a drawer, not a page: /chat opens it rather than mounting a
      // second chatPage() (which would duplicate session state and the stream).
      if (route.path === "/chat") {
        mount(outlet);
        toggleChat(true);
        return;
      }
      
      const viewContent = route.view ? route.view(new URLSearchParams(query)) : comingSoon(route);
      if (isValidElement(viewContent)) {
        render(viewContent, outlet);
      } else {
        mount(outlet, viewContent);
      }
      // Navigating to a page dismisses the chat drawer; the operator has moved on.
      toggleChat(false);
    });
  }

  window.addEventListener("hashchange", () => show(location.hash.slice(1) || "/"));
  window.addEventListener("keydown", (e) => {
    // A focused control (the composer dismissing its command menu, or the
    // topbar search) already handled Escape via preventDefault; closing the
    // drawer too would make the menu impossible to dismiss alone.
    if (e.defaultPrevented) return;
    if (e.key === "Escape" && chatDrawer.classList.contains("is-open")) toggleChat(false);
  });
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
