import { el } from "../base/dom.js";
import { renderMarkdown } from "./markdown.js";

export function sessionTitle(session) {
  return session.title || session.branch_name || `Conversation ${session.session_id.slice(0, 8)}`;
}

export function chatBubble(message) {
  const from = message.from ?? message.From ?? "";
  const text = message.text ?? message.Text ?? "";
  const isAssistant = from !== "web";
  return el(
    `div.chat-bubble-row.${isAssistant ? "assistant" : "user"}`,
    el(
      "div.chat-bubble",
      el("div.chat-bubble-meta", isAssistant ? "Archie" : "You"),
      isAssistant ? renderMarkdown(text) : el("div.chat-bubble-text", text),
    ),
  );
}
