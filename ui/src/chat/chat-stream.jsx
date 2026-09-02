import { renderMarkdown } from "./markdown.jsx";

export function updateStreamingReply(current, text) {
  const next = renderMarkdown(text);
  if (typeof current?.replaceWith === "function") current.replaceWith(next);
  else if (current?.parentNode) current.parentNode.replaceChild(next, current);
  return next;
}
