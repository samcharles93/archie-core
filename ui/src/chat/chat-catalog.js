import { el } from "../base/dom.js";
import { sessionTitle } from "./chat-render.js";

export function renderSessions({ sessionList, sessionTools, sessions, currentSession, onSelect }) {
  sessionList.replaceChildren();
  sessionTools.hidden = !currentSession;
  if (!sessions.length) {
    sessionList.append(el("div.chat-empty", "Your conversations will appear here."));
    return;
  }
  for (const session of sessions) {
    sessionList.append(el(
      "button.chat-session",
      { class: currentSession === session.session_id ? "active" : "", onclick: () => onSelect(session.session_id) },
      el("strong", sessionTitle(session)),
      el("span", new Date(session.last_active_at).toLocaleString()),
    ));
  }
}

export function renderSelectors(refs, data, onDangerous) {
  refs.persona.replaceChildren(el("option", { value: "" }, "Personality"));
  for (const name of data.personas || []) refs.persona.append(el("option", { value: name }, name));
  refs.persona.value = data.active_persona || "";
  const providers = data.providers || [];
  refs.provider.hidden = providers.length === 0;
  refs.provider.replaceChildren(el("option", { value: "" }, "Provider"));
  for (const name of providers) refs.provider.append(el("option", { value: name }, name));
  refs.provider.value = data.active_provider || providers[0] || "";
  renderModels(refs, data, refs.provider.value);
  refs.restart.hidden = !data.restart_available;
  refs.dangerousPanel.hidden = !data.dangerous_available;
  if (data.dangerous_available) onDangerous();
}

export function renderModels(refs, data, providerName) {
  const grouped = data.models_by_provider || {};
  const names = providerName && grouped[providerName] ? grouped[providerName] : data.models || [];
  refs.model.replaceChildren(el("option", { value: "" }, "Model"));
  for (const name of names) refs.model.append(el("option", { value: name }, name));
  refs.model.value = data.active_model || "";
}

export function renderCommands(commandHelp, commands) {
  const specs = (commands || []).map((item) => typeof item === "string" ? { command: item, usage: item, description: "" } : item);
  const list = el("div.chat-command-list");
  if (!specs.length) list.append(el("span", "Send a message to begin."));
  for (const spec of specs) list.append(el("div.chat-command-item", el("code", spec.usage || spec.command), el("span", spec.description || "")));
  commandHelp.replaceChildren(el("summary", `Available commands (${specs.length})`), list);
  return specs;
}
