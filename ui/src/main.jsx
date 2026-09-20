import "./css/_main.css";
import { Fragment, h, render } from "preact";
import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import { api } from "./base/api.jsx";
import { hiddenRoutes } from "./capabilities.jsx";
import { loadTaskMeta } from "./base/task-meta.jsx";
import { Icon } from "./base/icons.jsx";
import { dashboardPage } from "./dashboard/dashboard.jsx";
import { tasksPage } from "./tasks/tasks.jsx";
import { taskDetailPage } from "./tasks/task-detail.jsx";
import { skillsPage } from "./skills/skills.jsx";
import { workflowsPage } from "./workflows/workflows.jsx";
import { channelsPage } from "./channels/channels.jsx";
import { curatorsPage } from "./curators/curators.jsx";
import { settingsPage } from "./settings/settings.jsx";
import { logsPage } from "./logs/logs.jsx";
import { capturesPage } from "./captures/captures.jsx";
import { mappingsPage } from "./mappings/mappings.jsx";
import { bindingsPage } from "./bindings/bindings.jsx";
import { ChatApp } from "./chat/chat.jsx";
import { matchRoute, navPath } from "./routing.jsx";

/**
 * Routes are declared once here. Adding a section means adding a feature
 * folder and one entry -- there is no switch statement to hunt for.
 *
 * `soon: true` sections are deliberately visible before they are built: a
 * greyed, labelled entry tells you the capability exists and is coming, which
 * is more honest than hiding it and more useful than a 404.
 */
// section names the capability a route needs, as reported by
// GET /api/capabilities. A route with no section is served in every
// composition. The dashboard is rendered by two different processes -- the
// daemon and the extracted UI service -- and a section the serving process
// cannot back would otherwise render as permanently empty rather than as
// absent (archie-core-8cda.5.4).
//
// A route may also carry:
//   nav:false -- addressed by URL but not a navigation entry (a detail page
//                reached from its section)
//   navPath  -- the navigation entry this route keeps current; a parameterised
//                route is not itself in the nav, so without this the section it
//                belongs to would lose its aria-current highlight
//   :name    -- one captured path segment, e.g. /tasks/:id
const routes = [
  { path: "/", label: "Dashboard", icon: "dashboard", view: dashboardPage },
  { path: "/tasks", label: "Tasks", icon: "tasks", view: tasksPage },
  { path: "/tasks/:id", label: "Task run", view: taskDetailPage, nav: false, navPath: "/tasks" },
  { path: "/logs", label: "Logs", icon: "logs", view: logsPage, section: "logs" },
  { path: "/captures", label: "Event inspector", icon: "captures", view: capturesPage, section: "captures" },
  { path: "/mappings", label: "Field mappings", icon: "mappings", view: mappingsPage, section: "mappings" },
  { path: "/bindings", label: "Playbook bindings", icon: "bindings", view: bindingsPage, section: "bindings" },
  { path: "/skills", label: "Skills", icon: "skills", view: skillsPage, section: "skills" },
  { path: "/workflows", label: "Workflows", icon: "workflows", view: workflowsPage, section: "workflows" },
  { path: "/curators", label: "Curators", icon: "curators", view: curatorsPage, section: "curators" },
  { path: "/channels", label: "Channels", icon: "channels", view: channelsPage, section: "channels" },
  { path: "/settings", label: "Configuration", icon: "settings", view: settingsPage, section: "settings" },
];

const THEME_KEY = "archie.theme";

function currentTheme() {
  return localStorage.getItem(THEME_KEY) || "dark";
}

function applyTheme(theme) {
  document.documentElement.dataset.theme = theme;
  localStorage.setItem(THEME_KEY, theme);
}

// navEntries lists what gets a navigation item: a detail route (nav:false) is
// addressed by URL but reached from its section, so it is not an entry.
function navEntries(hidden) {
  return routes.filter((route) => route.nav !== false && !hidden.includes(route.path));
}

