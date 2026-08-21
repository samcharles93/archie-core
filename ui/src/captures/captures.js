import "./captures.css";
import { api, subscribeEvents } from "../base/api.js";
import { el, empty, mount, pill } from "../base/dom.js";
import { captureRow } from "./capture-row.js";

/**
 * Event inspector (t2db.2): the operator-facing half of capture. Recent
 * inbound events, newest first, with the full redacted payload explorable
 * per row -- see docs/prds/event-capture-storage.md for what the backend
 * guarantees (retention, redaction, disk bounds).
 *
 * Live updates ride the same /events SSE stream every other page uses
 * (Server.emit publishes a "capture" kind event on every successful
 * capture) rather than polling, so a captured event appears here without a
 * manual refresh. That event is deliberately lightweight (id + source only,
 * no payload -- see handleCapture's comment on why): it is treated purely
 * as an invalidation signal, triggering a refetch of the authoritative list
 * rather than being merged into local state directly.
 */
export function capturesPage() {
  const root = el("div");
  const list = el("div.card");
  const streamState = pill("connecting", "idle");

  let captures = [];
  let expandedId = null;
  let loadError = null;
  let enabled = true;

  render();
  load();

  const unsubscribe = subscribeEvents(
    (event) => {
      if (event.kind !== "capture") return;
      load();
    },
    (state) => {
      streamState.className = `pill pill-${state === "live" ? "ok" : "warn"}`;
      streamState.textContent = state;
    },
  );
  root.addEventListener("archie:teardown", unsubscribe);

  async function load() {
    try {
      const res = await api.captures(100);
      enabled = res.enabled !== false;
      captures = res.captures || [];
      loadError = null;
    } catch (err) {
      loadError = String(err.message || err);
    }
    renderRows();
  }

  function render() {
    mount(
      root,
      el(
        "div.page-head",
        el(
          "div",
          el("h1.page-title", "Event inspector"),
          el("p.page-sub", "Real inbound payloads archie has captured, most recent first."),
        ),
        el("div.page-actions", streamState, el("button.btn", { onclick: load }, "Refresh")),
      ),
      list,
    );
    renderRows();
  }

  function renderRows() {
    if (loadError) {
      mount(list, empty("Cannot reach archied", loadError));
      return;
    }
    if (!enabled) {
      mount(list, empty("Capture is not configured", "This deployment has no capture storage wired up."));
      return;
    }
    if (!captures.length) {
      mount(
        list,
        empty("No captures yet", "Point a webhook at /webhooks/capture/<source> and it will show up here."),
      );
      return;
    }
    mount(
      list,
      el(
        "div.table-scroll",
        el(
          "table.table",
          el(
            "thead",
            el(
              "tr",
              ...["Source", "Received", "Binding", "Content type", ""].map((h) => el("th", h)),
            ),
          ),
          el("tbody", ...captures.flatMap((c) => captureRow(c, {
            expanded: expandedId === c.id,
            onToggle: () => {
              expandedId = expandedId === c.id ? null : c.id;
              renderRows();
            },
          }))),
        ),
      ),
    );
  }

  return root;
}
