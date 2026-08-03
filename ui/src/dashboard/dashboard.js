import "./dashboard.css";
import { api, subscribeEvents } from "../base/api.js";
import { ago, compact, el, empty, mount, pill, statusKind } from "../base/dom.js";
import { statTile } from "../base/statTile.js";
import { gauge, segmentBar } from "../base/gauge.js";
import { icon } from "../base/icons.js";

/**
 * The control room. Answers, in order: is anything wrong, what is Archie doing
 * right now, and what have I not finished setting up.
 *
 * Every panel degrades to an explanatory empty state rather than a blank box,
 * because a fresh install has no tasks and a dashboard of zeroes teaches
 * nobody anything.
 */
export function dashboardPage() {
  const root = el("div");
  const runs = el("tbody");
  const statsRow = el("div.grid.grid-4");
  const setupSlot = el("div");
  const pulseSlot = el("div");
  const forecastSlot = el("div");
  const actions = el("div.hero-actions-inner");
  const title = el("h1.hero-title", greeting());
  const streamState = pill("connecting", "idle");

  render();
  load();

  const unsubscribe = subscribeEvents(
    (event) => prependRun(runs, event),
    (state) => {
      const kind = state === "live" ? "ok" : "warn";
      streamState.className = `pill pill-${kind}`;
      streamState.textContent = state;
    },
  );
  root.addEventListener("archie:teardown", unsubscribe);

  function render() {
    mount(
      root,
      el(
        "div.hero",
        el(
          "div",
          title,
          el(
            "p.hero-sub",
            "Your agent's control room \u2014 what it is working on, what needs you, and what it is spending.",
          ),
        ),
        el("div.hero-actions", actions),
      ),
      el(
        "div.dash-section",
        el("div.dash-section-head", el("h2.dash-section-title", "Health")),
        el("div.dash-top", setupSlot, pulseSlot),
      ),
      el(
        "div.dash-section",
        el(
          "div.dash-section-head",
          el("h2.dash-section-title", "Throughput"),
          el("span.dash-section-note", "Across all repositories"),
        ),
        statsRow,
      ),
      el(
        "div.dash-section",
        el(
          "div.dash-section-head",
          el("h2.dash-section-title", "Right now"),
        ),
        el(
        "div.dash-split",
        forecastSlot,
        el(
          "div.card",
          el(
            "div.card-head",
            el("div", el("h2.card-title", "Live activity"), el("p.card-sub", "Last 50, newest first")),
            streamState,
          ),
          el(
            "table.table",
            el("thead", el("tr", ...["Event", "Task", "Detail", "When"].map((h) => el("th", h)))),
            runs,
          ),
        ),
        ),
      ),
    );
    mount(runs, el("tr", el("td", { colspan: 4 }, empty("Waiting for activity", "Events appear here as Archie works."))));
  }

  async function load() {
    try {
      const [summary, setup, workflows] = await Promise.all([
        api.summary(),
        api.setup().catch(() => null),
        api.workflows().catch(() => null),
      ]);
      renderActions(summary);
      // The greeting is only personalised when the deployment says who it
      // assists; an unconfigured instance greets without a name rather than
      // inventing one.
      title.textContent = setup?.operator ? `${greeting()}, ${setup.operator}` : greeting();
      renderStats(summary);
      renderSetup(setup);
      renderPulse(summary, workflows);
      renderForecast(summary);
    } catch (err) {
      mount(statsRow, el("div.card", empty("Cannot reach archied", String(err.message || err))));
    }
  }

  // Actions follow state: what needs you comes first, and the generic ones
  // only fill the space when nothing does. A row of buttons that is identical
  // whatever is happening teaches nobody anything.
  function renderActions(summary) {
    const counts = summary?.statuses || {};
    const blocked = (counts.parked || 0) + (counts.waiting_human || 0);
    const running = counts.running || 0;

    mount(
      actions,
      blocked > 0 &&
        el(
          "a.btn.btn-attention",
          { href: "#/tasks" },
          icon("tasks", { size: 15 }),
          `${blocked} need${blocked === 1 ? "s" : ""} you`,
        ),
      running > 0 &&
        el("a.btn", { href: "#/tasks" }, icon("workflows", { size: 15 }), `${running} running`),
      el("a.btn", { href: "#/logs" }, icon("logs", { size: 15 }), "Logs"),
      el("button.btn.btn-primary", { onclick: load }, icon("refresh", { size: 15 }), "Refresh"),
    );
  }

  function renderStats(summary) {
    const counts = summary?.statuses || {};
    const done = (counts.merged || 0) + (counts.pr_open || 0);
    const attention = (counts.parked || 0) + (counts.waiting_human || 0);
    const total = Object.values(counts).reduce((a, b) => a + b, 0);
    const tokens = summary?.tokens_by_day || [];

    mount(
      statsRow,
      statTile({
        label: "Working now",
        value: counts.running || 0,
        compare: `${counts.queued || 0} waiting to start`,
        series: tokens.map((d) => d.tokens || 0),
      }),
      statTile({
        label: "Needs you",
        value: attention,
        compare: attention ? "Parked or awaiting a reply" : "Nothing is blocked",
        goodDirection: "down",
      }),
      statTile({
        label: "Delivered",
        value: done,
        compare: total ? `${Math.round((done / total) * 100)}% of all tasks` : "No tasks yet",
      }),
      statTile({
        label: "Tokens used",
        value: compact(tokens.reduce((a, d) => a + (d.tokens || 0), 0)),
        compare: `Across ${tokens.length || 0} days`,
        trend: trendPct(tokens.map((d) => d.tokens || 0)),
        goodDirection: "down",
        series: tokens.map((d) => d.tokens || 0),
      }),
    );
  }

  function renderSetup(setup) {
    if (!setup?.steps?.length) return mount(setupSlot);
    const remaining = setup.steps.filter((s) => !s.done);
    if (!remaining.length) return mount(setupSlot);

    const pct = Math.round(((setup.steps.length - remaining.length) / setup.steps.length) * 100);
    mount(
      setupSlot,
      el(
        "div.card.setup",
        el(
          "div.card-head",
          el(
            "div",
            el("h2.card-title", "Finish setting up"),
            el("p.card-sub", "Archie needs these before it can work on its own."),
          ),
          el("span.setup-pct", `${pct}%`),
        ),
        el("div.setup-bar", el("div.setup-bar-fill", { style: `width:${pct}%` })),
        el("ul.setup-list", ...setup.steps.map(setupStep)),
      ),
    );
  }

  function renderPulse(summary, workflows) {
    renderPulseInto(pulseSlot, summary, workflows);
  }

  function renderForecast(summary) {
    renderForecastInto(forecastSlot, summary);
  }

  function setupStep(step) {
    return el(
      `li.setup-step${step.done ? ".done" : ""}`,
      el("span.setup-check", step.done ? icon("check", { size: 12 }) : ""),
      el("div", el("div.setup-step-title", step.title), step.detail && el("div.setup-step-detail", step.detail)),
    );
  }

  return root;
}

