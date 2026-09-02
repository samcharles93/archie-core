import template from "./chat.html?raw";

export function createChatView() {
  const holder = document.createElement("template");
  holder.innerHTML = template.trim();
  const root = holder.content.firstElementChild;
  const refs = {};
  for (const node of root.querySelectorAll("[data-chat-ref]")) refs[node.dataset.chatRef] = node;
  return { root, refs };
}
