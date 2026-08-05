import { el, mount } from "../base/dom.js";
import { api } from "../base/api.js";

import "./chat.css";

const CHANNEL_KEY = "archie.web.chat.channel";

function channelID() {
  let id = localStorage.getItem(CHANNEL_KEY);
  if (!id) {
    id = crypto.randomUUID();
    localStorage.setItem(CHANNEL_KEY, id);
  }
  return id;
}

function sessionTitle(session) {
  return session.title || session.branch_name || `Conversation ${session.session_id.slice(0, 8)}`;
}

function inlineMarkdown(text) {
  const fragment = document.createDocumentFragment();
  const pattern = /(~~[^~]+~~|\*\*[^*]+\*\*|__[^_]+__|`[^`]+`|\[[^\]]+\]\(https?:\/\/[^\s)]+\)|\*[^*]+\*|_[^_]+_)/g;
  let cursor = 0;
  for (const match of text.matchAll(pattern)) {
    if (match.index > cursor) fragment.append(document.createTextNode(text.slice(cursor, match.index)));
    const value = match[0];
    if (value.startsWith("~~")) fragment.append(el("del", value.slice(2, -2)));
    else if (value.startsWith("**") || value.startsWith("__")) fragment.append(el("strong", value.slice(2, -2)));
    else if (value.startsWith("`")) fragment.append(el("code", value.slice(1, -1)));
    else if (value.startsWith("[")) {
      const link = value.match(/^\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)$/);
      fragment.append(el("a", { href: link[2], target: "_blank", rel: "noreferrer" }, link[1]));
    } else fragment.append(el("em", value.slice(1, -1)));
    cursor = match.index + value.length;
  }
  if (cursor < text.length) fragment.append(document.createTextNode(text.slice(cursor)));
  return fragment;
}

