// Single place that knows how to talk to archied. Every feature folder goes
// through api.* below, and every api.* method goes through send(), so the CSRF
// header, the Content-Type, the request timeout, and the error shape live in
// this one file.
import { streamStateFor, type StreamState } from "./stream-state";
import { randomUUID } from "./uuid";

// DEFAULT_TIMEOUT_MS bounds a request so a daemon that accepts the connection
// but never answers cannot leave the UI in a loading state with no way back.
// The retry affordances rely on fetch settling, so bound it.
const DEFAULT_TIMEOUT_MS = 15000;

/** Query values are omitted when empty, so the server sees an absent filter. */
export type QueryParams = Record<string, string | number | null | undefined>;

/** Opaque payloads are passed through unchanged; the server owns their shape. */
export type Payload = Record<string, unknown>;

// qs builds a query string, omitting empty values so the server sees an
// absent filter rather than an empty one.
function qs(params?: QueryParams): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params || {})) {
    if (v !== "" && v != null) q.set(k, String(v));
  }
  const s = q.toString();
  return s ? `?${s}` : "";
}

// errorMessage prefers what the server actually said. archied answers these
// with http.Error, i.e. a plain-text reason -- "task is not awaiting
// approval", "max retries reached (3/3)", "Content-Type must be
// application/json" -- and discarding it in favour of "409 Conflict" throws
// away the only part an operator can act on.
async function errorMessage(res: Response): Promise<string> {
  try {
    const body = (await res.text()).trim();
    if (body) return body.split("\n")[0].slice(0, 200);
  } catch {
    // Body already consumed or unreadable; fall through to the status.
  }
  return `${res.status} ${res.statusText}`;
}

export class ApiError extends Error {
  status: number;

