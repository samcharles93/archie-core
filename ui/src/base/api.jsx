// Single place that knows how to talk to archied. Every feature folder goes
// through api.* below, and every api.* method goes through send(), so the CSRF
// header, the Content-Type, the request timeout, and the error shape live in
// this one file. ui/test/api-client.test.js asserts that: it fails if a second
// fetch() call appears here, and it fails if any method omits a header the
// server requires.
import { randomUUID } from "./uuid.jsx";

// DEFAULT_TIMEOUT_MS bounds a request so a daemon that accepts the connection
// but never answers cannot leave the UI in a loading state with no way back.
// The retry affordances rely on fetch settling, so bound it.
const DEFAULT_TIMEOUT_MS = 15000;

// qs builds a query string, omitting empty values so the server sees an
// absent filter rather than an empty one.
function qs(params) {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params || {})) {
    if (v !== "" && v != null) q.set(k, v);
  }
  const s = q.toString();
  return s ? `?${s}` : "";
}

// errorMessage prefers what the server actually said. archied answers these
// with http.Error, i.e. a plain-text reason -- "task is not awaiting
// approval", "max retries reached (3/3)", "Content-Type must be
// application/json" -- and discarding it in favour of "409 Conflict" throws
// away the only part an operator can act on.
async function errorMessage(res) {
  try {
    const body = (await res.text()).trim();
    if (body) return body.split("\n")[0].slice(0, 200);
  } catch {
    // Body already consumed or unreadable; fall through to the status.
  }
  return `${res.status} ${res.statusText}`;
}

export class ApiError extends Error {
  constructor(message, status) {
    super(message);
    this.status = status;
  }
}

// classifyActionError turns a failed operator action into a rendering
// decision. archied's own handlers already return distinguishable status
// codes for a refused mutation (400/403/409/415 -- bad input, missing CSRF,
// cross-origin, conflicting state, a body that is not JSON) versus a broken
// one (5xx, or no status at all when fetch itself failed -- network drop,
// timeout); 401 additionally means the session -- archied's own token cookie,
// or an upstream forward-auth proxy sitting in front of it -- has expired
// rather than that the action was rejected or the daemon is unwell.
export function classifyActionError(err) {
  const status = err instanceof ApiError ? err.status : undefined;
  const message = err?.message || "Action failed";
  if (status === 401) return { kind: "session-expired", message };
  if (status >= 400 && status < 500) return { kind: "refused", message };
  return { kind: "broken", message };
}

// send is the only fetch() in the dashboard. It converts a non-ok response
// into an ApiError carrying the server's own explanation and a usable status,
// and hands the raw Response back so the streaming caller can read the body
// itself.
async function send(path, init) {
  const res = await fetch(path, init);
  if (!res.ok) throw new ApiError(await errorMessage(res), res.status);
  return res;
}

// request performs a JSON request. Every mutating method must declare a JSON
// body: archied's handlers run authorizeTaskMutation first, and it answers 415
// unless Content-Type is application/json -- including for a mutation that has
// no body to send, because that requirement is what makes a simple cross-origin
// form post inexpressible. Sending the header on every mutation, rather than
// only where a payload happens to exist, is what keeps a bodyless DELETE or
// approve from being refused.
async function request(path, { method = "GET", body, timeoutMs = DEFAULT_TIMEOUT_MS, parse = true } = {}) {
  const headers = { Accept: "application/json" };
  const init = { method, headers, signal: AbortSignal.timeout(timeoutMs) };
  if (method !== "GET") {
    headers["Content-Type"] = "application/json";
    headers["X-Archie-CSRF"] = "1";
  }
  if (body !== undefined) init.body = JSON.stringify(body);

  const res = await send(path, init);
  return parse ? res.json() : undefined;
}

