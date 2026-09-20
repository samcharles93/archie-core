import { computed, onMounted, onUnmounted, ref, watch, type Ref } from "vue";

import { api } from "@/lib/api";
import { channelID } from "./channel";
import { matchesFor } from "./commands";
import { resolveTurn, retryChatTurn, type ChatTurn, type RetryOptions } from "./turn";

/**
 * The chat panel's shared state.
 *
 * The launcher and the panel are separate components and the panel survives a
 * navigation, so the conversation lives in a module rather than in either one.
 * Everything the server answers with is read here; the components decide how it
 * looks.
 */

/** One web conversation, as GET /api/chat/sessions lists it. */
export interface ChatSession {
  session_id: string;
  title?: string;
  branch_name?: string;
}

/** One slash command the server will accept. */
export interface ChatCommandSpec {
  command: string;
  usage?: string;
  description?: string;
}

/**
 * A tool the assistant called. A live frame names the tool in `tool` and
 * carries its outcome in `text`; a recorded turn uses `name` and `summary`.
 */
export interface ChatToolCall {
  id?: string;
  name?: string;
  tool?: string;
  parameters?: string;
  summary?: string;
  text?: string;
  failed?: boolean;
  /** A dashboard_navigate result: the page to route to, instead of a tool line. */
  path?: string;
  label?: string;
  // The same view reaches the browser from more than one composition, so an
  // outcome may arrive under either spelling. `err`/`Err` is also how a failed
  // call reports itself: it is a result, not a missing one.
  Name?: string;
  Parameters?: string;
  Summary?: string;
  Err?: string;
  err?: string;
}

/** One message in the transcript, from either side. */
export interface ChatMessage {
  from?: string;
  text?: string;
  tool_calls?: ChatToolCall[];
  // The same view reaches the browser from more than one composition, so both
  // spellings are read rather than assuming one serialiser.
  From?: string;
  Text?: string;
  ToolCalls?: ChatToolCall[];
  message_id?: string;
  MessageID?: string;
}

/** A turn in flight: what it is showing, and what to resend if it fails. */
export interface StreamingTurn {
  text: string;
  tools: ChatToolCall[];
  isError: boolean;
  turn?: ChatTurn;
  isRetry: boolean;
}

/** Everything the selectors on the panel read, from one sessions read. */
export interface ChatSelectorData {
  personas?: string[];
  providers?: string[];
  models?: string[];
  models_by_provider?: Record<string, string[]>;
  active_persona?: string;
  active_personas?: Record<string, string>;
  active_provider?: string;
  active_model?: string;
  commands?: Array<ChatCommandSpec | string>;
}

/**
 * The id the launcher points its `aria-controls` at and the panel answers to.
 * Both read it here so the pair cannot drift apart.
 */
export const CHAT_PANEL_ID = "chat-panel";

/**
 * One `data: {...}` frame of the chat stream. The server always emits `text`,
 * empty or not: the browser concatenates it, so a missing key would put the
 * string "undefined" into the transcript on a turn whose reply is empty.
 */
interface ChatStreamFrame extends ChatToolCall {
  type?: string;
  text: string;
  session_id?: string;
}

/** A turn that never settles must not hold the composer closed forever. */
const STREAM_TIMEOUT_MS = 120000;

export const sessions = ref<ChatSession[]>([]);
export const currentSession = ref("");
export const messages = ref<ChatMessage[]>([]);
export const composerText = ref("");
export const statusText = ref("Ready");
export const isSending = ref(false);
export const streamingTurn = ref<StreamingTurn | null>(null);

export const selectorData = ref<ChatSelectorData>({});
export const selectedPersona = ref("");
export const selectedProvider = ref("");
export const selectedModel = ref("");

export const commandSpecs = ref<ChatCommandSpec[]>([]);
export const commandMatches = ref<ChatCommandSpec[]>([]);
export const commandSelection = ref(0);
export const isCommandMenuOpen = ref(false);

export const currentModels = computed(() => {
  const grouped = selectorData.value.models_by_provider || {};
  const provider = selectedProvider.value;
  return provider && grouped[provider] ? grouped[provider] : selectorData.value.models || [];
});

/**
 * How many layers inside the panel currently own Escape -- an open popover, a
 * select inside one.
 *
 * The panel's own Escape defers to them. Dismissing a menu must not also throw
 * away the conversation the menu was opened over, and a layer that dismissed
 * both would make the panel impossible to keep open while consulting it.
 */
const openLayers = ref(0);

/** Registers a toggleable layer inside the panel, for as long as it is mounted. */
export function useEscapeLayer(open: Ref<boolean>): void {
  watch(open, (isOpen) => {
    openLayers.value += isOpen ? 1 : -1;
  });
  onUnmounted(() => {
    if (open.value) openLayers.value -= 1;
  });
}

export function escapeOwnedByLayer(): boolean {
  return openLayers.value > 0;
}

let activeController: AbortController | null = null;

// The send path holds a stream loop that outlives the click that started it; a
// module-level controller is what a Stop, or an unmount, aborts.
function abortActiveTurn(): void {
  activeController?.abort();
}