function Topbar({ activePath, hidden, onNavigate, theme, onToggleTheme }) {
  const [searchOpen, setSearchOpen] = useState(false);
  const searchRef = useRef(null);

  const closeSearch = () => setSearchOpen(false);

  const onSearchKeyDown = (event) => {
    if (event.key === "Escape") {
      // preventDefault so the window-level handler (chat drawer close) does not
      // also fire: dismissing the search should not dismiss the drawer.
      event.preventDefault();
      closeSearch();
      event.target.blur();
      return;
    }
    if (event.key !== "Enter") return;
    // Search jumps between sections. It is deliberately not a data search: the
    // sections own their own filtering, and a second search that means
    // something different in each place is worse than none.
    const query = event.target.value.trim().toLowerCase();
    const hit = routes.find(
      (route) => route.nav !== false && !route.soon && route.label.toLowerCase().startsWith(query),
    );
    if (hit) {
      onNavigate(hit.path);
      event.target.value = "";
      event.target.blur();
      closeSearch();
    }
  };

  return (
    <header className="topbar">
      <div className="brand">
        <span className="brand-mark">A</span>
        Archie
      </div>
      <nav className="nav">
        {navEntries(hidden).map((route) => (
          <a
            className="nav-item"
            key={route.path}
            href={`#${route.path}`}
            aria-current={route.path === activePath ? "page" : undefined}
            aria-disabled={route.soon || undefined}
            title={route.soon ? "Coming soon" : undefined}
            onClick={(event) => {
              if (route.soon) event.preventDefault();
              else onNavigate(route.path);
            }}
          >
            <Icon name={route.icon} />
            <span className="nav-label">{route.label}</span>
            {route.soon ? <span className="nav-soon">soon</span> : null}
          </a>
        ))}
      </nav>
      <div className="topbar-end">
        <div className={`topbar-search${searchOpen ? " is-open" : ""}`}>
          <span className="topbar-search-icon" aria-hidden="true">
            <Icon name="search" size={15} />
          </span>
          <button
            className="icon-btn mobile-search-toggle"
            type="button"
            aria-label="Open Jump to navigation"
            aria-expanded={searchOpen ? "true" : "false"}
            onClick={() => {
              setSearchOpen(true);
              // The field is always mounted -- the narrow layout only hides it --
              // so focus can land in it straight away instead of on the toggle.
              searchRef.current?.focus();
            }}
          >
            <Icon name="search" size={15} />
          </button>
          <input
            ref={searchRef}
            type="search"
            placeholder="Jump to…"
            aria-label="Jump to a section"
            onKeyDown={onSearchKeyDown}
          />
        </div>
        <button className="icon-btn" title="Documentation" aria-label="Documentation">
          <Icon name="help" />
        </button>
        <button
          className="icon-btn"
          title="Toggle light and dark"
          aria-label="Toggle light and dark"
          onClick={onToggleTheme}
        >
          <Icon name={theme === "dark" ? "moon" : "sun"} />
        </button>
        <div className="avatar" title="Signed in locally">
          A
        </div>
      </div>
    </header>
  );
}

// Chat is a floating launcher and the panel it opens, mounted once beside the
// outlet. It is not a route: the operator asks Archie about the page they are
// already on, so navigating must not close it and opening must not replace the
// work that prompted the question.
//
// The panel anchors to the launcher and opens leftward from it, sharing its
// bottom edge, so the button stays put and reads as the thing the panel came
// out of. Nothing is dimmed and the page keeps scrolling: this is a companion
// to the work, not a modal over it.
//
// It hosts a single ChatApp instance, so session state and the stream survive
// navigation rather than being rebuilt per page.
const CHAT_PANEL_ID = "chat-panel";

