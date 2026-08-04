import "./tasks.css";
import { api } from "../base/api.js";
import { ago, el, empty, mount, pill, statusKind } from "../base/dom.js";
import { statTile } from "../base/statTile.js";

/**
 * Plain-language status labels. One place, so "waiting_human" only ever
 * reads as "Waiting for you" wherever a task list is shown.
 */
const STATUS_LABELS = {
  queued: "Queued",
  running: "Working",
  waiting_human: "Waiting for you",
  pr_open: "In review",
  merged: "Merged",
  parked: "Parked",
  dead: "Stopped (too many retries)",
  rejected: "Rejected",
  closed_wont_do: "Won't do",
};

function statusLabel(status) {
  return STATUS_LABELS[status] || status || "Unknown";
}

/**
 * The work board: every task archied knows about, filterable by status and
 * text, with a per-task event timeline available on click. Task rows carry
 * carries created_at/updated_at, so age is shown in the list and once
 * a row's timeline is loaded, sourced from its most recent event.
 */
export function tasksPage(params = new URLSearchParams()) {
  const root = el("div");
  const summaryRow = el("div.grid.grid-4.task-summary");
  const body = el("tbody");
  const searchInput = el("input.task-search", {
    type: "search",
    placeholder: "Search by title or repo…",
    oninput: (e) => {
      search = e.target.value.trim().toLowerCase();
      renderRows();
    },
  });
  const statusSelect = el(
    "select.task-filter",
    {
      onchange: (e) => {
        filterStatus = e.target.value;
        renderRows();
      },
    },
    el("option", { value: "" }, "All statuses"),
    ...Object.entries(STATUS_LABELS).map(([value, label]) => el("option", { value }, label)),
  );

  let tasks = [];
  let filterStatus = "";
  let search = "";
  const requestedTask = Number(params.get("task"));
  let expandedId = Number.isFinite(requestedTask) && requestedTask > 0 ? requestedTask : null;
  let forgeHost = "";
  const eventCache = new Map();
  // Keyed by task id. A single shared string leaked one row's failure into
  // every other row's action cell.
  const actionErrors = new Map();
  // Tasks with an action in flight, so the buttons can be disabled rather
  // than letting an impatient second click fire the same action twice.
  const actionsInFlight = new Set();

  render();
  load();

  function render() {
    mount(
      root,
      el(
        "div.page-head",
        el(
          "div",
          el("h1.page-title", "Tasks"),
          el("p.page-sub", "Every issue archied has picked up, and where it stands."),
        ),
        el("div.page-actions", el("button.btn", { onclick: load }, "Refresh")),
      ),
      summaryRow,
      el(
        "div.card",
        el(
          "div.card-head",
          el("div", el("h2.card-title", "All tasks"), el("p.card-sub", "Click a row for its timeline")),
          el("div.task-filters", searchInput, statusSelect),
        ),
        el(
          "div.table-scroll",
          el(
            "table.table",
            el(
              "thead",
              el(
                "tr",
                ...["Repo", "Issue", "Title", "Status", "Workflow", "Stage", "Last activity", "Action"].map((h) => el("th", h)),
              ),
            ),
            body,
          ),
        ),
      ),
    );
  }

  async function load() {
    try {
      [tasks, forgeHost] = await Promise.all([
        api.tasks(),
        api.config().then((cfg) => cfg?.identity?.forge_host || "").catch(() => ""),
      ]);
      renderSummary();
      renderRows();
      if (expandedId && tasks.some((task) => String(task.id) === String(expandedId))) {
        loadTimeline(expandedId);
      }
    } catch (err) {
      mount(summaryRow, el("div.card", empty("Cannot reach archied", String(err.message || err))));
      mount(body, el("tr", el("td", { colspan: 8 }, empty("Cannot reach archied", String(err.message || err)))));
    }
  }

  function renderSummary() {
    const total = tasks.length;
    const counts = {};
    for (const t of tasks) counts[t.status] = (counts[t.status] || 0) + 1;
    const working = counts.running || 0;
    const needsYou = (counts.waiting_human || 0) + (counts.parked || 0);
    const delivered = (counts.merged || 0) + (counts.pr_open || 0);

    mount(
      summaryRow,
      statTile({
        label: "Total tasks",
        value: total,
        compare: total ? "Everything archied has ever picked up" : "No tasks yet",
      }),
      statTile({
        label: "Working now",
        value: working,
        compare: total ? `${working} of ${total} in progress` : "Nothing running",
      }),
      statTile({
        label: "Needs you",
        value: needsYou,
        compare: needsYou ? "Parked or awaiting a reply" : "Nothing is blocked",
        goodDirection: "down",
      }),
      statTile({
        label: "Delivered",
        value: delivered,
        compare: total ? `${Math.round((delivered / total) * 100)}% of all tasks` : "No tasks yet",
      }),
    );
  }

  function filtered() {
    return tasks.filter((t) => {
      if (filterStatus && t.status !== filterStatus) return false;
      if (!search) return true;
      const hay = `${t.title} ${t.repo}`.toLowerCase();
      return hay.includes(search);
    });
  }

  function renderRows() {
    const rows = filtered();
    if (!tasks.length) {
      mount(
        body,
        el(
          "tr",
          el(
            "td",
            { colspan: 8 },
            empty("No tasks yet", "Tasks appear here once archied picks up an issue to work."),
          ),
        ),
      );
      return;
    }
    if (!rows.length) {
      mount(
        body,
        el("tr", el("td", { colspan: 8 }, empty("No matching tasks", "Try a different search or status filter."))),
      );
      return;
    }
    body.replaceChildren();
    for (const t of rows) {
      body.append(taskRow(t));
      if (expandedId === t.id) body.append(timelineRow(t));
    }
  }

  function taskRow(t) {
    return el(
      "tr.task-row",
      {
        onclick: () => {
          expandedId = expandedId === t.id ? null : t.id;
          renderRows();
          if (expandedId === t.id) loadTimeline(t.id);
        },
      },
      el("td.mono", repoLink(t) ?? `${t.owner}/${t.repo}`),
      el("td.mono", issueLink(t) ?? `#${t.issue_number}`),
      el("td.strong", t.title || "(untitled)"),
      el("td", pill(statusLabel(t.status), statusKind(t.status))),
      el("td", t.workflow || "—"),
      el("td", t.stage || "—"),
      // updated_at moves on every transition, so this reads as "how long has
      // it been sitting like this" rather than "how old is the task".
      el("td", { title: t.created_at ? `Created ${ago(t.created_at)}` : "" }, ago(t.updated_at)),
      el("td", taskActions(t)),
    );
  }

  function taskActions(t) {
    const message = actionErrors.get(t.id);
    const error = message ? el("span.task-action-error", message) : null;
    const busy = actionsInFlight.has(t.id);

    // The row itself toggles the timeline on click, so every button has to
    // stop the event or acting on a task also expands it.
    const button = (cls, action, label, confirmText) =>
      el(
        cls,
        {
          type: "button",
          disabled: busy,
          onclick: (e) => {
            e.stopPropagation();
            if (confirmText && !window.confirm(confirmText)) return;
            performAction(t.id, action);
          },
        },
        label,
      );

    if (t.status === "waiting_human") {
      return el(
        "div.task-actions",
        error,
        button("button.btn.btn-small", "approve", "Approve"),
        button(
          "button.btn.btn-small.btn-quiet",
          "reject",
          "Reject",
          `Reject "${t.title || "this task"}"? This closes the forge issue and cannot be undone from here.`,
        ),
      );
    }
    if (t.status === "parked") {
      return el("div.task-actions", error, button("button.btn.btn-small", "retry", "Retry"));
    }
    return error || "—";
  }

  async function performAction(id, action) {
    actionErrors.delete(id);
    actionsInFlight.add(id);
    renderRows();
    try {
      await api.taskAction(id, action);
      actionsInFlight.delete(id);
      await load();
    } catch (err) {
      actionsInFlight.delete(id);
      actionErrors.set(id, err.message || "Action failed");
      renderRows();
    }
  }

  // Both forge links derive from the same host so they can never drift apart:
  // one place builds the base, both anchors reuse it.
  function forgeBase(t) {
    if (!forgeHost || !t.owner || !t.repo) return null;
    return `${forgeHost.replace(/\/+$/, "")}/${encodeURIComponent(t.owner)}/${encodeURIComponent(t.repo)}`;
  }

  function repoLink(t) {
    const base = forgeBase(t);
    if (!base) return null;
    return el(
      "a.task-source-link",
      {
        href: base,
        target: "_blank",
        rel: "noreferrer",
        title: `Open ${t.owner}/${t.repo} on the forge`,
        onclick: (e) => e.stopPropagation(),
      },
      `${t.owner}/${t.repo}`,
    );
  }

  function issueLink(t) {
    const base = forgeBase(t);
    // A chat-sourced task has no issue on the forge, but its repo may still
    // exist -- the repo link stays, the issue link does not.
    if (t.source === "chat" || !base || !t.issue_number) return null;
    return el(
      "a.task-source-link",
      {
        href: `${base}/issues/${t.issue_number}`,
        target: "_blank",
        rel: "noreferrer",
        title: `Open issue #${t.issue_number} on the forge`,
        onclick: (e) => e.stopPropagation(),
      },
      `#${t.issue_number}`,
    );
  }

  function timelineRow(t) {
    const cached = eventCache.get(t.id);
    return el(
      "tr.task-timeline-row",
      el(
        "td",
        { colspan: 8 },
        t.status === "waiting_human" && t.plan
          ? el("div.task-decision", el("div.task-decision-title", "Decision required"), el("div.task-decision-plan", t.plan))
          : t.status === "parked" && t.park_reason
            ? el("div.task-decision", el("div.task-decision-title", "Park reason"), el("div.task-decision-plan", t.park_reason))
            : null,
        cached === undefined
          ? el("div.task-timeline-loading", "Loading timeline…")
          : cached === null
            ? el(
                "div.task-timeline-error",
                el("div.task-timeline-title", "Could not load timeline"),
                el(
                  "div.task-timeline-detail",
                  "archied did not answer for this task's events. It may be restarting — retry, or check the daemon.",
                ),
                el("button.btn", { onclick: () => loadTimeline(t.id) }, "Retry"),
              )
            : cached.length
              ? el("ul.task-timeline", ...cached.map(timelineEntry))
              : empty("No events yet", "This task has not recorded any transitions."),
      ),
    );
  }

  function timelineEntry(ev) {
    return el(
      "li.task-timeline-entry",
      el("span.task-timeline-dot"),
      el(
        "div",
        el("div.task-timeline-kind", ev.kind || ev.type || "event"),
        ev.detail && el("div.task-timeline-detail", ev.detail),
        el("div.task-timeline-when", ago(ev.at)),
      ),
    );
  }

  async function loadTimeline(id) {
    try {
      const events = await api.task(id);
      eventCache.set(id, events || []);
    } catch {
      eventCache.set(id, null);
    }
    renderRows();
  }

  return root;
}
