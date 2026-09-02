import { el } from "../base/dom.jsx";

export function renderUpdate(panel, data, { onDefer, onInstall }) {
  panel.replaceChildren();
  const snapshot = data?.snapshot || null;
  const available = data?.available || [];
  if (!available.length) {
    panel.append(el("span", snapshot?.deferred ? "Update deferred." : "Archie is up to date."));
    return;
  }
  panel.append(
    el("div", el("strong", "Update available"), el("span", available.map((component) => `${component.Label || component.label}: ${component.Available || component.available}`).join(" · "))),
    el("div.chat-update-actions",
      el("button.btn", { onclick: onDefer }, "Defer"),
      data.can_install ? el("button.btn.btn-primary", { onclick: onInstall }, "Install update") : null,
    ),
  );
}

export function renderDangerous(panel, data, { onRequest, onDecision }) {
  panel.replaceChildren();
  const checkpoints = data?.checkpoints || [];
  const pending = data?.pending || [];
  const checkpoint = el("select.chat-select", { "aria-label": "Checkpoint" },
    el("option", { value: "" }, "Select checkpoint"),
    ...checkpoints.map((item) => el("option", { value: item.Number ?? item.number }, `${item.Number ?? item.number} — ${item.Label ?? item.label ?? "checkpoint"}`)),
  );
  const stopSpec = el("input.chat-dangerous-input", { placeholder: "Process name or id", "aria-label": "Process name or id" });
  const request = (kind, spec) => () => onRequest(kind, String(spec.value || spec).trim());
  const decisionButton = (id, decision, label) => el("button.btn", { onclick: () => onDecision(id, decision) }, label);
  const pendingRows = pending.length ? pending.map((action) => el("div.chat-dangerous-pending",
    el("span", action.description),
    el("div.chat-update-actions",
      decisionButton(action.id, "approve", "Approve"),
      decisionButton(action.id, "permanent", "Approve for 24h"),
      decisionButton(action.id, "deny", "Deny"),
    ),
  )) : [el("span.chat-hint", "No pending dangerous actions.")];
  panel.append(
    el("div.chat-dangerous-head", el("strong", "Dangerous actions"), el("span.chat-hint", "Every action requires approval.")),
    el("div.chat-dangerous-controls",
      el("div", checkpoint, el("button.btn", { onclick: request("rollback", checkpoint) }, "Request rollback")),
      el("div", stopSpec, el("button.btn", { onclick: request("stop", stopSpec) }, "Request stop")),
    ),
    el("div.chat-dangerous-pending-list", ...pendingRows),
  );
}