function prependRun(tbody, event) {
  if (tbody.firstElementChild?.querySelector(".empty")) tbody.replaceChildren();
  const row = el(
    "tr",
    el("td.strong", el("span", event.kind || event.type || "event")),
    el("td.mono", event.task_id ? `#${event.task_id}` : "—"),
    el("td", event.detail || event.message || ""),
    el("td", ago(event.at || Date.now())),
  );
  tbody.prepend(row);
  while (tbody.children.length > 50) tbody.lastElementChild.remove();
}

function greeting() {
  const h = new Date().getHours();
  if (h < 5) return "Still up?";
  if (h < 12) return "Good morning";
  if (h < 18) return "Good afternoon";
  return "Good evening";
}


/**
 * Gate pulse: the share of finished work that got through the quality gates.
 *
 * This is the dashboard's headline health number -- Archie's whole promise is
 * that it does not open a PR until the gates pass, so a falling pass rate is
 * the first thing worth seeing.
 */
function renderPulseInto(slot, summary, workflows) {
  const stats = workflows?.workflows || [];
  const runs = stats.reduce((a, w) => a + (w.runs || 0), 0);
  const merged = stats.reduce((a, w) => a + (w.merged || 0) + (w.pr_open || 0), 0);
  const parked = stats.reduce((a, w) => a + (w.parked || 0), 0);

  if (!runs) {
    mount(
      slot,
      el(
        "div.card",
        el("div.card-head", el("h2.card-title", "Gate pulse")),
        empty("No completed runs yet", "Once Archie finishes a task, its gate pass rate appears here."),
      ),
    );
    return;
  }

  const pct = (merged / runs) * 100;
  mount(
    slot,
    el(
      "div.card",
      el(
        "div.card-head",
        el(
          "div",
          el("h2.card-title", "Gate pulse"),
          el("p.card-sub", "Work that passed its quality gates"),
        ),
      ),
      gauge({ value: pct, label: "pass rate" }),
      el(
        "ul.pulse-list",
        ...stats.slice(0, 3).map((w) =>
          el(
            "li.pulse-item",
            el("span.pulse-name", w.workflow || "workflow"),
            pill(
              `${w.merged || 0}/${w.runs || 0}`,
              (w.merged || 0) === (w.runs || 0) ? "ok" : (w.parked || 0) > 0 ? "warn" : "info",
            ),
          ),
        ),
        parked > 0 &&
          el("li.pulse-item", el("span.pulse-name", "Parked, awaiting you"), pill(String(parked), "warn")),
      ),
    ),
  );
}

/**
 * Budget outlook: what this month's token spend is tracking toward, and how
 * much of the configured budget it accounts for.
 */
function renderForecastInto(slot, summary) {
  const days = summary?.tokens_by_day || [];
  const used = days.reduce((a, d) => a + (d.tokens || 0), 0);

  if (!days.length) {
    mount(
      slot,
      el(
        "div.card",
        el("div.card-head", el("h2.card-title", "Token outlook")),
        empty("Nothing spent yet", "Usage appears here once Archie runs its first task."),
      ),
    );
    return;
  }

  const perDay = used / days.length;
  const projected = Math.round(perDay * 30);
  const recent = days.slice(-7).reduce((a, d) => a + (d.tokens || 0), 0);

  mount(
    slot,
    el(
      "div.card",
      el(
        "div.card-head",
        el("div", el("h2.card-title", "Token outlook"), el("p.card-sub", "Next 30 days at the current rate")),
      ),
      el("div.forecast-value", compact(projected)),
      el("div.forecast-note", `Based on ${compact(Math.round(perDay))} per day over ${days.length} days`),
      segmentBar([
        { label: `Last 7 days (${compact(recent)})`, value: recent, kind: "info" },
        { label: `Earlier (${compact(used - recent)})`, value: Math.max(used - recent, 0), kind: "idle" },
      ]),
    ),
  );
}

/** Percentage change between the first and second half of a series. */
function trendPct(series) {
  if (series.length < 4) return null;
  const mid = Math.floor(series.length / 2);
  const older = series.slice(0, mid).reduce((a, b) => a + b, 0);
  const newer = series.slice(mid).reduce((a, b) => a + b, 0);
  if (!older) return null;
  return ((newer - older) / older) * 100;
}
