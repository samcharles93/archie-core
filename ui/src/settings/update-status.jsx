import { el, empty, pill } from "../base/dom.jsx";

const STATUS_LABELS = {
  ok: "OK",
  update_available: "Update available",
  drift: "Drift",
  unknown: "Unknown",
};

const STATUS_KINDS = {
  ok: "ok",
  update_available: "info",
  drift: "danger",
  unknown: "idle",
};

/**
 * Per-component deployment/update status (GET /api/version): how each
 * component is installed, what's actually running, and whether that
 * agrees with what the update-check command claims. The comparison basis
 * -- the exact installed claim, running version and reference this status
 * was computed from -- sits in the row's title attribute (a hover tooltip)
 * rather than a separate expand control, since it's supplementary detail
 * an operator wants on demand, not on every glance at the table.
 *
 * Split out from settings.js (which top-level imports settings.css) so it
 * can be unit tested under plain node --test.
 */
export function updateStatusCard(components) {
  if (!components.length) {
    return section(
      "How each component is deployed, and whether what's running matches what's installed.",
      empty("No components reported", "The configured update-check command returned nothing."),
    );
  }
  return section(
    "How each component is deployed, and whether what's running matches what's installed. Hover a row for the exact versions compared.",
    el(
      "div.table-scroll",
      el(
        "table.table",
        el("thead", el("tr", ...["Component", "Install type", "Running", "Latest available", "Status"].map((h) => el("th", h)))),
        el(
          "tbody",
          ...components.map((c) => {
            const basis = [
              `Installed claim: ${c.installed_claim || "unknown"}`,
              `Running: ${c.running_version || "not observed"}`,
              c.reference ? `Reference: ${c.reference}` : null,
            ].filter(Boolean).join("\n");
            return el(
              "tr",
              { title: basis },
              el("td.strong", c.label || c.id),
              el("td.mono", c.install_type || "—"),
              el("td.mono", c.running_version || "—"),
              el("td.mono", c.latest_available || "—"),
              el("td", pill(STATUS_LABELS[c.status] || c.status, STATUS_KINDS[c.status] || "idle")),
            );
          }),
        ),
      ),
    ),
  );
}

function section(sub, ...children) {
  return el(
    "div.card.cfg-section",
    el("div.card-head", el("div", el("h2.card-title", "Update status"), el("p.card-sub", sub))),
    ...children,
  );
}
