/** How a lifecycle status kind (from task-meta) reads as a status pill. */
export function pillFor(kind: string): {
  tone: "neutral" | "warn" | "danger";
  dot: "live" | "warn" | "danger" | "idle";
} {
  if (kind === "danger") return { tone: "danger", dot: "danger" };
  if (kind === "warn") return { tone: "warn", dot: "warn" };
  if (kind === "ok" || kind === "info") return { tone: "neutral", dot: "live" };
  return { tone: "neutral", dot: "idle" };
}
