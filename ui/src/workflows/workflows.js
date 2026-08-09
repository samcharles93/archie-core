import "./workflows.css";
import { api } from "../base/api.js";
import { el, empty, mount } from "../base/dom.js";

/**
 * Plain-language workflow names. Falls back to a title-cased version of the
 * raw identifier for anything not listed here, so a new workflow never
 * renders blank.
 */
const WORKFLOW_LABELS = {
  bootstrap: "Bootstrap",
  implement: "Implement",
  tdd: "TDD",
  feasibility: "Feasibility check",
};

function workflowLabel(name) {
  if (WORKFLOW_LABELS[name]) return WORKFLOW_LABELS[name];
  if (!name) return "Unknown";
  return name.charAt(0).toUpperCase() + name.slice(1).replace(/_/g, " ");
}

function pct(part, whole) {
  if (!whole) return 0;
  return Math.round((part / whole) * 100);
}

function bar(fraction, kind = "ok") {
  return el(
    "div.wf-bar",
    el(`div.wf-bar-fill.wf-bar-${kind}`, { style: `width:${Math.min(100, Math.max(0, fraction))}%` }),
  );
}

function formatMs(ms) {
  if (!ms) return "—";
  if (ms < 1000) return `${ms} ms`;
  const secs = ms / 1000;
  if (secs < 60) return `${secs.toFixed(1)} s`;
  return `${(secs / 60).toFixed(1)} min`;
}

/**
 * How work actually flows: per-workflow outcomes and spend, then per-stage
 * duration and failure counts -- the "where does it get stuck" view. Both
 * come from a single /api/workflows call built on the same store queries
 * that back the dashboard's summary stats.
 */
