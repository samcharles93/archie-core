import { h, Fragment, render } from "preact";
import { useState, useEffect, useRef } from "preact/hooks";
import { api } from "../base/api.jsx";
import { renderMarkdown } from "./markdown.jsx";
import { appendToolCall, appendNavigateChip } from "./chat-tools.jsx";
import { channelID } from "./chat-state.jsx";
import { newChatTurn, retryChatTurn, resolveTurn } from "./chat-retry.jsx";
import { sessionTitle } from "./chat-render.jsx";
import { updateUnavailableMessage } from "./chat-update-status.jsx";

import "./chat.css";

function currentPage() {
  return location.hash.slice(1) || "/";
}

function DOMWrap({ node }) {
  const ref = useRef(null);
  useEffect(() => {
    if (ref.current && node) {
      ref.current.replaceChildren(node);
    }
  }, [node]);
  return <div ref={ref} style={{ display: "contents" }} />;
}

function MarkdownView({ text }) {
  const ref = useRef(null);
  useEffect(() => {
    if (ref.current) {
      ref.current.replaceChildren(renderMarkdown(text || ""));
    }
  }, [text]);
  return <div ref={ref} style={{ display: "contents" }} />;
}

export function ChatApp() {
  const [sessions, setSessions] = useState([]);
  const [currentSession, setCurrentSession] = useState("");
  const [messages, setMessages] = useState([]);
  const [composerText, setComposerText] = useState("");
  const [statusText, setStatusText] = useState("Ready");
  const [isSending, setIsSending] = useState(false);
  
  const [selectorData, setSelectorData] = useState({});
  const [selectedPersona, setSelectedPersona] = useState("");
  const [selectedProvider, setSelectedProvider] = useState("");
  const [selectedModel, setSelectedModel] = useState("");
  
  const [commandSpecs, setCommandSpecs] = useState([]);
  const [commandMatches, setCommandMatches] = useState([]);
  const [commandSelection, setCommandSelection] = useState(0);
  const [isCommandMenuOpen, setIsCommandMenuOpen] = useState(false);
  
  const [updateData, setUpdateData] = useState(null);
  const [updateSnapshot, setUpdateSnapshot] = useState(null);
  
  const [dangerousData, setDangerousData] = useState(null);
  const [dangerousCheckpoint, setDangerousCheckpoint] = useState("");
  const [dangerousStopSpec, setDangerousStopSpec] = useState("");
  
  const [streamingTurn, setStreamingTurn] = useState(null);
  // streamingTurn: { text: string, tools: [], isError: bool, turn: object, isRetry: bool }
  
  const transcriptRef = useRef(null);
  const composerRef = useRef(null);
  const activeControllerRef = useRef(null);
  const sessionToolsRef = useRef(null);
  const commandMenuRef = useRef(null);
  const isSendingRef = useRef(isSending);
  isSendingRef.current = isSending;

  // Auto scroll transcript to bottom on new messages or streaming tokens
  useEffect(() => {
    if (transcriptRef.current) {
      transcriptRef.current.scrollTop = transcriptRef.current.scrollHeight;
    }
  }, [messages, streamingTurn]);

  useEffect(() => {
    refreshSessions().catch((err) => {
      setStatusText(err.message || "Chat unavailable");
    });
    refreshUpdate();

    const onTeardown = () => {
      activeControllerRef.current?.abort();
    };
    window.addEventListener("archie:teardown", onTeardown);
    return () => {
      window.removeEventListener("archie:teardown", onTeardown);
      activeControllerRef.current?.abort();
    };
  }, []);

  async function refreshUpdate() {
    try {
      const data = await api.chatUpdate();
      setUpdateSnapshot(data?.snapshot || null);
      setUpdateData(data);
    } catch (err) {
      const message = updateUnavailableMessage(err);
      if (message) {
        setUpdateData({ error: message });
      } else {
        setUpdateData(null);
      }
    }
  }

  async function deferUpdate() {
    if (!updateSnapshot) return;
    try {
      await api.chatUpdateDefer(updateSnapshot);
      setUpdateData({ snapshot: { deferred: true }, available: [] });
      setStatusText("Update deferred");
    } catch (err) {
      setStatusText(err.message || "Could not defer update");
    }
  }

  async function installUpdate() {
    if (!updateSnapshot) return;
    try {
      setStatusText("Installing…");
      const result = await api.chatUpdateInstall(updateSnapshot);
      setUpdateSnapshot(null);
      setUpdateData({ snapshot: {}, available: [] });
      setStatusText(result?.result?.restart_requested ? "Update installed; restart queued" : "Update complete");
    } catch (err) {
      setStatusText(err.message || "Update install failed");
    }
  }

  async function refreshDangerous() {
    try {
      const data = await api.chatDangerous();
      setDangerousData(data);
    } catch (err) {
      setDangerousData({ error: err.message || "Dangerous actions unavailable" });
    }
  }

  async function requestDangerousAction(kind, spec) {
    if (!spec) return;
    try {
      await api.chatDangerousRequest(kind, spec);
      setStatusText("Approval requested");
      await refreshDangerous();
    } catch (err) {
      setStatusText(err.message || "Could not request action");
    }
  }

  async function decideDangerousAction(id, decision) {
    try {
      await api.chatDangerousDecision(id, decision);
      setStatusText(decision === "deny" ? "Action denied" : "Action approved");
      await refreshDangerous();
    } catch (err) {
      setStatusText(err.message || "Approval failed");
    }
  }

  async function refreshSessions() {
    const data = await api.chatSessions();
    const sessionList = data.sessions || [];
    setSessions(sessionList);
    setSelectorData(data);
    
    setSelectedPersona(data.active_persona || "");
    const provs = data.providers || [];
    const activeProv = data.active_provider || provs[0] || "";
    setSelectedProvider(activeProv);
    setSelectedModel(data.active_model || "");
    
    const specs = (data.commands || []).map((item) =>
      typeof item === "string" ? { command: item, usage: item, description: "" } : item
    );
    setCommandSpecs(specs);

    if (data.dangerous_available) {
      await refreshDangerous();
    }

    if (!currentSession && sessionList[0]) {
      await selectSession(sessionList[0].session_id, data, sessionList);
    }
  }

  async function selectSession(id, dataOverride, listOverride) {
    const data = dataOverride || selectorData;
    setCurrentSession(id);
    setMessages([]);
    setStreamingTurn(null);

    const activePersona = data.active_personas?.[id] || data.active_persona || "";
    setSelectedPersona(activePersona);

    try {
      await api.chatMessage(channelID(), `/resume ${id}`);
      const [msgs, turns] = await Promise.all([api.chatMessages(id), api.chatTurns(id)]);
      const byAssistantMessage = new Map();
      for (const turn of turns || []) {
        const assistantID = turn.assistant_message_id || turn.AssistantMessageID;
        if (assistantID) byAssistantMessage.set(assistantID, turn);
      }
      const hydrated = (msgs || []).map((message) => {
        const messageID = message.message_id || message.MessageID;
        const turn = byAssistantMessage.get(messageID);
        return turn ? { ...message, tool_calls: turn.tool_calls || turn.ToolCalls || [] } : message;
      });
      setMessages(hydrated);
    } catch (err) {
      setStatusText(err.message || "Could not load session messages");
    }
  }

  async function runSessionCommand(command) {
    if (!currentSession) return;
    try {
      setStatusText("Working…");
      const result = await api.chatMessage(channelID(), command);
      if (result.session_id) setCurrentSession(result.session_id);
      setStatusText(result.reply || "Session updated");
      await refreshSessions();
      if (result.session_id || currentSession) {
        await selectSession(result.session_id || currentSession);
      }
      if (sessionToolsRef.current) sessionToolsRef.current.open = false;
    } catch (err) {
      setStatusText(err.message || "Session action failed");
    }
  }

  function promptSessionCommand(command, message) {
    const value = window.prompt(message);
    if (value?.trim()) runSessionCommand(`${command} ${value.trim()}`);
  }

  function handlePersonaChange(e) {
    const val = e.target.value;
    setSelectedPersona(val);
    if (!currentSession || !val) return;
    api.chatPersona(currentSession, val).then(() => {
      setSelectorData((prev) => ({
        ...prev,
        active_personas: { ...(prev.active_personas || {}), [currentSession]: val },
      }));
      setStatusText(`Personality: ${val}`);
    }).catch((err) => {
      setStatusText(err.message || "Failed to set personality");
    });
  }

  async function handleProviderChange(e) {
    const prov = e.target.value;
    setSelectedProvider(prov);
    if (!prov) return;
    try {
      const result = await api.chatMessage(channelID(), `/model --provider ${prov}`);
      setStatusText(result.reply || "Provider updated");
      await refreshSessions();
    } catch (err) {
      setStatusText(`Provider update failed: ${err.message || err}`);
    }
  }

  async function handleModelChange(e) {
    const mod = e.target.value;
    setSelectedModel(mod);
    if (!mod) return;
    try {
      const result = await api.chatMessage(channelID(), `/model ${mod}`);
      setStatusText(result.reply || "Model updated");
      await refreshSessions();
    } catch (err) {
      setStatusText(`Model update failed: ${err.message || err}`);
    }
  }

  function handleRestartTelegram() {
    if (!window.confirm("Reload the Telegram chat adapter?")) return;
    setComposerText("/restart");
    sendMessage({ textOverride: "/restart" });
  }

  function handleNewChat() {
    setComposerText("/new");
    sendMessage({ textOverride: "/new" });
  }

  function handleComposerInput(e) {
    const val = e.target.value;
    setComposerText(val);

    const before = val.slice(0, e.target.selectionStart);
    const match = before.match(/(?:^|\s)\/([^\s]*)$/);
    if (match) {
      const query = match[1].toLowerCase();
      const matches = commandSpecs.filter((spec) => {
        const haystack = `${spec.command} ${spec.description}`.toLowerCase();
        return haystack.includes(query);
      }).slice(0, 8);
      if (matches.length) {
        setCommandMatches(matches);
        setCommandSelection(0);
        setIsCommandMenuOpen(true);
        return;
      }
    }
    setIsCommandMenuOpen(false);
    setCommandMatches([]);
  }

  function chooseCommand(index) {
    const spec = commandMatches[index];
    if (!spec || !composerRef.current) return;
    const input = composerRef.current;
    const before = input.value.slice(0, input.selectionStart);
    const after = input.value.slice(input.selectionStart);
    const start = before.search(/(?:^|\s)\/[^\s]*$/);
    const tokenStart = start < 0 ? before.length : start + (before[start] === " " ? 1 : 0);
    const nextVal = `${before.slice(0, tokenStart)}${spec.command} ${after}`;
    setComposerText(nextVal);
    setIsCommandMenuOpen(false);
    const cursor = tokenStart + spec.command.length + 1;
    setTimeout(() => {
      input.focus();
      input.setSelectionRange(cursor, cursor);
    }, 0);
  }

  function handleComposerKeyDown(event) {
    if (isCommandMenuOpen && commandMatches.length) {
      if (
        event.key === "Enter" &&
        !event.shiftKey &&
        !event.ctrlKey &&
        !event.metaKey &&
        commandMatches.some((spec) => spec.command === composerText.trim())
      ) {
        event.preventDefault();
        setIsCommandMenuOpen(false);
        sendMessage();
        return;
      }
      if (event.key === "ArrowDown") {
        event.preventDefault();
        setCommandSelection((prev) => (prev + 1) % commandMatches.length);
        return;
      }
      if (event.key === "ArrowUp") {
        event.preventDefault();
        setCommandSelection((prev) => (prev + commandMatches.length - 1) % commandMatches.length);
        return;
      }
      if (event.key === "Enter" && !event.shiftKey && !event.ctrlKey && !event.metaKey) {
        event.preventDefault();
        chooseCommand(commandSelection);
        return;
      }
      if (event.key === "Escape") {
        event.preventDefault();
        setIsCommandMenuOpen(false);
        return;
      }
    }
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      sendMessage();
    }
  }

  async function sendMessage(retryOpts = null) {
    const explicitText = retryOpts?.textOverride;
    const rawVal = explicitText !== undefined ? explicitText : composerText;
    const { text, turn, isRetry } = resolveTurn(retryOpts, rawVal);
    if (!text || isSendingRef.current) return;

    setIsCommandMenuOpen(false);
    setIsSending(true);
    setStatusText(isRetry ? "Retrying…" : "Thinking…");

    if (!isRetry) {
      setMessages((prev) => [...prev, { from: "web", text }]);
      setComposerText("");
    }

    let activeTools = [];
    setStreamingTurn({ text: "", tools: [], isError: false, turn, isRetry });

    let streamedText = "";
    let finished = false;
    let timedOut = false;
    let timeoutId = null;

    try {
      const controller = new AbortController();
      activeControllerRef.current = controller;
      timeoutId = setTimeout(() => {
        timedOut = true;
        controller.abort();
      }, 120000);

      const response = await fetch("/api/chat/stream", {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "text/event-stream" },
        body: JSON.stringify({
          channel_id: channelID(),
          source_id: turn.sourceID,
          text: turn.text,
          page: currentPage(),
        }),
        signal: controller.signal,
      });

      if (!response.ok) {
        throw new Error((await response.text()) || `${response.status} ${response.statusText}`);
      }

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
          if (event.session_id) setCurrentSession(event.session_id);
          if (event.type === "delta") {
            streamedText += event.text;
            setStreamingTurn((prev) => (prev ? { ...prev, text: streamedText } : null));
          }
          if (event.type === "tool") {
            activeTools = [...activeTools, event];
            setStreamingTurn((prev) => (prev ? { ...prev, tools: activeTools } : null));
          }
          if (event.type === "navigate") {
            activeTools = [...activeTools, event];
            setStreamingTurn((prev) => (prev ? { ...prev, tools: activeTools } : null));
          }
          if (event.type === "done") {
            finished = true;
            if (!streamedText) {
              streamedText = event.text;
            }
          }
          if (event.type === "error") throw new Error(event.text);
        }
        if (done) break;
      }

      if (!finished) throw new Error("chat stream ended before completion");

      // Commit finished message into transcript
      setMessages((prev) => [
        ...prev,
        {
          from: "assistant",
          text: streamedText,
          tool_calls: activeTools,
        },
      ]);
      setStreamingTurn(null);
      setStatusText("Ready");
      await refreshSessions();
    } catch (err) {
      if (err.name === "AbortError" && !timedOut) {
        setStreamingTurn({
          text: "Turn stopped.",
          tools: activeTools,
          isError: false,
          turn,
          isRetry,
        });
        setStatusText("Stopped");
      } else {
        const failedText = `Unable to complete that turn: ${err.message || err}`;
        setStreamingTurn({
          text: failedText,
          tools: activeTools,
          isError: true,
          turn: retryChatTurn(turn),
          isRetry,
        });
        setStatusText("Error — retry available");
      }
    } finally {
      clearTimeout(timeoutId);
      activeControllerRef.current = null;
      setIsSending(false);
      if (composerRef.current) composerRef.current.focus();
    }
  }

  function handleStop() {
    setStatusText("Stopping…");
    if (currentSession) {
      void api.chatCancel(currentSession).catch(() => {});
    }
    activeControllerRef.current?.abort();
  }

  const groupedModels = selectorData.models_by_provider || {};
  const currentModels =
    selectedProvider && groupedModels[selectedProvider]
      ? groupedModels[selectedProvider]
      : selectorData.models || [];

  return (
    <div className="chat-page-content">
      <header className="chat-topbar">
        <div>
          <span className="chat-kicker">ARCHIE WORKSPACE</span>
          <h1 className="page-title">Chat</h1>
        </div>
        <div className="chat-topbar-actions">
          <span className="chat-status">{statusText}</span>
          {selectorData.restart_available && (
            <button className="btn" type="button" onClick={handleRestartTelegram}>
              Reload Telegram
            </button>
          )}
          <button className="btn" type="button" onClick={refreshUpdate}>
            Check updates
          </button>
          <select
            className="chat-select"
            aria-label="Personality"
            value={selectedPersona}
            onChange={handlePersonaChange}
          >
            <option value="">Personality</option>
            {(selectorData.personas || []).map((name) => (
              <option key={name} value={name}>
                {name}
              </option>
            ))}
          </select>
          {(selectorData.providers || []).length > 0 && (
            <select
              className="chat-select"
              aria-label="Provider"
              value={selectedProvider}
              onChange={handleProviderChange}
            >
              <option value="">Provider</option>
              {(selectorData.providers || []).map((p) => (
                <option key={p} value={p}>
                  {p}
                </option>
              ))}
            </select>
          )}
          <select
            className="chat-select"
            aria-label="Model"
            value={selectedModel}
            onChange={handleModelChange}
          >
            <option value="">Model</option>
            {currentModels.map((m) => (
              <option key={m} value={m}>
                {m}
              </option>
            ))}
          </select>
        </div>
      </header>

      {/* Update Panel */}
      {updateData && (
        <div className="chat-update-panel">
          {updateData.error ? (
            <span>{updateData.error}</span>
          ) : updateData.available?.length ? (
            <Fragment>
              <div>
                <strong>Update available</strong>
                <span>
                  {updateData.available
                    .map((c) => `${c.Label || c.label}: ${c.Available || c.available}`)
                    .join(" · ")}
                </span>
              </div>
              <div className="chat-update-actions">
                <button className="btn" type="button" onClick={deferUpdate}>
                  Defer
                </button>
                {updateData.can_install && (
                  <button className="btn btn-primary" type="button" onClick={installUpdate}>
                    Install update
                  </button>
                )}
              </div>
            </Fragment>
          ) : (
            <span>{updateData.snapshot?.deferred ? "Update deferred." : "Archie is up to date."}</span>
          )}
        </div>
      )}

      {/* Dangerous Panel */}
      {selectorData.dangerous_available && dangerousData && (
        <div className="chat-dangerous-panel">
          <div className="chat-dangerous-head">
            <strong>Dangerous actions</strong>
            <span className="chat-hint">Every action requires approval.</span>
          </div>
          <div className="chat-dangerous-controls">
            <div>
              <select
                className="chat-select"
                aria-label="Checkpoint"
                value={dangerousCheckpoint}
                onChange={(e) => setDangerousCheckpoint(e.target.value)}
              >
                <option value="">Select checkpoint</option>
                {(dangerousData.checkpoints || []).map((cp) => (
                  <option key={cp.Number ?? cp.number} value={cp.Number ?? cp.number}>
                    {cp.Number ?? cp.number} — {cp.Label ?? cp.label ?? "checkpoint"}
                  </option>
                ))}
              </select>
              <button
                className="btn"
                type="button"
                onClick={() => requestDangerousAction("rollback", dangerousCheckpoint)}
              >
                Request rollback
              </button>
            </div>
            <div>
              <input
                className="chat-dangerous-input"
                placeholder="Process name or id"
                aria-label="Process name or id"
                value={dangerousStopSpec}
                onInput={(e) => setDangerousStopSpec(e.target.value)}
              />
              <button
                className="btn"
                type="button"
                onClick={() => requestDangerousAction("stop", dangerousStopSpec)}
              >
                Request stop
              </button>
            </div>
          </div>
          <div className="chat-dangerous-pending-list">
            {(dangerousData.pending || []).length ? (
              dangerousData.pending.map((action) => (
                <div className="chat-dangerous-pending" key={action.id}>
                  <span>{action.description}</span>
                  <div className="chat-update-actions">
                    <button
                      className="btn"
                      type="button"
                      onClick={() => decideDangerousAction(action.id, "approve")}
                    >
                      Approve
                    </button>
                    <button
                      className="btn"
                      type="button"
                      onClick={() => decideDangerousAction(action.id, "permanent")}
                    >
                      Approve for 24h
                    </button>
                    <button
                      className="btn"
                      type="button"
                      onClick={() => decideDangerousAction(action.id, "deny")}
                    >
                      Deny
                    </button>
                  </div>
                </div>
              ))
            ) : (
              <span className="chat-hint">No pending dangerous actions.</span>
            )}
          </div>
        </div>
      )}

      {/* Main Layout */}
      <div className="chat-layout">
        <aside className="chat-sidebar">
          <div className="chat-sidebar-head">
            <strong>Conversations</strong>
            <button className="btn btn-primary" type="button" onClick={handleNewChat}>
              + New chat
            </button>
          </div>
          <div className="chat-session-list">
            {!sessions.length ? (
              <div className="chat-empty">Your conversations will appear here.</div>
            ) : (
              sessions.map((session) => (
                <button
                  key={session.session_id}
                  className={`chat-session ${currentSession === session.session_id ? "active" : ""}`}
                  type="button"
                  onClick={() => selectSession(session.session_id)}
                >
                  <strong>{sessionTitle(session)}</strong>
                  <span>{new Date(session.last_active_at).toLocaleString()}</span>
                </button>
              ))
            )}
          </div>
        </aside>

        <section className="chat-workspace">
          <div className="chat-workspace-head">
            <span className="chat-workspace-label">PERSONAL ASSISTANT</span>
            <details className="chat-command-help">
              <summary>Available commands ({commandSpecs.length})</summary>
              <div className="chat-command-list">
                {!commandSpecs.length ? (
                  <span>Send a message to begin.</span>
                ) : (
                  commandSpecs.map((spec) => (
                    <div className="chat-command-item" key={spec.command}>
                      <code>{spec.usage || spec.command}</code>
                      <span>{spec.description || ""}</span>
                    </div>
                  ))
                )}
              </div>
            </details>
            <details
              className="chat-session-tools"
              ref={sessionToolsRef}
              hidden={!currentSession}
            >
              <summary>Session actions</summary>
              <div className="chat-session-tool-list">
                <button
                  className="btn"
                  type="button"
                  onClick={() => runSessionCommand("/retry")}
                >
                  Retry
                </button>
                <button
                  className="btn"
                  type="button"
                  onClick={() => runSessionCommand("/undo")}
                >
                  Undo
                </button>
                <button
                  className="btn"
                  type="button"
                  onClick={() => promptSessionCommand("/title", "Session title")}
                >
                  Rename
                </button>
                <button
                  className="btn"
                  type="button"
                  onClick={() => promptSessionCommand("/branch", "Branch name (optional)")}
                >
                  Branch
                </button>
                <button
                  className="btn"
                  type="button"
                  onClick={() => runSessionCommand("/compress --preview")}
                >
                  Preview compression
                </button>
              </div>
            </details>
          </div>

          {/* Transcript */}
          <div className="chat-transcript" ref={transcriptRef}>
            {!messages.length && !streamingTurn ? (
              <div className="chat-empty-state">
                <span className="chat-empty-mark">A</span>
                <h2>What are we working on?</h2>
                <p>Ask Archie anything, or start with one of these prompts.</p>
                <div className="chat-prompt-grid">
                  <button
                    className="chat-prompt"
                    type="button"
                    onClick={() => {
                      setComposerText("Summarise active tasks");
                      composerRef.current?.focus();
                    }}
                  >
                    Summarise active tasks
                  </button>
                  <button
                    className="chat-prompt"
                    type="button"
                    onClick={() => {
                      setComposerText("Show running agents");
                      composerRef.current?.focus();
                    }}
                  >
                    Show running agents
                  </button>
                  <button
                    className="chat-prompt"
                    type="button"
                    onClick={() => {
                      setComposerText("Check system status");
                      composerRef.current?.focus();
                    }}
                  >
                    Check system status
                  </button>
                </div>
              </div>
            ) : (
              <Fragment>
                {messages.map((message, idx) => {
                  const from = message.from ?? message.From ?? "";
                  const text = message.text ?? message.Text ?? "";
                  const isAssistant = from !== "web";
                  const tools = message.tool_calls || message.ToolCalls || [];

                  return (
                    <div
                      key={idx}
                      className={`chat-bubble-row ${isAssistant ? "assistant" : "user"}`}
                    >
                      <div className="chat-bubble">
                        <div className="chat-bubble-meta">{isAssistant ? "Archie" : "You"}</div>
                        {isAssistant ? (
                          <Fragment>
                            {tools.length > 0 && (
                              <div className="chat-tools">
                                {tools.map((tool, tIdx) => {
                                  if (tool.path) {
                                    return (
                                      <a
                                        key={tIdx}
                                        className="chat-nav-chip"
                                        href={`#${tool.path}`}
                                        title={`Go to ${tool.label || tool.path}`}
                                      >
                                        <span className="chat-nav-chip-icon" aria-hidden="true">
                                          ↗
                                        </span>
                                        <span className="chat-nav-chip-label">
                                          {tool.label || tool.path}
                                        </span>
                                      </a>
                                    );
                                  }
                                  const name = tool.name || tool.Name || tool.tool || "tool";
                                  const parameters = tool.parameters || tool.Parameters || "";
                                  const summary =
                                    tool.summary ||
                                    tool.Summary ||
                                    tool.text ||
                                    tool.err ||
                                    tool.Err ||
                                    "done";
                                  const failed = Boolean(tool.err || tool.Err || tool.failed);
                                  return (
                                    <div
                                      key={tIdx}
                                      className={`chat-tool ${failed ? "failed" : ""}`}
                                    >
                                      <span className="chat-tool-icon">🔧</span>
                                      <span className="chat-tool-name">{name}</span>
                                      {parameters && (
                                        <code className="chat-tool-parameters">{parameters}</code>
                                      )}
                                      <span className="chat-tool-summary">{summary}</span>
                                    </div>
                                  );
                                })}
                              </div>
                            )}
                            <MarkdownView text={text} />
                          </Fragment>
                        ) : (
                          <div className="chat-bubble-text">{text}</div>
                        )}
                      </div>
                    </div>
                  );
                })}

                {/* Live streaming assistant turn */}
                {streamingTurn && (
                  <div className="chat-bubble-row assistant">
                    <div className="chat-bubble">
                      <div className="chat-bubble-meta">Archie</div>
                      {streamingTurn.tools?.length > 0 && (
                        <div className="chat-tools">
                          {streamingTurn.tools.map((tool, tIdx) => {
                            if (tool.path) {
                              return (
                                <a
                                  key={tIdx}
                                  className="chat-nav-chip"
                                  href={`#${tool.path}`}
                                  title={`Go to ${tool.label || tool.path}`}
                                >
                                  <span className="chat-nav-chip-icon" aria-hidden="true">
                                    ↗
                                  </span>
                                  <span className="chat-nav-chip-label">
                                    {tool.label || tool.path}
                                  </span>
                                </a>
                              );
                            }
                            const name = tool.name || tool.tool || "tool";
                            const parameters = tool.parameters || "";
                            const summary = tool.summary || tool.text || (tool.failed ? "failed" : "running…");
                            const failed = Boolean(tool.failed);
                            return (
                              <div
                                key={tIdx}
                                className={`chat-tool ${failed ? "failed" : ""}`}
                              >
                                <span className="chat-tool-icon">🔧</span>
                                <span className="chat-tool-name">{name}</span>
                                {parameters && (
                                  <code className="chat-tool-parameters">{parameters}</code>
                                )}
                                <span className="chat-tool-summary">{summary}</span>
                              </div>
                            );
                          })}
                        </div>
                      )}
                      <MarkdownView text={streamingTurn.text || "…"} />
                      {streamingTurn.isError && (
                        <button
                          className="btn chat-retry"
                          type="button"
                          onClick={() => sendMessage({ turn: streamingTurn.turn })}
                        >
                          Retry
                        </button>
                      )}
                    </div>
                  </div>
                )}
              </Fragment>
            )}
          </div>

          {/* Composer */}
          <div className="chat-compose">
            <div className="chat-composer-shell">
              <textarea
                ref={composerRef}
                className="chat-composer"
                rows={3}
                placeholder="Message Archie…  (commands like /status and /new work here)"
                aria-label="Message Archie"
                disabled={isSending}
                value={composerText}
                onInput={handleComposerInput}
                onKeyDown={handleComposerKeyDown}
              />
              {isCommandMenuOpen && commandMatches.length > 0 && (
                <div
                  className="chat-command-menu"
                  ref={commandMenuRef}
                  role="listbox"
                  aria-label="Commands"
                >
                  {commandMatches.map((spec, index) => (
                    <button
                      key={spec.command}
                      type="button"
                      role="option"
                      aria-selected={String(index === commandSelection)}
                      className="chat-command-option"
                      onMouseDown={(e) => e.preventDefault()}
                      onClick={() => chooseCommand(index)}
                    >
                      <code>{spec.command}</code>
                      <span>{spec.description || spec.usage || ""}</span>
                    </button>
                  ))}
                </div>
              )}
              <div className="chat-compose-actions">
                <span className="chat-hint">Enter to send · Shift+Enter for a new line</span>
                <button
                  className="btn"
                  type="button"
                  disabled={!isSending}
                  onClick={handleStop}
                >
                  Stop
                </button>
                <button
                  className="btn btn-primary"
                  type="button"
                  disabled={isSending || !composerText.trim()}
                  onClick={() => sendMessage()}
                >
                  Send
                </button>
              </div>
            </div>
          </div>
        </section>
      </div>
    </div>
  );
}

export function ChatPage() {
  return <ChatApp />;
}

export function chatPage() {
  const container = document.createElement("div");
  container.className = "chat-page";
  render(<ChatApp />, container);
  return container;
}
