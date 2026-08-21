import { ago, el, pill } from "../base/dom.js";

/**
 * One captured event's summary row, plus its expandable detail row when
 * expanded is true. Split out from captures.js so it can be unit tested
 * without the page's fetch/SSE wiring, mirroring tasks/task-row.js.
 */
export function captureRow(c, { expanded, onToggle }) {
  const row = el(
    "tr.capture-row",
    {
      tabindex: "0",
      "aria-expanded": String(expanded),
      onclick: onToggle,
      onkeydown: (e) => {
        if (e.key !== "Enter" && e.key !== " ") return;
        e.preventDefault();
        onToggle(e);
      },
    },
    el("td.mono", c.source || "(unknown)"),
    el("td", { title: c.received_at || "" }, ago(c.received_at)),
    // Every captured event is unbound today: no playbook binding data model
    // exists yet (t2db.4). Once a binding can match an event, this pill
    // distinguishes bound from unbound instead of always reading "Unbound"
    // -- see docs/prds/event-capture-storage.md's "what this does not decide".
    el("td", pill("Unbound", "idle")),
    el("td.mono", c.content_type || "—"),
    el("td", el("button.btn.btn-small", { type: "button" }, expanded ? "Hide" : "View")),
  );
  return expanded ? [row, captureDetailRow(c)] : [row];
}

export function captureDetailRow(c) {
  return el(
    "tr.capture-detail-row",
    el(
      "td",
      { colspan: 5 },
      el(
        "div.capture-detail",
        el("div.capture-detail-section", el("div.capture-detail-title", "Payload"), payloadView(c.body)),
        el("div.capture-detail-section", el("div.capture-detail-title", "Headers"), payloadView(c.headers)),
      ),
    ),
  );
}

function payloadView(raw) {
  if (!raw) return el("div.capture-empty", "(empty)");
  return el("pre.capture-payload", prettyPrint(raw));
}

// prettyPrint indents valid JSON for readability; a non-JSON payload (the
// capture endpoint stores whatever arrives, JSON or not, per
// docs/prds/webhook-intake-security.md) is shown as-is rather than failing
// to render.
export function prettyPrint(raw) {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch {
    return raw;
  }
}
