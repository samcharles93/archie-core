/**
 * The timeline's vocabulary: one human line per event kind.
 *
 * The formatters live here rather than inside a component because two surfaces
 * read them -- the timeline row and the stage rail's agent reports -- and a
 * second copy would eventually disagree with this one.
 */
import type { TaskEvent } from "./task-run";

function words(value: unknown): string {
  return String(value || "event")
    .replaceAll("_", " ")
    .replace(/\b\w/g, (letter) => letter.toUpperCase());
}

/** Exported so the stage rail renders a duration exactly the way the timeline
 * does; a second formatter would eventually disagree with this one. */
export function duration(milliseconds: unknown): string {
  const ms = Number(milliseconds);
  if (!Number.isFinite(ms) || ms < 0) return "";
  if (ms < 1000) return `${Math.round(ms)} ms`;
  const seconds = ms / 1000;
  // Under 60s: 1 dp when seconds < 10, 0 dp otherwise (40.6 s, but 41 s).
  if (seconds < 60) return `${seconds.toFixed(seconds < 10 ? 1 : 0)} s`;
  const roundedSeconds = Math.round(seconds);
  const minutes = Math.floor(roundedSeconds / 60);
  const remSec = roundedSeconds % 60;
  if (minutes < 60)
    return remSec === 0 ? `${minutes} min` : `${minutes} min ${remSec} s`;
  const hours = Math.floor(minutes / 60);
  const remMin = minutes - hours * 60;
  return remMin === 0 ? `${hours} h` : `${hours} h ${remMin} min`;
}

function count(value: unknown): string {
  const n = Number(value);
  // Render only for positive counts; zero is treated as "no metric
  // reported" so a partially-populated agent_finish (pre-v0.1.24 events,
  // or a failed run whose result is the zero Result) does not appear as
  // "0 prompt · 0 completion · 0 cached".
  if (!Number.isFinite(n) || n <= 0) return "";
  return n.toLocaleString("en-US");
}

function text(value: unknown): string {
  return typeof value === "string" ? value : "";
}

export interface TimelineLine {
  title: string;
  detail: string;
  /** Set for an event a reader must not skim past. */
  tone?: "warn";
}

export function describeTimelineEvent(ev: TaskEvent = {}): TimelineLine {
  const data = ev.data || {};
  const stage = ev.stage || "unknown stage";
  const workflow = ev.workflow ? `${ev.workflow} workflow` : "";

  if (ev.kind === "stage_start") {
    return { title: `Started stage: ${stage}`, detail: workflow };
  }
  if (ev.kind === "stage_finish") {
    const elapsed = duration(data.duration_ms);
    const context = [workflow, elapsed && `ran for ${elapsed}`]
      .filter(Boolean)
      .join(" · ");
    return {
      title: data.interrupted
        ? `Stage interrupted: ${stage}`
        : data.error
          ? `Stage failed: ${stage}`
          : `Finished stage: ${stage}`,
      detail: [context, text(data.error)].filter(Boolean).join(" — "),
    };
  }
  if (ev.kind === "task_retried") {
    const retryCount = Number(data.retry_count);
    const attempt =
      Number.isFinite(retryCount) && retryCount > 0
        ? `Retry ${retryCount}`
        : "Retry requested";
    const previous = [
      text(data.previous_stage) && `after ${text(data.previous_stage)}`,
      text(data.previous_reason),
    ]
      .filter(Boolean)
      .join(": ");
    return { title: attempt, detail: previous || ev.detail || "" };
  }
  if (ev.kind === "agent_finish") {
    const iterations = count(data.iterations);
    const total = count(data.tokens);
    const cachedRaw = Number(data.cached_tokens) || 0;
    // data.prompt_tokens INCLUDES cache hits -- showing it next to "cached"
    // reads as two separate charges when it's really one figure containing
    // the other. Show the fresh (full-price) remainder instead, so a
    // heavily-cached run doesn't look ~10x more expensive than it was billed.
    const fresh = count((Number(data.prompt_tokens) || 0) - cachedRaw);
    const completion = count(data.completion_tokens);
    const cached = count(data.cached_tokens);
    const usage = [
      iterations && `${iterations} iterations`,
      total && `${total} tokens`,
      fresh && `${fresh} fresh prompt`,
      completion && `${completion} completion`,
      cached && `${cached} cached`,
      text(data.model),
    ].filter(Boolean);
    return {
      title: `Agent ${text(data.status) || "finished"}: ${stage}`,
      detail: usage.join(" · ") || ev.detail || "",
    };
  }

  // Condition (d): the two provenance kinds render as one human line. The
  // captured payload is rendered by its own panel, never dumped into the
  // timeline, so these cases name what was recorded and nothing more.
  if (ev.kind === "config_captured") {
    return {
      title: "Configuration captured",
      detail: "The effective task-runtime configuration for this run.",
    };
  }
  if (ev.kind === "unsigned_event") {
    const source = text(data.source);
    return {
      title: "Started by an unsigned event",
      detail: source ? `source ${source}` : "",
      tone: "warn",
    };
  }
  if (ev.kind === "changes_captured") {
    const totals = (data.totals ?? {}) as { files?: unknown };
    const fileCount = Number(totals.files);
    const files =
      Number.isFinite(fileCount) && fileCount > 0
        ? `${fileCount} file${fileCount === 1 ? "" : "s"} changed`
        : "";
    const after = data.captured_after
      ? `captured after ${text(data.captured_after)}`
      : "";
    return {
      title: "Change capture recorded",
      detail: [files, after].filter(Boolean).join(" · "),
    };
  }

  return { title: words(ev.kind || ev.type), detail: ev.detail || "" };
}
