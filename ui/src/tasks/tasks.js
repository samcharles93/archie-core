import "./tasks.css";
import { api } from "../base/api.js";
import { ago, el, empty, mount, pill, statusKind } from "../base/dom.js";
import { statTile } from "../base/statTile.js";
import { taskRowA11y } from "./task-row.js";
import { initialTaskFilter, taskMatchesStatus } from "./task-filters.js";

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
  let tasks = [];
  let filterStatus = initialTaskFilter(params);
  let search = "";
  const requestedTask = Number(params.get("task"));
  let expandedId = Number.isFinite(requestedTask) && requestedTask > 0 ? requestedTask : null;
  let focusRequestedTask = expandedId !== null;
  let forgeHost = "";
  const eventCache = new Map();
  // Keyed by task id. A single shared string leaked one row's failure into
  // every other row's action cell.
  const actionErrors = new Map();
  // Tasks with an action in flight, so the buttons can be disabled rather
  // than letting an impatient second click fire the same action twice.
  const actionsInFlight = new Set();
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
    el("option", { value: "needs_you", selected: filterStatus === "needs_you" }, "Needs you"),
    ...Object.entries(STATUS_LABELS).map(([value, label]) =>
      el("option", { value, selected: filterStatus === value }, label)),
  );

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
      focusDirectTask();
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
      if (!taskMatchesStatus(t, filterStatus)) return false;
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
    focusDirectTask();
  }

  function focusDirectTask() {
    if (!focusRequestedTask || !expandedId) return;
    focusRequestedTask = false;
    requestAnimationFrame(() => {
      const row = document.getElementById(`task-row-${expandedId}`);
      if (!row) return;
      row.focus({ preventScroll: true });
      row.scrollIntoView({ block: "center", behavior: "smooth" });
    });
  }

  function taskRow(t) {
    const expanded = expandedId === t.id;
    const timelineID = `task-timeline-${t.id}`;
    const toggle = (event) => {
      event?.stopPropagation();
      expandedId = expandedId === t.id ? null : t.id;
      renderRows();
      if (expandedId === t.id) loadTimeline(t.id);
    };
    return el(
      "tr.task-row",
      {
        ...taskRowA11y(t, expanded),
        id: `task-row-${t.id}`,
        tabindex: "0",
        onkeydown: (event) => {
          if (event.key !== "Enter" && event.key !== " ") return;
          event.preventDefault();
          toggle(event);
        },
        onclick: toggle,
      },
      el(
        "td.mono",
        { "data-label": "Repository" },
        el(
          "button.task-expand",
          {
            type: "button",
            "aria-expanded": String(expanded),
            "aria-controls": timelineID,
            "aria-label": `${expanded ? "Collapse" : "Expand"} timeline for ${t.title || "untitled task"}`,
            onclick: toggle,
          },
          expanded ? "−" : "+",
        ),
        repoLink(t) ?? `${t.owner}/${t.repo}`,
      ),
      el("td.mono", { "data-label": "Issue" }, issueLink(t) ?? `#${t.issue_number}`),
      el("td.strong", { "data-label": "Title" }, t.title || "(untitled)"),
      el("td", { "data-label": "Status" }, pill(statusLabel(t.status), statusKind(t.status))),
      el("td", { "data-label": "Workflow" }, t.workflow || "—"),
      el("td", { "data-label": "Stage" }, t.stage || "—"),
      // updated_at moves on every transition, so this reads as "how long has
      // it been sitting like this" rather than "how old is the task".
      el(
        "td",
        { "data-label": "Last activity", title: t.created_at ? `Created ${ago(t.created_at)}` : "" },
        ago(t.updated_at),
      ),
      el("td", { "data-label": "Actions" }, taskActions(t)),
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

    const controls = (t.actions || []).map((action) => {
      switch (action) {
        case "cancel":
          return button(
            "button.btn.btn-small.btn-quiet",
            action,
            "Cancel",
            `Cancel "${t.title || "this task"}"? This closes the forge issue.`,
          );
        case "stop":
          return button(
            "button.btn.btn-small",
            action,
            "Stop",
            `Stop "${t.title || "this task"}"? Recoverable work will remain parked.`,
          );
        case "approve":
          return button("button.btn.btn-small", action, "Approve");
        case "reject":
          return button(
            "button.btn.btn-small.btn-quiet",
            action,
            "Reject",
            `Reject "${t.title || "this task"}"? This closes the forge issue.`,
          );
        case "retry":
          return button("button.btn.btn-small", action, "Retry");
        case "abandon":
          return button(
            "button.btn.btn-small.btn-quiet",
            action,
            "Abandon",
            `Abandon "${t.title || "this task"}"? This closes the forge issue.`,
          );
        case "archive":
          return button(
            "button.btn.btn-small.btn-quiet",
            action,
            "Archive",
            `Archive the local record for "${t.title || "this task"}"?`,
          );
        case "open_pr":
          return taskLink(t, "pulls", t.pr_number, "Open PR");
        case "open_issue":
          return taskLink(t, "issues", t.issue_number, "Open issue");
        default:
          return null;
      }
    });
    if (!controls.length && !error) return "—";
    return el("div.task-actions", error, controls);
  }

  async function performAction(id, action) {
    actionErrors.delete(id);
    actionsInFlight.add(id);
    renderRows();
    try {
      await api.taskAction(id, action);
      actionsInFlight.delete(id);
      if (action === "archive") expandedId = null;
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

  function taskLink(t, kind, number, label) {
    const base = forgeBase(t);
    if (!base || !number || (kind === "issues" && t.source === "chat")) {
      return el(
        "button.btn.btn-small",
        { type: "button", disabled: true, title: `${label} is unavailable` },
        label,
      );
    }
    return el(
      "a.btn.btn-small",
      {
        href: `${base}/${kind}/${number}`,
        target: "_blank",
        rel: "noreferrer",
        onclick: (event) => event.stopPropagation(),
      },
      label,
    );
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
      { id: `task-timeline-${t.id}` },
      el(
        "td",
        { colspan: 8 },
        el(
          "div.task-detail-actions",
          el("div.task-decision-title", "Available actions"),
          taskActions(t),
        ),
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
