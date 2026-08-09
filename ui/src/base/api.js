// Single place that knows how to talk to archied. Every feature folder goes
// through this, so auth handling and error shape live in one file.

async function req(path) {
  // A daemon that accepts the connection but never answers would otherwise
  // leave the UI stuck in a loading state with no way back; the retry
  // affordances rely on fetch settling, so bound it.
  const res = await fetch(path, {
    headers: { Accept: "application/json" },
    signal: AbortSignal.timeout(15000),
  });
  if (res.status === 401) {
    throw new ApiError("unauthorised", 401);
  }
  if (!res.ok) {
    throw new ApiError(`${res.status} ${res.statusText}`, res.status);
  }
  return res.json();
}

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
// approval", "max retries reached (3/3)" -- and discarding it in favour of
// "409 Conflict" throws away the only part an operator can act on.
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

export const api = {
  summary: () => req("/api/summary"),
  tasks: () => req("/api/tasks"),
  task: (id) => req(`/api/tasks/${id}`),
  taskAction: async (id, action) => {
    const res = await fetch(`/api/tasks/${id}/action`, {
      method: "POST",
      headers: { Accept: "application/json", "Content-Type": "application/json", "X-Archie-CSRF": "1" },
      body: JSON.stringify({ action }),
      signal: AbortSignal.timeout(15000),
    });
    if (!res.ok) throw new ApiError(await errorMessage(res), res.status);
    return res.json();
  },
  setup: () => req("/api/setup"),
  workflows: () => req("/api/workflows"),
	workRequest: async (request) => {
		const res = await fetch("/api/work-requests", {
			method: "POST",
			headers: { Accept: "application/json", "Content-Type": "application/json", "X-Archie-CSRF": "1" },
			body: JSON.stringify(request), signal: AbortSignal.timeout(15000),
		});
		if (!res.ok) throw new ApiError(await errorMessage(res), res.status);
		return res.json();
	},
  skills: () => req("/api/skills"),
  channels: () => req("/api/channels"),
	channelReload: async (id) => {
		const res = await fetch(`/api/channels/${encodeURIComponent(id)}/reload`, {
			method: "POST", headers: { Accept: "application/json", "Content-Type": "application/json", "X-Archie-CSRF": "1" },
			body: "{}", signal: AbortSignal.timeout(15000),
		});
		if (!res.ok) throw new ApiError(await errorMessage(res), res.status);
		return res.json();
	},
  config: () => req("/api/config"),
  configUpdate: async (updates) => {
    const res = await fetch("/api/config", {
      method: "PATCH",
      headers: { Accept: "application/json", "Content-Type": "application/json", "X-Archie-CSRF": "1" },
      body: JSON.stringify({ updates }),
      signal: AbortSignal.timeout(15000),
    });
    if (!res.ok) throw new ApiError(await errorMessage(res), res.status);
    return res.json();
  },
  logs: (params) => req("/api/logs" + qs(params)),
  memory: () => req("/api/memory"),
  chatSessions: () => req("/api/chat/sessions"),
  chatMessages: (id) => req(`/api/chat/sessions/${encodeURIComponent(id)}/messages`),
  chatCancel: async (sessionID) => {
    const res = await fetch("/api/chat/cancel", {
      method: "POST", headers: { Accept: "application/json", "Content-Type": "application/json" },
      body: JSON.stringify({ session_id: sessionID }), signal: AbortSignal.timeout(15000),
    });
    if (!res.ok) throw new ApiError(await errorMessage(res), res.status);
    return res.json();
  },
  chatMessage: async (channelID, text) => {
    const res = await fetch("/api/chat/message", {
      method: "POST", headers: { Accept: "application/json", "Content-Type": "application/json" },
      body: JSON.stringify({ channel_id: channelID, source_id: crypto.randomUUID(), text }),
      signal: AbortSignal.timeout(15000),
    });
    if (!res.ok) throw new ApiError(await errorMessage(res), res.status);
    return res.json();
  },
  chatPersona: async (sessionID, name) => {
    const res = await fetch("/api/chat/persona", {
      method: "POST", headers: { Accept: "application/json", "Content-Type": "application/json" },
      body: JSON.stringify({ session_id: sessionID, name }), signal: AbortSignal.timeout(15000),
    });
    if (!res.ok) throw new ApiError(await errorMessage(res), res.status);
    return res.json();
  },
  chatUpdate: () => req("/api/chat/update"),
  chatUpdateDefer: async (snapshot) => {
    const res = await fetch("/api/chat/update/defer", {
      method: "POST", headers: { Accept: "application/json", "Content-Type": "application/json" },
      body: JSON.stringify({ snapshot }), signal: AbortSignal.timeout(15000),
    });
    if (!res.ok) throw new ApiError(await errorMessage(res), res.status);
    return res.json();
  },
  chatUpdateInstall: async (snapshot) => {
    const res = await fetch("/api/chat/update/install", {
      method: "POST", headers: { Accept: "application/json", "Content-Type": "application/json" },
      body: JSON.stringify({ snapshot }), signal: AbortSignal.timeout(120000),
    });
    if (!res.ok) throw new ApiError(await errorMessage(res), res.status);
    return res.json();
  },
  chatDangerous: () => req("/api/chat/dangerous"),
  chatDangerousRequest: async (kind, spec) => {
    const res = await fetch(`/api/chat/dangerous/${encodeURIComponent(kind)}`, {
      method: "POST", headers: { Accept: "application/json", "Content-Type": "application/json" },
      body: JSON.stringify({ spec }), signal: AbortSignal.timeout(15000),
    });
    if (!res.ok) throw new ApiError(await errorMessage(res), res.status);
    return res.json();
  },
  chatDangerousDecision: async (id, decision) => {
    const res = await fetch(`/api/chat/dangerous/${encodeURIComponent(id)}/decision`, {
      method: "POST", headers: { Accept: "application/json", "Content-Type": "application/json" },
      body: JSON.stringify({ decision }), signal: AbortSignal.timeout(15000),
    });
    if (!res.ok) throw new ApiError(await errorMessage(res), res.status);
    return res.json();
  },
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