  constructor(message: string, status: number) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

/** How a failed operator action should render. */
export type ActionErrorKind = "session-expired" | "refused" | "broken";

// classifyActionError turns a failed operator action into a rendering
// decision. archied's own handlers already return distinguishable status
// codes for a refused mutation (400/403/409/415 -- bad input, missing CSRF,
// cross-origin, conflicting state, a body that is not JSON) versus a broken
// one (5xx, or no status at all when fetch itself failed -- network drop,
// timeout); 401 additionally means the session -- archied's own token cookie,
// or an upstream forward-auth proxy sitting in front of it -- has expired
// rather than that the action was rejected or the daemon is unwell.
export function classifyActionError(err: unknown): { kind: ActionErrorKind; message: string } {
  const status = err instanceof ApiError ? err.status : undefined;
  const message = err instanceof Error && err.message ? err.message : "Action failed";
  if (status === 401) return { kind: "session-expired", message };
  if (status !== undefined && status >= 400 && status < 500) return { kind: "refused", message };
  return { kind: "broken", message };
}

// send is the only fetch() in the dashboard. It converts a non-ok response
// into an ApiError carrying the server's own explanation and a usable status,
// and hands the raw Response back so the streaming caller can read the body
// itself.
async function send(path: string, init: RequestInit): Promise<Response> {
  const res = await fetch(path, init);
  if (!res.ok) throw new ApiError(await errorMessage(res), res.status);
  return res;
}

interface RequestOptions {
  method?: string;
  body?: unknown;
  timeoutMs?: number;
  parse?: boolean;
}

// request performs a JSON request. Every mutating method must declare a JSON
// body: archied's handlers run authorizeTaskMutation first, and it answers 415
// unless Content-Type is application/json -- including for a mutation that has
// no body to send, because that requirement is what makes a simple cross-origin
// form post inexpressible. Sending the header on every mutation, rather than
// only where a payload happens to exist, is what keeps a bodyless DELETE or
// approve from being refused.
async function request<T = unknown>(path: string, opts: RequestOptions = {}): Promise<T> {
  const { method = "GET", body, timeoutMs = DEFAULT_TIMEOUT_MS, parse = true } = opts;
  const headers: Record<string, string> = { Accept: "application/json" };
  const init: RequestInit = { method, headers, signal: AbortSignal.timeout(timeoutMs) };
  if (method !== "GET") {
    headers["Content-Type"] = "application/json";
    headers["X-Archie-CSRF"] = "1";
  }
  if (body !== undefined) init.body = JSON.stringify(body);

  const res = await send(path, init);
  return (parse ? await res.json() : undefined) as T;
}

export const api = {
  summary: <T = unknown>() => request<T>("/api/summary"),
  tasks: <T = unknown>() => request<T>("/api/tasks"),
  taskMeta: <T = unknown>() => request<T>("/api/task-meta"),
  task: <T = unknown>(id: string) => request<T>(`/api/tasks/${encodeURIComponent(id)}`),
  taskAttempts: <T = unknown>(id: string) => request<T>(`/api/tasks/${encodeURIComponent(id)}/attempts`),
  taskChanges: <T = unknown>(id: string, params?: QueryParams) =>
    request<T>(`/api/tasks/${encodeURIComponent(id)}/changes` + qs(params)),
  taskDebug: <T = unknown>(id: string, params?: QueryParams) =>
    request<T>(`/api/tasks/${encodeURIComponent(id)}/debug` + qs(params)),
  taskAction: <T = unknown>(id: string, action: string) =>
    request<T>(`/api/tasks/${encodeURIComponent(id)}/action`, { method: "POST", body: { action } }),
  setup: <T = unknown>() => request<T>("/api/setup"),
  capabilities: <T = unknown>() => request<T>("/api/capabilities"),
  workflows: <T = unknown>() => request<T>("/api/workflows"),
  workRequest: <T = unknown>(workRequest: Payload) => request<T>("/api/work-requests", { method: "POST", body: workRequest }),
  skills: <T = unknown>() => request<T>("/api/skills"),
  channels: <T = unknown>() => request<T>("/api/channels"),
  curators: <T = unknown>() => request<T>("/api/curators"),
  channelReload: <T = unknown>(id: string) =>
    request<T>(`/api/channels/${encodeURIComponent(id)}/reload`, { method: "POST", body: {} }),
  config: <T = unknown>() => request<T>("/api/config"),
  version: <T = unknown>() => request<T>("/api/version"),
  configUpdate: <T = unknown>(updates: Payload) => request<T>("/api/config", { method: "PATCH", body: { updates } }),
  configRepoUpdate: <T = unknown>(owner: string, name: string, field: string, value: unknown) =>
    request<T>(`/api/config/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}`, {
      method: "PATCH",
      body: { field, value },
    }),
  configReset: <T = unknown>(key: string) => request<T>("/api/config/reset", { method: "POST", body: { key } }),
  logs: <T = unknown>(params?: QueryParams) => request<T>("/api/logs" + qs(params)),
  taskLogs: <T = unknown>(id: string, params?: QueryParams) =>
    request<T>(`/api/tasks/${encodeURIComponent(id)}/logs` + qs(params)),
  // A download is a navigation, not a fetch: the browser must own the
  // Content-Disposition filename and stream the body to disk rather than hold
  // a whole log in memory. The URL builder is the client-side contract, and
  // log-row keeps it beside the panel that uses it.
  taskLogDownloadURL: (id: string, attempt?: number | null) =>
    `/api/tasks/${encodeURIComponent(id)}/logs/download` + (attempt == null ? "" : `?attempt=${attempt}`),
  captures: <T = unknown>(limit?: number) => request<T>("/api/captures" + qs({ limit })),
  mappings: <T = unknown>() => request<T>("/api/mappings"),
  mappingCreate: <T = unknown>(mapping: Payload) => request<T>("/api/mappings", { method: "POST", body: mapping }),
  mappingUpdate: <T = unknown>(id: string, mapping: Payload) =>
    request<T>(`/api/mappings/${encodeURIComponent(id)}`, { method: "PATCH", body: mapping }),
  // A delete answers 204, so there is no body to parse.
  mappingDelete: (id: string) => request<void>(`/api/mappings/${encodeURIComponent(id)}`, { method: "DELETE", parse: false }),
  mappingPreview: <T = unknown>(captureId: string, fields: unknown) =>
    request<T>("/api/mappings/preview", { method: "POST", body: { capture_id: captureId, fields } }),
  bindings: <T = unknown>() => request<T>("/api/bindings"),
  bindingCreate: <T = unknown>(binding: Payload) => request<T>("/api/bindings", { method: "POST", body: binding }),
  bindingUpdate: <T = unknown>(id: string, binding: Payload) =>
    request<T>(`/api/bindings/${encodeURIComponent(id)}`, { method: "PATCH", body: binding }),
  bindingDelete: (id: string) => request<void>(`/api/bindings/${encodeURIComponent(id)}`, { method: "DELETE", parse: false }),
  bindingApprove: <T = unknown>(id: string) => request<T>(`/api/bindings/${encodeURIComponent(id)}/approve`, { method: "POST" }),
  chatSessions: <T = unknown>() => request<T>("/api/chat/sessions"),
  chatMessages: <T = unknown>(id: string) => request<T>(`/api/chat/sessions/${encodeURIComponent(id)}/messages`),
  chatTurns: <T = unknown>(id: string) => request<T>(`/api/chat/sessions/${encodeURIComponent(id)}/turns`),
  chatCancel: <T = unknown>(sessionID: string) => request<T>("/api/chat/cancel", { method: "POST", body: { session_id: sessionID } }),
  // The page the operator was on when they asked, so a chat answer can be
  // about what they were looking at. History routing makes that the pathname.
  chatMessage: <T = unknown>(channelID: string, text: string) =>
    request<T>("/api/chat/message", {
      method: "POST",
      body: {
        channel_id: channelID,
        source_id: randomUUID(),
        text,
        page: location.pathname + location.search || "/",
      },
    }),
  chatPersona: <T = unknown>(sessionID: string, name: string) =>
    request<T>("/api/chat/persona", { method: "POST", body: { session_id: sessionID, name } }),
  chatUpdate: <T = unknown>() => request<T>("/api/chat/update"),
  chatUpdateDefer: <T = unknown>(snapshot: unknown) => request<T>("/api/chat/update/defer", { method: "POST", body: { snapshot } }),
  // Installing restarts archied, so it is allowed far longer than a normal
  // request before the UI gives up.
  chatUpdateInstall: <T = unknown>(snapshot: unknown) =>
    request<T>("/api/chat/update/install", { method: "POST", body: { snapshot }, timeoutMs: 120000 }),
  chatDangerous: <T = unknown>() => request<T>("/api/chat/dangerous"),
  chatDangerousRequest: <T = unknown>(kind: string, spec: unknown) =>
    request<T>(`/api/chat/dangerous/${encodeURIComponent(kind)}`, { method: "POST", body: { spec } }),
  chatDangerousDecision: <T = unknown>(id: string, decision: string) =>
    request<T>(`/api/chat/dangerous/${encodeURIComponent(id)}/decision`, { method: "POST", body: { decision } }),
  // A server-sent event stream cannot be parsed as JSON and outlives a normal
  // request, so the caller owns the abort controller and the timeout, and
  // reads the body itself. Only the URL, headers, and failure shape are shared.
  chatStream: (payload: unknown, opts: { signal?: AbortSignal } = {}) =>
    send("/api/chat/stream", {
      method: "POST",
      headers: { Accept: "text/event-stream", "Content-Type": "application/json", "X-Archie-CSRF": "1" },
      body: JSON.stringify(payload),
      signal: opts.signal,
    }),
};

/**
 * Subscribe to archied's event stream. Returns an unsubscribe function.
 * Reconnection is the browser's job via EventSource, but a closed stream is
 * surfaced to the caller so the UI can show it rather than silently freezing.
 */
export function subscribeEvents(
  onEvent: (event: unknown) => void,
  onStateChange?: (state: StreamState) => void,
): () => void {
  const src = new EventSource("/events");
  src.onopen = () => onStateChange?.("live");
  src.onerror = () => onStateChange?.("reconnecting");
  src.onmessage = (e) => {
    try {
      onEvent(JSON.parse(e.data));
    } catch {
      /* a malformed frame should not kill the stream */
    }
  };
  return () => src.close();
}

export function subscribeLogs(
  onEntry: (entry: unknown) => void,
  onStateChange?: (state: StreamState) => void,
): () => void {
  const src = new EventSource("/api/logs/stream");
  src.onopen = () => onStateChange?.("live");
  // onerror fires both for a drop the browser will retry and for one it has
  // given up on; readyState is what tells them apart.
  src.onerror = () => onStateChange?.(streamStateFor(src.readyState));
  src.onmessage = (event) => {
    try {
      onEntry(JSON.parse(event.data));
    } catch {
      /* malformed log frame */
    }
  };
  return () => src.close();
}
