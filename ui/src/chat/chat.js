import { el } from "../base/dom.js";
import { api } from "../base/api.js";
import { createChatView } from "./chat-view.js";
import { chatBubble } from "./chat-render.js";
import { updateStreamingReply } from "./chat-stream.js";
import { channelID } from "./chat-state.js";
import { retryChatTurn, resolveTurn } from "./chat-retry.js";
import { renderCommands, renderModels, renderSelectors, renderSessions } from "./chat-catalog.js";
import { renderDangerous, renderUpdate } from "./chat-panels.js";

import "./chat.css";

export function chatPage() {
  const { root, refs } = createChatView();
  const { sessionList, transcript, composer, send, stop, status, persona, provider, model,
    restart, emptyState, updatePanel, dangerousPanel, commandMenu, sessionTools, commandHelp,
    checkUpdates, newChat } = refs;
  let currentSession = "";
  let sessions = [];
  let sending = false;
  let activeController = null;
  let updateSnapshot = null;
  let selectorData = {};
  let commandSpecs = [];
  let commandMatches = [];
  let commandSelection = 0;

  restart.onclick = () => {
    if (!window.confirm("Reload the Telegram chat adapter?")) return;
    composer.value = "/restart";
    sendMessage();
  };
  newChat.onclick = () => { composer.value = "/new"; sendMessage(); };
  for (const prompt of root.querySelectorAll("[data-chat-prompt]")) {
    prompt.onclick = () => { composer.value = prompt.dataset.chatPrompt; composer.focus(); };
  }

  function appendMessage(message) {
    emptyState.remove();
    transcript.append(chatBubble(message));
    transcript.scrollTop = transcript.scrollHeight;
  }

  function commandQuery() {
    const before = composer.value.slice(0, composer.selectionStart);
    const match = before.match(/(?:^|\s)\/([^\s]*)$/);
    return match ? match[1].toLowerCase() : null;
  }

  function hideCommandMenu() {
    commandMenu.hidden = true;
    commandMatches = [];
    commandSelection = 0;
  }

  function renderCommandMenu() {
    const query = commandQuery();
    if (query === null) {
      hideCommandMenu();
      return;
    }
    commandMatches = commandSpecs.filter((spec) => {
      const haystack = `${spec.command} ${spec.description}`.toLowerCase();
      return haystack.includes(query);
    }).slice(0, 8);
    if (!commandMatches.length) {
      hideCommandMenu();
      return;
    }
    commandSelection = Math.min(commandSelection, commandMatches.length - 1);
    commandMenu.replaceChildren(...commandMatches.map((spec, index) => el(
      "button.chat-command-option",
      {
        type: "button",
        role: "option",
        "aria-selected": String(index === commandSelection),
        onmousedown: (event) => event.preventDefault(),
        onclick: () => chooseCommand(index),
      },
      el("code", spec.command),
      el("span", spec.description || spec.usage || ""),
    )));
    commandMenu.hidden = false;
  }

  function chooseCommand(index) {
    const spec = commandMatches[index];
    if (!spec) return;
    const before = composer.value.slice(0, composer.selectionStart);
    const after = composer.value.slice(composer.selectionStart);
    const start = before.search(/(?:^|\s)\/[^\s]*$/);
    const tokenStart = start < 0 ? before.length : start + (before[start] === " " ? 1 : 0);
    composer.value = `${before.slice(0, tokenStart)}${spec.command} ${after}`;
    const cursor = tokenStart + spec.command.length + 1;
    composer.setSelectionRange(cursor, cursor);
    hideCommandMenu();
    composer.focus();
  }

  async function refreshUpdate() {
    try {
      const data = await api.chatUpdate();
      updateSnapshot = data?.snapshot || null;
      renderUpdate(updatePanel, data, {
        onDefer: async () => {
          await api.chatUpdateDefer(updateSnapshot);
          renderUpdate(updatePanel, { snapshot: { deferred: true }, available: [] }, { onDefer: () => {}, onInstall: () => {} });
          status.textContent = "Update deferred";
        },
        onInstall: async () => {
          status.textContent = "Installing…";
          await api.chatUpdateInstall(updateSnapshot);
          status.textContent = "Update started";
          refreshUpdate();
        },
      });
    } catch (err) {
      updatePanel.replaceChildren(el("span", err.message || "Updates unavailable"));
    }
  }

  async function refreshDangerous() {
    try {
      renderDangerous(dangerousPanel, await api.chatDangerous(), {
        onRequest: async (kind, spec) => {
          if (!spec) return;
          try {
            await api.chatDangerousRequest(kind, spec);
            status.textContent = "Approval requested";
            await refreshDangerous();
          } catch (err) {
            status.textContent = err.message || "Could not request action";
          }
        },
        onDecision: async (id, decision) => {
          try {
            await api.chatDangerousDecision(id, decision);
            status.textContent = decision === "deny" ? "Action denied" : "Action approved";
            await refreshDangerous();
          } catch (err) {
            status.textContent = err.message || "Approval failed";
          }
        },
      });
    } catch (err) {
      dangerousPanel.replaceChildren(el("span.chat-hint", err.message || "Dangerous actions unavailable"));
    }
  }

  async function refreshSessions() {
    const data = await api.chatSessions();
    sessions = data.sessions || [];
    selectorData = data;
    renderSelectors({ persona, provider, model, restart, dangerousPanel }, data, refreshDangerous);
    commandSpecs = renderCommands(commandHelp, data.commands);
    renderSessions({ sessionList, sessionTools, sessions, currentSession, onSelect: selectSession });
    if (!currentSession && sessions[0]) await selectSession(sessions[0].session_id);
  }

  async function runSessionCommand(command) {
    if (!currentSession) return;
    try {
      status.textContent = "Working…";
      const result = await api.chatMessage(channelID(), command);
      if (result.session_id) currentSession = result.session_id;
      status.textContent = result.reply || "Session updated";
      await refreshSessions();
      if (currentSession) await selectSession(currentSession);
      sessionTools.open = false;
    } catch (err) {
      status.textContent = err.message || "Session action failed";
    }
  }

  function promptSessionCommand(command, message) {
    const value = window.prompt(message);
    if (value?.trim()) runSessionCommand(`${command} ${value.trim()}`);
  }

  async function selectSession(id) {
    currentSession = id;
    renderSessions({ sessionList, sessionTools, sessions, currentSession, onSelect: selectSession });
    transcript.replaceChildren();
    await api.chatMessage(channelID(), `/resume ${id}`);
    persona.value = selectorData.active_personas?.[id] || selectorData.active_persona || "";
    const messages = await api.chatMessages(id);
    for (const message of messages) appendMessage(message);
    if (!transcript.children.length) transcript.append(emptyState);
  }

  async function sendMessage(retry = null) {
    const { text, turn, isRetry } = resolveTurn(retry, composer.value);
    if (!text || sending) return;
    hideCommandMenu();
    sending = true;
    send.disabled = true;
    stop.disabled = false;
    composer.disabled = true;
    status.textContent = isRetry ? "Retrying…" : "Thinking…";
    if (!isRetry) {
      appendMessage({ from: "web", text });
      composer.value = "";
    }
    const replyBubble = isRetry ? retry.replyBubble : el("div.chat-bubble-row.assistant", el("div.chat-bubble", el("div.chat-bubble-meta", "Archie"), el("div.chat-bubble-text")));
    if (!isRetry) transcript.append(replyBubble);
    replyBubble.querySelector(".chat-retry")?.remove();
    let replyText = replyBubble.querySelector(".chat-bubble-text");
    let streamedText = "";
    let finished = false;
    let timedOut = false;
    let timeout;
    try {
      activeController = new AbortController();
      timeout = setTimeout(() => {
        timedOut = true;
        activeController.abort();
      }, 120000);
      const response = await fetch("/api/chat/stream", {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "text/event-stream" },
        body: JSON.stringify({ channel_id: channelID(), source_id: turn.sourceID, text: turn.text }),
        signal: activeController.signal,
      });
      if (!response.ok) throw new Error((await response.text()) || `${response.status} ${response.statusText}`);
      const reader = response.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";
      for (;;) {
        const { value, done } = await reader.read();
        buffer += decoder.decode(value || new Uint8Array(), { stream: !done });
        const frames = buffer.split("\n\n");
        buffer = frames.pop() || "";
        for (const frame of frames) {
          const line = frame.split("\n").find((part) => part.startsWith("data: "));
          if (!line) continue;
          const event = JSON.parse(line.slice(6));
          if (event.session_id) currentSession = event.session_id;
          if (event.type === "delta") {
            streamedText += event.text;
            replyText = updateStreamingReply(replyText, streamedText);
          }
          if (event.type === "done") {
            finished = true;
            if (!streamedText) {
              streamedText = event.text;
              replyText = updateStreamingReply(replyText, streamedText);
            }
          }
          if (event.type === "error") throw new Error(event.text);
        }
        if (done) break;
      }
      if (!finished) throw new Error("chat stream ended before completion");
      if (replyText.isConnected) replyText = updateStreamingReply(replyText, streamedText);
      status.textContent = "Ready";
      await refreshSessions();
    } catch (err) {
      if (err.name === "AbortError" && !timedOut) {
        replyText = updateStreamingReply(replyText, "Turn stopped.");
        status.textContent = "Stopped";
      } else {
        replyText = updateStreamingReply(replyText, `Unable to complete that turn: ${err.message || err}`);
        const failedTurn = retryChatTurn(turn);
        const retryButton = el("button.btn.chat-retry", {
          type: "button",
          onclick: () => sendMessage({ turn: failedTurn, replyBubble }),
        }, "Retry");
        replyBubble.append(retryButton);
        status.textContent = "Error — retry available";
      }
    } finally {
      clearTimeout(timeout);
      activeController = null;
      sending = false;
      send.disabled = false;
      stop.disabled = true;
      composer.disabled = false;
      composer.focus();
    }
  }

  send.onclick = () => sendMessage();
  stop.onclick = () => {
    const sessionID = currentSession;
    status.textContent = "Stopping…";
    if (sessionID) void api.chatCancel(sessionID).catch(() => {});
    activeController?.abort();
  };
  composer.oninput = renderCommandMenu;
  composer.onkeydown = (event) => {
    if (!commandMenu.hidden && commandMatches.length) {
      if (event.key === "Enter" && !event.shiftKey && !event.ctrlKey && !event.metaKey
        && commandMatches.some((spec) => spec.command === composer.value.trim())) {
        event.preventDefault();
        hideCommandMenu();
        sendMessage();
        return;
      }
      if (event.key === "ArrowDown") {
        event.preventDefault();
        commandSelection = (commandSelection + 1) % commandMatches.length;
        renderCommandMenu();
        return;
      }
      if (event.key === "ArrowUp") {
        event.preventDefault();
        commandSelection = (commandSelection + commandMatches.length - 1) % commandMatches.length;
        renderCommandMenu();
        return;
      }
      if (event.key === "Enter" && !event.shiftKey && !event.ctrlKey && !event.metaKey) {
        event.preventDefault();
        chooseCommand(commandSelection);
        return;
      }
      if (event.key === "Escape") {
        event.preventDefault();
        hideCommandMenu();
        return;
      }
    }
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      sendMessage();
    }
  };
  persona.onchange = async () => {
    if (!currentSession || !persona.value) return;
    await api.chatPersona(currentSession, persona.value);
    selectorData.active_personas = { ...(selectorData.active_personas || {}), [currentSession]: persona.value };
    status.textContent = `Personality: ${persona.value}`;
  };
  provider.onchange = async () => {
    renderModels({ model }, selectorData, provider.value);
    if (!provider.value) return;
    await applyModelCommand(`/model --provider ${provider.value}`);
  };
  model.onchange = async () => {
    if (model.value) await applyModelCommand(`/model ${model.value}`);
  };
  async function applyModelCommand(command) {
    try {
      const result = await api.chatMessage(channelID(), command);
      status.textContent = result.reply || "Model updated";
      await refreshSessions();
    } catch (err) {
      status.textContent = `Model update failed: ${err.message || err}`;
    }
  };

  checkUpdates.onclick = refreshUpdate;
  const actionHandlers = {
    retry: () => runSessionCommand("/retry"),
    undo: () => runSessionCommand("/undo"),
    title: () => promptSessionCommand("/title", "Session title"),
    branch: () => promptSessionCommand("/branch", "Branch name (optional)"),
    compress: () => runSessionCommand("/compress --preview"),
  };
  for (const button of root.querySelectorAll("[data-chat-action]")) {
    button.onclick = actionHandlers[button.dataset.chatAction];
  }

  refreshSessions().catch((err) => { status.textContent = err.message || "Chat unavailable"; });
  refreshUpdate();
  return root;
}