function renderMarkdown(text) {
  const root = el("div.chat-bubble-text");
  const lines = String(text || "").split("\n");
  let paragraph = [];
  let list = null;
  let code = null;
  const flushParagraph = () => {
    if (paragraph.length) {
      root.append(el("p", inlineMarkdown(paragraph.join(" "))));
      paragraph = [];
    }
  };
  const flushList = () => {
    if (list) root.append(list);
    list = null;
  };
  for (let index = 0; index < lines.length; index++) {
    const line = lines[index];
    if (line.startsWith("```")) {
      flushParagraph();
      flushList();
      if (code) {
        root.append(el("pre", el("code", code.join("\n"))));
        code = null;
      } else code = [];
      continue;
    }
    if (code) {
      code.push(line);
      continue;
    }
    const headerCells = tableCells(line);
    const separatorCells = index + 1 < lines.length ? tableCells(lines[index + 1]) : null;
    if (headerCells && separatorCells && isTableSeparator(separatorCells) && headerCells.length === separatorCells.length) {
      flushParagraph();
      flushList();
      const rows = [];
      for (index += 2; index < lines.length; index++) {
        const cells = tableCells(lines[index]);
        if (!cells || cells.length !== headerCells.length) {
          index--;
          break;
        }
        rows.push(cells);
      }
      const body = el("tbody");
      for (const row of rows) {
        body.append(el("tr", ...row.map((cell) => el("td", inlineMarkdown(cell)))));
      }
      root.append(el(
        "table",
        el("thead", el("tr", ...headerCells.map((cell) => el("th", inlineMarkdown(cell))))),
        body,
      ));
      continue;
    }
    if (!line.trim()) {
      flushParagraph();
      flushList();
      continue;
    }
    const heading = line.match(/^(#{1,3})\s+(.+)$/);
    if (heading) {
      flushParagraph();
      flushList();
      root.append(el(`h${heading[1].length}`, inlineMarkdown(heading[2])));
      continue;
    }
    const bullet = line.match(/^\s*[-*]\s+(.+)$/);
    const ordered = line.match(/^\s*\d+[.)]\s+(.+)$/);
    if (bullet || ordered) {
      flushParagraph();
      const tag = ordered ? "ol" : "ul";
      if (!list || list.tagName.toLowerCase() !== tag) {
        flushList();
        list = el(tag);
      }
      list.append(el("li", inlineMarkdown((bullet || ordered)[1])));
      continue;
    }
    if (line.startsWith(">")) {
      flushParagraph();
      flushList();
      root.append(el("blockquote", inlineMarkdown(line.replace(/^>\s?/, ""))));
      continue;
    }
    paragraph.push(line);
  }
  flushParagraph();
  flushList();
  if (code) root.append(el("pre", el("code", code.join("\n"))));
  return root;
}

function tableCells(line) {
  const value = String(line || "").trim();
  if (!value.includes("|")) return null;
  const unwrapped = value.replace(/^\|/, "").replace(/\|$/, "");
  const cells = unwrapped.split("|").map((cell) => cell.trim());
  return cells.length > 1 && cells.every((cell) => cell !== "") ? cells : null;
}

function isTableSeparator(cells) {
  return cells.every((cell) => /^:?-{3,}:?$/.test(cell));
}

function chatBubble(message) {
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

export function chatPage() {
  const root = el("div.chat-page");
  const sessionList = el("div.chat-session-list");
  const transcript = el("div.chat-transcript");
  const composer = el("textarea.chat-composer", {
    rows: 3,
    placeholder: "Message Archie…  (commands like /status and /new work here)",
    "aria-label": "Message Archie",
  });
  const send = el("button.btn.btn-primary", "Send");
  const stop = el("button.btn", { disabled: true }, "Stop");
  const status = el("span.chat-status", "Ready");
  const persona = el("select.chat-select", { "aria-label": "Personality" });
  const provider = el("select.chat-select", { "aria-label": "Provider", hidden: true });
  const model = el("select.chat-select", { "aria-label": "Model" });
  const restart = el("button.btn", { hidden: true, onclick: () => {
    if (!window.confirm("Reload the Telegram chat adapter?")) return;
    composer.value = "/restart";
    sendMessage();
  } }, "Reload Telegram");
  let currentSession = "";
  let sessions = [];
  let sending = false;
  let activeController = null;
  let updateSnapshot = null;
  let selectorData = {};
  let commandSpecs = [];
  let commandMatches = [];
  let commandSelection = 0;

  const emptyState = el(
    "div.chat-empty-state",
    el("span.chat-empty-mark", "A"),
    el("h2", "What are we working on?"),
    el("p", "Ask Archie anything, or start with one of these prompts."),
    el("div.chat-prompt-grid",
      ...["Summarise active tasks", "Show running agents", "Check system status"].map((prompt) =>
        el("button.chat-prompt", { onclick: () => { composer.value = prompt; composer.focus(); } }, prompt)),
    ),
  );

  function appendMessage(message) {
    emptyState.remove();
    transcript.append(chatBubble(message));
    transcript.scrollTop = transcript.scrollHeight;
  }

  function renderSessions() {
    sessionList.replaceChildren();
    sessionTools.hidden = !currentSession;
    if (!sessions.length) {
      sessionList.append(el("div.chat-empty", "Your conversations will appear here."));
      return;
    }
    for (const session of sessions) {
      const item = el(
        "button.chat-session",
        { class: currentSession === session.session_id ? "active" : "", onclick: () => selectSession(session.session_id) },
        el("strong", sessionTitle(session)),
        el("span", new Date(session.last_active_at).toLocaleString()),
      );
      sessionList.append(item);
    }
  }

  function renderSelectors(data) {
    selectorData = data;
    persona.replaceChildren(el("option", { value: "" }, "Personality"));
    for (const name of data.personas || []) persona.append(el("option", { value: name }, name));
    persona.value = data.active_persona || "";
    const providers = data.providers || [];
    provider.hidden = providers.length === 0;
    provider.replaceChildren(el("option", { value: "" }, "Provider"));
    for (const name of providers) provider.append(el("option", { value: name }, name));
    provider.value = data.active_provider || providers[0] || "";
    renderModelsForProvider(provider.value);
    restart.hidden = !data.restart_available;
    dangerousPanel.hidden = !data.dangerous_available;
    if (data.dangerous_available) refreshDangerous();
  }

  function renderModelsForProvider(providerName) {
    const grouped = selectorData.models_by_provider || {};
    const names = providerName && grouped[providerName] ? grouped[providerName] : selectorData.models || [];
    model.replaceChildren(el("option", { value: "" }, "Model"));
    for (const name of names) model.append(el("option", { value: name }, name));
    model.value = selectorData.active_model || "";
  }

  function renderCommands(data) {
    const commands = data.commands || [];
    commandSpecs = commands.map((item) => typeof item === "string" ? { command: item, usage: item, description: "" } : item);
    const list = el("div.chat-command-list");
    if (!commands.length) list.append(el("span", "Send a message to begin."));
    for (const item of commands) {
      const spec = typeof item === "string" ? { command: item, usage: item, description: "" } : item;
      list.append(el("div.chat-command-item", el("code", spec.usage || spec.command), el("span", spec.description || "")));
    }
    commandHelp.replaceChildren(el("summary", `Available commands (${commands.length})`), list);
  }

  const updatePanel = el("div.chat-update-panel");
  const dangerousPanel = el("div.chat-dangerous-panel", { hidden: true });
  const commandMenu = el("div.chat-command-menu", { hidden: true, role: "listbox", "aria-label": "Commands" });
  const sessionTools = el("details.chat-session-tools", { hidden: true });

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
        "aria-selected": index === commandSelection,
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

  function renderUpdate(data) {
    updatePanel.replaceChildren();
    updateSnapshot = data?.snapshot || null;
    const available = data?.available || [];
    if (!available.length) {
      updatePanel.append(el("span", data?.snapshot?.deferred ? "Update deferred." : "Archie is up to date."));
      return;
    }
    updatePanel.append(
      el("div", el("strong", "Update available"), el("span", available.map((component) => `${component.Label || component.label}: ${component.Available || component.available}`).join(" · "))),
      el("div.chat-update-actions",
        el("button.btn", { onclick: async () => { await api.chatUpdateDefer(updateSnapshot); renderUpdate({ snapshot: { deferred: true }, available: [] }); status.textContent = "Update deferred"; } }, "Defer"),
        data.can_install ? el("button.btn.btn-primary", { onclick: async () => { status.textContent = "Installing…"; await api.chatUpdateInstall(updateSnapshot); status.textContent = "Update started"; refreshUpdate(); } }, "Install update") : null,
      ),
    );
  }

  async function refreshUpdate() {
    try {
      renderUpdate(await api.chatUpdate());
    } catch (err) {
      updatePanel.replaceChildren(el("span", err.message || "Updates unavailable"));
    }
  }

  function dangerousActionButton(id, decision, label) {
    return el("button.btn", { onclick: async () => {
      try {
        await api.chatDangerousDecision(id, decision);
        status.textContent = decision === "deny" ? "Action denied" : "Action approved";
        await refreshDangerous();
      } catch (err) {
        status.textContent = err.message || "Approval failed";
      }
    } }, label);
  }

  function renderDangerous(data) {
    dangerousPanel.replaceChildren();
    const checkpoints = data?.checkpoints || [];
    const pending = data?.pending || [];
    const checkpoint = el("select.chat-select", { "aria-label": "Checkpoint" },
      el("option", { value: "" }, "Select checkpoint"),
      ...checkpoints.map((item) => el("option", { value: item.Number ?? item.number }, `${item.Number ?? item.number} — ${item.Label ?? item.label ?? "checkpoint"}`)),
    );
    const stopSpec = el("input.chat-dangerous-input", { placeholder: "Process name or id", "aria-label": "Process name or id" });
    const request = (kind, spec) => async () => {
      if (!String(spec.value || spec).trim()) return;
      try {
        await api.chatDangerousRequest(kind, String(spec.value || spec).trim());
        status.textContent = "Approval requested";
        await refreshDangerous();
      } catch (err) {
        status.textContent = err.message || "Could not request action";
      }
    };
    const pendingRows = pending.length ? pending.map((action) => el("div.chat-dangerous-pending",
      el("span", action.description),
      el("div.chat-update-actions", dangerousActionButton(action.id, "approve", "Approve"), dangerousActionButton(action.id, "permanent", "Approve for 24h"), dangerousActionButton(action.id, "deny", "Deny")),
    )) : [el("span.chat-hint", "No pending dangerous actions.")];
    dangerousPanel.append(
      el("div.chat-dangerous-head", el("strong", "Dangerous actions"), el("span.chat-hint", "Every action requires approval.")),
      el("div.chat-dangerous-controls",
        el("div", checkpoint, el("button.btn", { onclick: request("rollback", checkpoint) }, "Request rollback")),
        el("div", stopSpec, el("button.btn", { onclick: request("stop", stopSpec) }, "Request stop")),
      ),
      el("div.chat-dangerous-pending-list", ...pendingRows),
    );
  }

  async function refreshDangerous() {
    try {
      renderDangerous(await api.chatDangerous());
    } catch (err) {
      dangerousPanel.replaceChildren(el("span.chat-hint", err.message || "Dangerous actions unavailable"));
    }
  }

  async function refreshSessions() {
    const data = await api.chatSessions();
    sessions = data.sessions || [];
    renderSelectors(data);
    renderCommands(data);
    renderSessions();
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
    renderSessions();
    transcript.replaceChildren();
    await api.chatMessage(channelID(), `/resume ${id}`);
    persona.value = selectorData.active_personas?.[id] || selectorData.active_persona || "";
    const messages = await api.chatMessages(id);
    for (const message of messages) appendMessage(message);
    if (!transcript.children.length) transcript.append(emptyState);
  }

  async function sendMessage() {
    const text = composer.value.trim();
    if (!text || sending) return;
    hideCommandMenu();
    sending = true;
    send.disabled = true;
    stop.disabled = false;
    composer.disabled = true;
    status.textContent = "Thinking…";
    appendMessage({ from: "web", text });
    composer.value = "";
    const replyBubble = el("div.chat-bubble-row.assistant", el("div.chat-bubble", el("div.chat-bubble-meta", "Archie"), el("div.chat-bubble-text")));
    transcript.append(replyBubble);
      const replyText = replyBubble.querySelector(".chat-bubble-text");
    let timeout;
    try {
      activeController = new AbortController();
      timeout = setTimeout(() => activeController.abort(), 120000);
      const response = await fetch("/api/chat/stream", {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "text/event-stream" },
        body: JSON.stringify({ channel_id: channelID(), source_id: crypto.randomUUID(), text }),
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
          if (event.type === "delta") replyText.textContent += event.text;
          if (event.type === "done" && !replyText.textContent) replyText.textContent = event.text;
          if (event.type === "error") throw new Error(event.text);
        }
        if (done) break;
      }
      if (replyText.isConnected) replyText.replaceWith(renderMarkdown(replyText.textContent));
      status.textContent = "Ready";
      await refreshSessions();
      if (currentSession) await selectSession(currentSession);
    } catch (err) {
      if (err.name === "AbortError") {
        replyText.textContent = "Turn stopped.";
        status.textContent = "Stopped";
      } else {
        replyText.textContent = `Unable to complete that turn: ${err.message || err}`;
        status.textContent = "Error";
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

  send.onclick = sendMessage;
  stop.onclick = () => {
    const sessionID = currentSession;
    status.textContent = "Stopping…";
    if (sessionID) void api.chatCancel(sessionID).catch(() => {});
    activeController?.abort();
  };
  composer.oninput = renderCommandMenu;
  composer.onkeydown = (event) => {
    if (!commandMenu.hidden && commandMatches.length) {
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
      if (event.key === "Enter" && !event.ctrlKey && !event.metaKey) {
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
    if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
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
    renderModelsForProvider(provider.value);
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

  const commandHelp = el("details.chat-command-help");
  const checkUpdates = el("button.btn", { onclick: refreshUpdate }, "Check updates");
  const sessionActions = el(
    "div.chat-session-tool-list",
    el("button.btn", { type: "button", onclick: () => runSessionCommand("/retry") }, "Retry"),
    el("button.btn", { type: "button", onclick: () => runSessionCommand("/undo") }, "Undo"),
    el("button.btn", { type: "button", onclick: () => promptSessionCommand("/title", "Session title") }, "Rename"),
    el("button.btn", { type: "button", onclick: () => promptSessionCommand("/branch", "Branch name (optional)") }, "Branch"),
    el("button.btn", { type: "button", onclick: () => runSessionCommand("/compress --preview") }, "Preview compression"),
  );
  sessionTools.append(el("summary", "Session actions"), sessionActions);
  const chatTopbar = el(
    "div.chat-topbar",
    el("div", el("span.chat-kicker", "ARCHIE WORKSPACE"), el("h1.page-title", "Chat")),
    el("div.chat-topbar-actions", el("span.chat-status", status), restart, checkUpdates, persona, provider, model),
  );
  const chatLayout = el(
    "div.chat-layout",
    el("aside.chat-sidebar", el("div.chat-sidebar-head", el("strong", "Conversations"), el("button.btn.btn-primary", { onclick: () => { composer.value = "/new"; sendMessage(); } }, "+ New chat")), sessionList),
    el(
      "section.chat-workspace",
      el("div.chat-workspace-head", el("span.chat-workspace-label", "PERSONAL ASSISTANT"), commandHelp, sessionTools),
      transcript,
      el("div.chat-compose", el("div.chat-composer-shell", composer, commandMenu, el("div.chat-compose-actions", el("span.chat-hint", "⌘/Ctrl + Enter to send"), stop, send))),
    ),
  );
  mount(root, el(
    "div.chat-page-content",
    chatTopbar,
    updatePanel,
    dangerousPanel,
    chatLayout,
  ));

  transcript.append(emptyState);

  refreshSessions().catch((err) => { status.textContent = err.message || "Chat unavailable"; });
  refreshUpdate();
  return root;
}
