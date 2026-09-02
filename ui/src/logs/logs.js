import "./logs.css";
import { api, subscribeLogs } from "../base/api.js";
import { el, empty, mount, pill } from "../base/dom.js";
import { logRow } from "../base/log-row.js";

/**
 * The log view: history from the file on disk, then live lines appended from
 * the event stream.
 *
 * Filtering is applied server-side for history (the file is far larger than a
 * page of it) and client-side for live lines (they are already in hand). Both
 * paths go through the same matcher shape so a filter means the same thing
 * either side of the join.
 */

const LEVELS = [
  { value: "", label: "All levels" },
  { value: "ERROR", label: "Errors" },
  { value: "WARN,ERROR", label: "Warnings and errors" },
  { value: "INFO,WARN,ERROR", label: "Info and above" },
  { value: "DEBUG", label: "Debug only" },
];

export function logsPage() {
  const root = el("div");
  const list = el("div.log-list");
  const streamState = pill("connecting", "idle");
  const meta = el("span.log-meta");

  let filters = { level: "", component: "", q: "" };
  let paused = false;
  let componentOptions = [];
	let historyEntries = [];
	const liveEntries = new Map();
	let durableUnavailable = false;

  const levelSelect = el(
    "select.log-select",
    { onchange: (e) => set({ level: e.target.value }) },
    ...LEVELS.map((l) => el("option", { value: l.value }, l.label)),
  );
  const componentSelect = el("select.log-select", {
    onchange: (e) => set({ component: e.target.value }),
  });
  const search = el("input.log-search", {
    type: "search",
    placeholder: "Search messages and fields…",
    oninput: debounce((e) => set({ q: e.target.value }), 250),
  });
  const pauseBtn = el("button.btn", { onclick: togglePause }, "Pause");

  render();

  const unsubscribe = subscribeLogs(
    (event) => appendLive(event),
    (state) => {
      streamState.className = `pill pill-${state === "live" ? "ok" : "warn"}`;
      streamState.textContent = state;
    },
  );
  root.addEventListener("archie:teardown", unsubscribe);
  load();

  function set(patch) {
    filters = { ...filters, ...patch };
    load();
  }

  function togglePause() {
    paused = !paused;
    pauseBtn.textContent = paused ? "Resume" : "Pause";
    pauseBtn.classList.toggle("btn-primary", paused);
  }

  function render() {
    mount(
      root,
      el(
        "div.page-head",
        el(
          "div",
          el("h1.page-title", "Logs"),
          el("p.page-sub", "What Archie has been doing, newest last."),
        ),
        el("div.page-actions", pauseBtn, el("button.btn", { onclick: load }, "Refresh")),
      ),
      el(
        "div.card",
        el(
          "div.card-head",
          el("div.log-filters", levelSelect, componentSelect, search),
          el("div.log-status", meta, streamState),
        ),
        list,
      ),
    );
  }

  async function load() {
		meta.textContent = "Refreshing…";
    try {
      const res = await api.logs({
        level: filters.level,
        component: filters.component,
        q: filters.q,
        limit: 500,
      });

      syncComponents(res.components || []);
      durableUnavailable = !!res.disabled;
      meta.textContent = res.disabled ? "Durable history unavailable; live daemon logs continue." : res.truncated
        ? `showing the most recent matches from ${res.file}`
        : res.file || "";
		historyEntries = res.entries || [];
		renderEntries();
    } catch (err) {
      mount(list, empty("Cannot read logs", String(err.message || err)));
    }
  }

  // Rebuild the component filter from what the log actually contains, so the
  // options never drift from reality.
  function syncComponents(components) {
    if (sameList(components, componentOptions)) return;
    componentOptions = components;
    const current = filters.component;
    mount(
      componentSelect,
      el("option", { value: "" }, "All components"),
      ...components.map((c) => el("option", { value: c }, c)),
    );
    componentSelect.value = current;
  }

  function appendLive(event) {
    if (paused) return;
		const entry = event;
    if (!matchesLocally(entry)) return;
		liveEntries.set(entryKey(entry), entry);
		if (entry.fields?.component) syncComponents([...componentOptions, entry.fields.component]);
		renderEntries();
  }

	function renderEntries() {
		const byID = new Map();
		for (const entry of historyEntries) byID.set(entryKey(entry), entry);
		for (const [id, entry] of liveEntries) byID.set(id, entry);
		const entries = [...byID.values()].filter(matchesLocally).slice(-1000);
		if (!entries.length) {
			mount(list, empty(durableUnavailable ? "Durable history unavailable" : "Nothing matches", durableUnavailable ? "Live daemon logs will appear here while this page is open." : "Try a wider level or clear the search."));
			return;
		}
		mount(list, ...entries.map(logRow));
		list.scrollTop = list.scrollHeight;
	}

  function matchesLocally(entry) {
    if (filters.level && !filters.level.split(",").includes((entry.level || "").toUpperCase())) {
      return false;
    }
    if (filters.component && entry.fields?.component !== filters.component) return false;
    if (filters.q) {
      const needle = filters.q.toLowerCase();
      const hay = `${entry.message || entry.msg || ""} ${JSON.stringify(entry.fields || {})}`.toLowerCase();
      if (!hay.includes(needle)) return false;
    }
    return true;
  }

  return root;
}

function entryKey(entry) {
	return `${entry.time || ""}|${entry.level || ""}|${entry.message || entry.msg || ""}|${JSON.stringify(entry.fields || {})}`;
}

function sameList(a, b) {
  return a.length === b.length && a.every((v, i) => v === b[i]);
}

function debounce(fn, ms) {
  let t;
  return (...args) => {
    clearTimeout(t);
    t = setTimeout(() => fn(...args), ms);
  };
}
