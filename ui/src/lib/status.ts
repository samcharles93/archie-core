/**
 * Status vocabulary. The one place status colour is decided: a task state, a
 * gate result, a log level and a budget segment all draw from the same five
 * kinds, so the same colour always means the same thing wherever it appears.
 */
export type StatusKind = "ok" | "warn" | "danger" | "info" | "idle";

/**
 * The filled colour, for marks that carry the kind as a shape rather than as
 * text: a bar segment, a legend dot.
 */
export const STATUS_FILL: Record<StatusKind, string> = {
  ok: "bg-ok",
  warn: "bg-warn",
  danger: "bg-danger",
  info: "bg-info",
  idle: "bg-idle",
};