export async function refreshSessions(): Promise<void> {
  const data = await api.chatSessions<ChatSelectorData & { sessions?: ChatSession[] }>();
  const sessionList = data.sessions || [];
  sessions.value = sessionList;
  selectorData.value = data;

  selectedPersona.value = data.active_persona || "";
  const providers = data.providers || [];
  selectedProvider.value = data.active_provider || providers[0] || "";
  selectedModel.value = data.active_model || "";

  commandSpecs.value = (data.commands || []).map((item) =>
    typeof item === "string" ? { command: item, usage: item, description: "" } : item,
  );

  if (!currentSession.value && sessionList[0]) {
    // Vue's reactivity makes the assignment above readable here immediately, so
    // the read that selects the first conversation needs no data threaded into
    // it the way the Preact tree had to pass its own copy.
    await selectSession(sessionList[0].session_id);
  }
}

export async function selectSession(id: string): Promise<void> {
  if (!id) return;
  const data = selectorData.value;
  currentSession.value = id;
  messages.value = [];
  streamingTurn.value = null;

  selectedPersona.value = data.active_personas?.[id] || data.active_persona || "";

  try {
    // Resuming is the server's own command, so the transcript read below is of
    // a conversation the server considers current rather than a stale one.
    await api.chatMessage(channelID(), `/resume ${id}`);
    const [msgs, turns] = await Promise.all([api.chatMessages<ChatMessage[]>(id), api.chatTurns<ChatTurnView[]>(id)]);
    const byAssistantMessage = new Map<string, ChatTurnView>();
    for (const turn of turns || []) {
      const assistantID = turn.assistant_message_id || turn.AssistantMessageID;
      if (assistantID) byAssistantMessage.set(assistantID, turn);
    }
    // Tool calls belong to the turn, not the message, so a reopened
    // conversation gets its transcript back with the evidence in place.
    messages.value = (msgs || []).map((message) => {
      const messageID = message.message_id || message.MessageID;
      const turn = messageID ? byAssistantMessage.get(messageID) : undefined;
      return turn ? { ...message, tool_calls: turn.tool_calls || turn.ToolCalls || [] } : message;
    });
  } catch (err) {
    statusText.value = (err as Error).message || "Could not load session messages";
  }
}

/** One recorded turn, as GET /api/chat/sessions/{id}/turns returns it. */
interface ChatTurnView {
  assistant_message_id?: string;
  AssistantMessageID?: string;
  tool_calls?: ChatToolCall[];
  ToolCalls?: ChatToolCall[];
}

export async function runSessionCommand(command: string): Promise<void> {
  if (!currentSession.value) return;
  try {
    statusText.value = "Working…";
    const result = await api.chatMessage<{ session_id?: string; reply?: string }>(channelID(), command);
    if (result.session_id) currentSession.value = result.session_id;
    statusText.value = result.reply || "Session updated";
    await refreshSessions();
    if (result.session_id || currentSession.value) {
      await selectSession(result.session_id || currentSession.value);
    }
  } catch (err) {
    statusText.value = (err as Error).message || "Session action failed";
  }
}

// Rename and branch take a value the server has no form for, so the browser's
// own prompt is the input: a modal built for two commands would be more UI than
// the two commands are worth.
export function promptSessionCommand(command: string, message: string): void {
  const value = window.prompt(message);
  if (value?.trim()) void runSessionCommand(`${command} ${value.trim()}`);
}

export function setPersona(name: string): void {
  selectedPersona.value = name;
  const session = currentSession.value;
  if (!session || !name) return;
  api
    .chatPersona(session, name)
    .then(() => {
      const data = selectorData.value;
      selectorData.value = {
        ...data,
        active_personas: { ...(data.active_personas || {}), [session]: name },
      };
      statusText.value = `Personality: ${name}`;
    })
    .catch((err: Error) => {
      statusText.value = err.message || "Failed to set personality";
    });
}

export async function setProvider(provider: string): Promise<void> {
  selectedProvider.value = provider;
  if (!provider) return;
  try {
    const result = await api.chatMessage<{ reply?: string }>(channelID(), `/model --provider ${provider}`);
    statusText.value = result.reply || "Provider updated";
    await refreshSessions();
  } catch (err) {
    statusText.value = `Provider update failed: ${(err as Error).message || err}`;
  }
}

export async function setModel(model: string): Promise<void> {
  selectedModel.value = model;
  if (!model) return;
  try {
    const result = await api.chatMessage<{ reply?: string }>(channelID(), `/model ${model}`);
    statusText.value = result.reply || "Model updated";
    await refreshSessions();
  } catch (err) {
    statusText.value = `Model update failed: ${(err as Error).message || err}`;
  }
}

export function newChat(): void {
  composerText.value = "/new";
  void sendMessage({ textOverride: "/new" });
}

