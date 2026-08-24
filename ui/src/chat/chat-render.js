import { el } from "../base/dom.js";
import { renderMarkdown } from "./markdown.js";

export function sessionTitle(session) {
  return session.title || session.branch_name || `Conversation ${session.session_id.slice(0, 8)}`;
}

// assistantBubble builds the row a live reply streams into. The tool list is
// created empty and up front so tool activity has somewhere to land that the
// streaming text replacement cannot destroy, and so its position above the
// reply is fixed by the bubble rather than by insertion order.
export function assistantBubble() {
  return el(
    "div.chat-bubble-row.assistant",
    el(
      "div.chat-bubble",
      el("div.chat-bubble-meta", "Archie"),
      el("div.chat-tools"),
      el("div.chat-bubble-text"),
    ),
  );
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
      isAssistant ? assistantContent(message, text) : el("div.chat-bubble-text", text),
    ),
  );
}

function assistantContent(message, text) {
  const tools = message.tool_calls || message.ToolCalls || [];
  const children = [el("div.chat-tools")];
  for (const tool of tools) {
    const failed = Boolean(tool.err || tool.Err);
    children[0].append(el("div.chat-tool" + (failed ? ".failed" : ""),
      el("span.chat-tool-icon", "🔧"),
      el("span.chat-tool-name", tool.name || tool.Name || "tool"),
      el("span.chat-tool-summary", tool.summary || tool.Summary || tool.err || tool.Err || "done")));
  }
  children.push(renderMarkdown(text));
  return children;
}