export const api = {
  summary: () => request("/api/summary"),
  tasks: () => request("/api/tasks"),
  taskMeta: () => request("/api/task-meta"),
  task: (id) => request(`/api/tasks/${id}`),
  taskAction: (id, action) => request(`/api/tasks/${id}/action`, { method: "POST", body: { action } }),
  setup: () => request("/api/setup"),
  capabilities: () => request("/api/capabilities"),
  workflows: () => request("/api/workflows"),
  workRequest: (workRequest) => request("/api/work-requests", { method: "POST", body: workRequest }),
  skills: () => request("/api/skills"),
  channels: () => request("/api/channels"),
  curators: () => request("/api/curators"),
  channelReload: (id) => request(`/api/channels/${encodeURIComponent(id)}/reload`, { method: "POST", body: {} }),
  config: () => request("/api/config"),
  version: () => request("/api/version"),
  configUpdate: (updates) => request("/api/config", { method: "PATCH", body: { updates } }),
  configRepoUpdate: (owner, name, field, value) =>
    request(`/api/config/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}`, { method: "PATCH", body: { field, value } }),
  configReset: (key) => request("/api/config/reset", { method: "POST", body: { key } }),
  logs: (params) => request("/api/logs" + qs(params)),
  taskLogs: (id, params) => request(`/api/tasks/${id}/logs` + qs(params)),
  // A download is a navigation, not a fetch: the browser must own the
  // Content-Disposition filename and stream the body to disk rather than hold
  // a whole log in memory. The URL builder is the client-side contract, and
  // base/log-row's sibling module keeps it beside the panel that uses it.
  taskLogDownloadURL: (id, attempt) => `/api/tasks/${id}/logs/download` + (attempt == null ? "" : `?attempt=${attempt}`),
  captures: (limit) => request("/api/captures" + qs({ limit })),
  mappings: () => request("/api/mappings"),
  mappingCreate: (mapping) => request("/api/mappings", { method: "POST", body: mapping }),
  mappingUpdate: (id, mapping) => request(`/api/mappings/${id}`, { method: "PATCH", body: mapping }),
  // A delete answers 204, so there is no body to parse.
  mappingDelete: (id) => request(`/api/mappings/${id}`, { method: "DELETE", parse: false }),
  mappingPreview: (captureId, fields) => request("/api/mappings/preview", { method: "POST", body: { capture_id: captureId, fields } }),
  bindings: () => request("/api/bindings"),
  bindingCreate: (binding) => request("/api/bindings", { method: "POST", body: binding }),
  bindingUpdate: (id, binding) => request(`/api/bindings/${id}`, { method: "PATCH", body: binding }),
  bindingDelete: (id) => request(`/api/bindings/${id}`, { method: "DELETE", parse: false }),
  bindingApprove: (id) => request(`/api/bindings/${id}/approve`, { method: "POST" }),
  memory: () => request("/api/memory"),
  chatSessions: () => request("/api/chat/sessions"),
  chatMessages: (id) => request(`/api/chat/sessions/${encodeURIComponent(id)}/messages`),
  chatTurns: (id) => request(`/api/chat/sessions/${encodeURIComponent(id)}/turns`),
  chatCancel: (sessionID) => request("/api/chat/cancel", { method: "POST", body: { session_id: sessionID } }),
  chatMessage: (channelID, text) =>
    request("/api/chat/message", {
      method: "POST",
      body: { channel_id: channelID, source_id: randomUUID(), text, page: location.hash.slice(1) || "/" },
    }),
  chatPersona: (sessionID, name) => request("/api/chat/persona", { method: "POST", body: { session_id: sessionID, name } }),
  chatUpdate: () => request("/api/chat/update"),
  chatUpdateDefer: (snapshot) => request("/api/chat/update/defer", { method: "POST", body: { snapshot } }),
  // Installing restarts archied, so it is allowed far longer than a normal
  // request before the UI gives up.
  chatUpdateInstall: (snapshot) => request("/api/chat/update/install", { method: "POST", body: { snapshot }, timeoutMs: 120000 }),
  chatDangerous: () => request("/api/chat/dangerous"),
  chatDangerousRequest: (kind, spec) =>
    request(`/api/chat/dangerous/${encodeURIComponent(kind)}`, { method: "POST", body: { spec } }),
  chatDangerousDecision: (id, decision) =>
    request(`/api/chat/dangerous/${encodeURIComponent(id)}/decision`, { method: "POST", body: { decision } }),
  // A server-sent event stream cannot be parsed as JSON and outlives a normal
  // request, so the caller owns the abort controller and the timeout, and
  // reads the body itself. Only the URL, headers, and failure shape are shared.
  chatStream: (payload, { signal } = {}) =>
    send("/api/chat/stream", {
      method: "POST",
      headers: { Accept: "text/event-stream", "Content-Type": "application/json", "X-Archie-CSRF": "1" },
      body: JSON.stringify(payload),
      signal,
    }),
};

/**
 * Subscribe to archied's event stream. Returns an unsubscribe function.
 * Reconnection is the browser's job via EventSource, but a closed stream is
 * surfaced to the caller so the UI can show it rather than silently freezing.
 */
export function subscribeEvents(onEvent, onStateChange) {
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

export function subscribeLogs(onEntry, onStateChange) {
  const src = new EventSource("/api/logs/stream");
  src.onopen = () => onStateChange?.("live");
  src.onerror = () => onStateChange?.("reconnecting");
  src.onmessage = (event) => {
    try { onEntry(JSON.parse(event.data)); } catch { /* malformed log frame */ }
  };
  return () => src.close();
}