/** Opens the palette for the slash word the caret sits in, or closes it. */
export function syncCommandMenu(value: string, caret: number): void {
  const matches = matchesFor(commandSpecs.value, value, caret);
  if (matches.length) {
    commandMatches.value = matches;
    commandSelection.value = 0;
    isCommandMenuOpen.value = true;
    return;
  }
  isCommandMenuOpen.value = false;
  commandMatches.value = [];
}

export function closeCommandMenu(): void {
  isCommandMenuOpen.value = false;
}

export function moveCommandSelection(delta: number): void {
  const length = commandMatches.value.length;
  if (!length) return;
  commandSelection.value = (commandSelection.value + delta + length) % length;
}

/** The page the operator asked from, so an answer can be about what they saw. */
function currentPage(): string {
  return location.pathname + location.search || "/";
}

export async function sendMessage(retryOpts?: RetryOptions): Promise<void> {
  const explicitText = retryOpts?.textOverride;
  const rawValue = explicitText !== undefined ? explicitText : composerText.value;
  const { text, turn, isRetry } = resolveTurn(retryOpts, rawValue);
  if (!text || isSending.value) return;

  closeCommandMenu();
  isSending.value = true;
  statusText.value = isRetry ? "Retrying…" : "Thinking…";

  if (!isRetry) {
    messages.value = [...messages.value, { from: "web", text }];
    composerText.value = "";
  }

  let activeTools: ChatToolCall[] = [];
  streamingTurn.value = { text: "", tools: [], isError: false, turn, isRetry };

  let streamedText = "";
  let finished = false;
  let timedOut = false;
  let timeoutId: ReturnType<typeof setTimeout> | undefined;

  try {
    const controller = new AbortController();
    activeController = controller;
    timeoutId = setTimeout(() => {
      timedOut = true;
      controller.abort();
    }, STREAM_TIMEOUT_MS);

    const response = await api.chatStream(
      {
        channel_id: channelID(),
        source_id: turn.sourceID,
        text: turn.text,
        page: currentPage(),
      },
      { signal: controller.signal },
    );
    const body = response.body;
    if (!body) throw new Error("the chat stream returned no body");

    const reader = body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";

    // Drained to completion: the stream is read until the reader reports done,
    // and a turn that ends without a `done` frame is a failed turn rather than
    // a short one. Reading only some frames leaves the producer writing into a
    // stream nobody collects.
    for (;;) {
      const { value, done } = await reader.read();
      buffer += decoder.decode(value || new Uint8Array(), { stream: !done });
      const frames = buffer.split("\n\n");
      buffer = frames.pop() || "";
      for (const frame of frames) {
        const line = frame.split("\n").find((part) => part.startsWith("data: "));
        if (!line) continue;
        const event = JSON.parse(line.slice(6)) as ChatStreamFrame;
        if (event.session_id) currentSession.value = event.session_id;
        if (event.type === "delta") {
          streamedText += event.text;
          streamingTurn.value = streamingTurn.value ? { ...streamingTurn.value, text: streamedText } : null;
        }
        // A navigate result is appended to the same list as the tool calls: it
        // is one more thing the turn did, and it renders as a chip rather than
        // a tool line.
        if (event.type === "tool" || event.type === "navigate") {
          activeTools = [...activeTools, event];
          streamingTurn.value = streamingTurn.value ? { ...streamingTurn.value, tools: activeTools } : null;
        }
        if (event.type === "done") {
          finished = true;
          if (!streamedText) streamedText = event.text || "";
        }
        if (event.type === "error") throw new Error(event.text);
      }
      if (done) break;
    }

    if (!finished) throw new Error("chat stream ended before completion");

    messages.value = [...messages.value, { from: "assistant", text: streamedText, tool_calls: activeTools }];
    streamingTurn.value = null;
    statusText.value = "Ready";
    await refreshSessions();
  } catch (err) {
    const failed = err as Error;
    if (failed.name === "AbortError" && !timedOut) {
      streamingTurn.value = { text: "Turn stopped.", tools: activeTools, isError: false, turn, isRetry };
      statusText.value = "Stopped";
    } else {
      streamingTurn.value = {
        text: `Unable to complete that turn: ${failed.message || failed}`,
        tools: activeTools,
        isError: true,
        turn: turn ? retryChatTurn(turn) : undefined,
        isRetry,
      };
      statusText.value = "Error — retry available";
    }
  } finally {
    clearTimeout(timeoutId);
    activeController = null;
    isSending.value = false;
  }
}

export function stopTurn(): void {
  statusText.value = "Stopping…";
  if (currentSession.value) {
    void api.chatCancel(currentSession.value).catch(() => {});
  }
  abortActiveTurn();
}

/**
 * Owns the first sessions read and the teardown of an in-flight turn for the
 * panel's lifetime. The panel is mounted once beside the outlet and stays
 * mounted while it is closed, so opening it is a prop change rather than a
 * mount and the conversation survives a navigation.
 */
export function useChat(): void {
  onMounted(() => {
    refreshSessions().catch((err: Error) => {
      statusText.value = err.message || "Chat unavailable";
    });
  });
  onUnmounted(() => {
    abortActiveTurn();
  });
}
