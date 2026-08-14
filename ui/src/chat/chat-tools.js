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
  // A failure reads as a completed step unless it is marked as one.
  if (summary.startsWith("failed:")) line.className = "chat-tool failed";

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
