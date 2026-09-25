import type { StreamState } from "./stream-state";

export type ConnectionState = StreamState | "connecting";

/** What the UI calls the live stream's state, and the tone it is shown in. */
export function connectionStatus(state: ConnectionState): {
  label: string;
  tone: "ok" | "warn" | "danger";
} {
  if (state === "live") return { label: "Connected", tone: "ok" };
  if (state === "unavailable") return { label: "Disconnected", tone: "danger" };
  return { label: "Connecting", tone: "warn" };
}
