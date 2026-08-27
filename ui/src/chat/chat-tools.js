import { el } from "../base/dom.js";

// Tool activity is rendered into its own list inside the assistant bubble
// rather than folded into the reply text: the text node is replaced wholesale
// on every streamed frame, and again on the final reply, so anything written
// into it is lost the next time the answer grows.
//
// The list is part of the bubble from the start (see assistantBubble) and
// sits above the reply, matching the shape the same turn takes in Telegram:
// the work first, then the answer built from it.
export function appendToolCall(host, event) {
  const name = event?.tool ?? "";
  if (!host || !name) return null;

  const parameters = event.parameters ?? "";
  const summary = event.text ?? "";
  const line = el(
    "div.chat-tool",
    el("span.chat-tool-icon", "🔧"),
    el("span.chat-tool-name", name),
    parameters && el("code.chat-tool-parameters", parameters),
    el("span.chat-tool-summary", summary),
  );
  // A failure reads as a completed step unless it is marked as one. Styled
  // from the structured `failed` field the server sends, not by sniffing
  // the summary text: a successful tool can print output that itself starts
  // with "failed:" (e.g. a grep hit, a log line), which a string prefix
  // check would misread as the tool call itself having failed.
  if (event.failed) line.className = "chat-tool failed";

  toolList(host).append(line);
  return line;
}

// toolList returns the bubble's tool list. A bubble built elsewhere may not
// have one, in which case the list is appended rather than dropped  --  tool
// activity out of position still beats tool activity nowhere.
function toolList(host) {
  const existing = host.querySelector(".chat-tools");
  if (existing) return existing;
  const list = el("div.chat-tools");
  host.append(list);
  return list;
}

// appendNavigateChip renders a dashboard_navigate result as a clickable chip
// that routes the operator to a dashboard page. It is an action, not a tool
// narration line: the model called dashboard_navigate to point the operator
// somewhere, so the chip is a real link (the app routes on location.hash),
// and it is the only clickable navigation surface the transcript offers. The
// label is the tool's resolved page name, never a markdown link the model
// invented.
export function appendNavigateChip(host, event) {
  const path = event?.path ?? "";
  const label = event?.label || path;
  if (!host || !path) return null;
  const chip = el("a.chat-nav-chip",
    {
      href: "#" + path,
      title: "Go to " + label,
      "aria-label": "Go to " + label,
    },
    el("span.chat-nav-chip-icon", { "aria-hidden": "true" }, "↗"),
    el("span.chat-nav-chip-label", label),
  );
  toolList(host).append(chip);
  return chip;
}