function ChatLauncher() {
  const [open, setOpen] = useState(false);
  const fabRef = useRef(null);

  const close = useCallback(() => {
    setOpen(false);
    // Focus goes back to the control that opened the panel; otherwise a
    // keyboard user who dismisses it lands at the top of the document.
    fabRef.current?.focus();
  }, []);

  useEffect(() => {
    if (!open) return undefined;
    const onKeyDown = (event) => {
      // A focused control (the composer dismissing its command menu, or the
      // topbar search) already handled Escape via preventDefault; closing the
      // panel too would make that menu impossible to dismiss on its own.
      if (event.defaultPrevented || event.key !== "Escape") return;
      close();
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [open, close]);

  return (
    <div className={`chat-dock${open ? " is-open" : ""}`}>
      <aside
        className={`chat-drawer${open ? " is-open" : ""}`}
        id={CHAT_PANEL_ID}
        aria-label="Chat with Archie"
        aria-hidden={open ? undefined : "true"}
      >
        <div className="chat-drawer-panel">
          <div className="chat-drawer-head">
            <strong>Archie</strong>
            <button className="icon-btn chat-drawer-close" aria-label="Close chat" title="Close chat" onClick={close}>
              <Icon name="close" />
            </button>
          </div>
          <ChatApp />
        </div>
      </aside>
      <button
        ref={fabRef}
        type="button"
        className="chat-fab"
        aria-label={open ? "Close chat" : "Chat with Archie"}
        title={open ? "Close chat" : "Chat with Archie"}
        aria-expanded={open ? "true" : "false"}
        aria-controls={CHAT_PANEL_ID}
        onClick={() => (open ? close() : setOpen(true))}
      >
        <span className="chat-fab-icon chat-fab-icon-open" aria-hidden="true">
          <Icon name="chat" size={22} />
        </span>
        <span className="chat-fab-icon chat-fab-icon-close" aria-hidden="true">
          <Icon name="close" size={22} />
        </span>
      </button>
    </div>
  );
}

function ComingSoon({ label }) {
  return (
    <div>
      <div className="page-head">
        <div>
          <h1 className="page-title">{label}</h1>
        </div>
      </div>
      <div className="card">
        <div className="empty">
          <div className="empty-title">{`${label} is not built yet`}</div>
          <div>This section is next up. Nothing is broken.</div>
        </div>
      </div>
    </div>
  );
}

// RouteView gives the router a keyed boundary. A page is a function of its
// query and its path parameters, and two ids on the same parameterised route
// must be two instances: without the key the second id diffs the first one in
// place and the operator's open tab from the previous task stays open on the
// next (W11).
function RouteView({ view, search, params }) {
  return view(search, params);
}

// Routing is wired once, at import, and pages render into one long-lived outlet
// rather than as children of App.
//
// The listeners cannot live in an effect: a page installs its own
// hash-sensitive state as it mounts, and the outgoing page has to be gone in
// the same tick the hash changes. Task detail writes the tab selection back
// with replaceState as it settles; if it is still mounted when the hashchange
// lands, it canonicalises the URL back to its own route and the navigation is
// silently lost. App publishes its outlet and its two navigation-owned pieces
// of chrome through a callback ref, which runs at commit time -- before any
// page effect can run.
let outlet = null;
let chrome = null;

// renderRoute mounts the route's page into the outlet. It is separate from
// show() because the lifecycle catalog arrives after the first paint and the
// route tree renders into its own root: App re-rendering cannot reach into the
// outlet, so a late catalog has to re-render the page explicitly or it keeps
// the freeze-dried labels it read on the way up. The key is unchanged, so Preact
// re-renders the mounted page in place and keeps its state.
function renderRoute(rawPath) {
  if (!outlet) return;
  const [path, query = ""] = String(rawPath).split("?", 2);
  const match = matchRoute(routes, path);
  const route = match?.route || routes[0];
  const params = match?.params || {};

  // The key covers the path and its parameters, but NOT the query: the query
  // string is an entry state, so a navigation that only changes the query of
  // the page already mounted keeps the operator's own filter selection.
  render(
    route.view ? (
      <RouteView
        key={`${route.path}|${Object.values(params).join("/")}`}
        view={route.view}
        search={new URLSearchParams(query)}
        params={params}
      />
    ) : (
      <ComingSoon label={route.label} />
    ),
    outlet,
  );
}

function show(rawPath) {
  const [path] = String(rawPath).split("?", 2);
  const match = matchRoute(routes, path);
  const route = match?.route || routes[0];

  // Let a page release its subscriptions before it is replaced, so the SSE
  // stream does not leak a connection per navigation. The event goes to the
  // outgoing page's root, before the swap.
  outlet?.firstElementChild?.dispatchEvent(new CustomEvent("archie:teardown"));
  // A detail route highlights the section it belongs to, so the nav item for
  // Tasks stays current on #/tasks/42 instead of every item losing it.
  chrome?.setActive(navPath(route));

  renderRoute(rawPath);
}

function navigate(next) {
  if (location.hash !== `#${next}`) location.hash = next;
  else show(next);
}

window.addEventListener("hashchange", () => show(location.hash.slice(1) || "/"));
function App() {
  const [active, setActive] = useState("/");
  const [theme, setTheme] = useState(currentTheme);
  const [hidden, setHidden] = useState([]);

  // Publishes the router's handles. A callback ref runs at commit time, so a
  // hashchange that lands right after the first paint already finds them.
  const publish = useCallback((element) => {
    outlet = element;
    chrome = { setActive };
  }, []);

  useEffect(() => {
    applyTheme(theme);
  }, [theme]);

  // Asked for once, after the shell is up: the nav renders immediately and
  // loses the entries this process cannot back a moment later, rather than
  // holding the whole page behind one request.
  useEffect(() => {
    api
      .capabilities()
      .then((caps) => setHidden(hiddenRoutes(caps?.sections, routes)))
      .catch(() => {});
  }, []);

  // The lifecycle vocabulary -- status labels, pill severity, "needs you"
  // grouping, and the operator actions -- is served by /api/task-meta. Ask for
  // it once, after the shell is up: the freeze-dried defaults paint
  // immediately and this replaces them, so a catalogue change on the server
  // reaches the browser without a UI release. loadTaskMeta never throws (a
  // failed fetch keeps the defaults), so it needs no catch here.
  useEffect(() => {
    // The catalog lands after the first paint, and the route tree renders into
    // its own root: without re-rendering it, an already-painted page keeps the
    // freeze-dried labels it read on the way up until something else happens to
    // re-render it.
    loadTaskMeta().then(() => renderRoute(location.hash.slice(1) || "/"));
  }, []);

  return (
    <Fragment>
      <div className="shell">
        <Topbar
          activePath={active}
          hidden={hidden}
          onNavigate={navigate}
          theme={theme}
          onToggleTheme={() => setTheme((current) => (current === "dark" ? "light" : "dark"))}
        />
        <main className="main" ref={publish} />
      </div>
      {/* Outside .shell on purpose: its backdrop-filter is a containing block
          for fixed positioning, so a launcher nested inside would anchor to the
          scrolling shell and ride off the bottom of a long page. */}
      <ChatLauncher />
    </Fragment>
  );
}

applyTheme(currentTheme());
render(<App />, document.getElementById("app"));
show(location.hash.slice(1) || "/");