export function workflowsPage() {
  const root = el("div");
  const workflowSlot = el("div");
  const stageSlot = el("div");
	const startSlot = el("div");

  render();
  load();

  function render() {
    mount(
      root,
      el(
        "div.page-head",
        el(
          "div",
          el("h1.page-title", "Workflows"),
          el("p.page-sub", "Run outcomes and spend per workflow, and where stages get stuck."),
        ),
        el("div.page-actions", el("button.btn", { onclick: load }, "Refresh")),
      ),
      startSlot,
      workflowSlot,
      stageSlot,
    );
  }

  async function load() {
    try {
      const data = await api.workflows();
      renderStartWork(data?.definitions || []);
      renderWorkflows(data?.workflows || [], data?.definitions || []);
      renderStages(data?.stages || []);
    } catch (err) {
      mount(workflowSlot, el("div.card", empty("Cannot reach archied", String(err.message || err))));
      mount(stageSlot);
    }
  }

  function renderStartWork(definitions) {
    if (!definitions.length) {
      mount(startSlot);
      return;
    }
    const identity = el("input", { placeholder: "Identity", required: true });
    const repository = el("input", { placeholder: "owner/repository", required: true });
    const workflow = el("select", { required: true }, ...definitions.filter((d) => d.enabled).map((d) => el("option", { value: d.id }, workflowLabel(d.name || d.id))));
    const title = el("input", { placeholder: "Short task title", required: true });
    const instructions = el("textarea", { placeholder: "Instructions for the work", required: true, rows: 3 });
    const notice = el("p.card-sub");
    const submit = el("button.btn", { type: "submit" }, "Start work");
    const form = el("form.wf-form", {
      onsubmit: async (event) => {
        event.preventDefault();
        submit.disabled = true;
        try {
          const result = await api.workRequest({ identity: identity.value, repository: repository.value, workflow: workflow.value, title: title.value, instructions: instructions.value });
          notice.textContent = `Queued task #${result.task_id}.`;
          form.reset();
        } catch (err) {
          notice.textContent = String(err.message || err);
        } finally {
          submit.disabled = false;
        }
      },
    }, identity, repository, workflow, title, instructions, submit, notice);
    mount(startSlot, el("div.card", el("div.card-head", el("div", el("h2.card-title", "Start work"), el("p.card-sub", "This enters Archie’s normal admitted task queue."))), form));
  }

  function renderWorkflows(workflows, definitions) {
    const byID = new Map(workflows.map((workflow) => [workflow.workflow, workflow]));
    const rows = definitions.map((definition) => ({ ...definition, ...(byID.get(definition.id) || { workflow: definition.id, runs: 0, merged: 0 }) }));
    if (!rows.length) {
      mount(
        workflowSlot,
        el(
          "div.card",
          el("div.card-head", el("div", el("h2.card-title", "Per workflow"))),
          empty("No workflow runs yet", "Stats appear here once a task has run through a workflow."),
        ),
      );
      return;
    }

    mount(
      workflowSlot,
      el(
        "div.card",
        el(
          "div.card-head",
          el(
            "div",
            el("h2.card-title", "Per workflow"),
            el("p.card-sub", "Success rate is merged tasks over all runs"),
          ),
        ),
        el(
          "div.table-scroll",
          el(
            "table.table",
            el(
              "thead",
              el(
                "tr",
                ...["Workflow", "Origin", "Runs", "Success rate", "Avg tokens", "Avg steps"].map((h) => el("th", h)),
              ),
            ),
            el(
              "tbody",
              ...rows.map((w) => {
                const rate = pct(w.merged, w.runs);
                return el(
                  "tr",
                  el("td.strong", workflowLabel(w.workflow)),
				  el("td.mono", w.origin || "registry"),
                  el("td", `${w.runs} run${w.runs === 1 ? "" : "s"}`),
                  el(
                    "td",
                    el("div.wf-rate", `${rate}%`, el("span.wf-rate-sub", `${w.merged} of ${w.runs} merged`)),
                    bar(rate, rate >= 50 ? "ok" : "warn"),
                  ),
                  el("td.mono", w.avg_tokens ? w.avg_tokens.toLocaleString() : "—"),
                  el("td.mono", w.avg_steps ? w.avg_steps.toFixed(1) : "—"),
                );
              }),
            ),
          ),
        ),
      ),
    );
  }

  function renderStages(stages) {
    if (!stages.length) {
      mount(
        stageSlot,
        el(
          "div.card.wf-stage-card",
          el("div.card-head", el("div", el("h2.card-title", "Per stage"))),
          empty("No stage data yet", "Stage timing and failures appear here once workflows have run."),
        ),
      );
      return;
    }

    const maxMs = Math.max(...stages.map((s) => s.avg_ms || 0), 1);
    const byDuration = [...stages].sort((a, b) => (b.avg_ms || 0) - (a.avg_ms || 0));
    const byErrors = [...stages].filter((s) => s.errors > 0).sort((a, b) => b.errors - a.errors);

    mount(
      stageSlot,
      el(
        "div.grid.grid-2.wf-stage-card",
        el(
          "div.card",
          el(
            "div.card-head",
            el("div", el("h2.card-title", "Slowest stages"), el("p.card-sub", "Average duration, this workflow's stages")),
          ),
          el(
            "div.table-scroll",
            el(
              "table.table",
              el("thead", el("tr", ...["Workflow", "Stage", "Runs", "Avg duration"].map((h) => el("th", h)))),
              el(
                "tbody",
                ...byDuration.map((s) =>
                  el(
                    "tr",
                    el("td", workflowLabel(s.workflow)),
                    el("td.strong", s.stage),
                    el("td", `${s.runs}`),
                    el(
                      "td",
                      el("div.wf-rate", formatMs(s.avg_ms)),
                      bar(pct(s.avg_ms, maxMs), "info"),
                    ),
                  ),
                ),
              ),
            ),
          ),
        ),
        el(
          "div.card",
          el(
            "div.card-head",
            el("div", el("h2.card-title", "Most failures"), el("p.card-sub", "Stages that error out, over their runs")),
          ),
          byErrors.length
            ? el(
                "div.table-scroll",
                el(
                  "table.table",
                  el("thead", el("tr", ...["Workflow", "Stage", "Failures"].map((h) => el("th", h)))),
                  el(
                    "tbody",
                    ...byErrors.map((s) =>
                      el(
                        "tr",
                        el("td", workflowLabel(s.workflow)),
                        el("td.strong", s.stage),
                        el(
                          "td",
                          el("div.wf-rate", `${s.errors} of ${s.runs}`),
                          bar(pct(s.errors, s.runs), "danger"),
                        ),
                      ),
                    ),
                  ),
                ),
              )
            : empty("No failures recorded", "Every stage has completed cleanly so far."),
        ),
      ),
    );
  }

  return root;
}
