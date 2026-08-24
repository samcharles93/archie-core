function words(value) {
  return String(value || "event")
    .replaceAll("_", " ")
    .replace(/\b\w/g, (letter) => letter.toUpperCase());
}

function duration(milliseconds) {
  const ms = Number(milliseconds);
  if (!Number.isFinite(ms) || ms < 0) return "";
  if (ms < 1000) return `${Math.round(ms)} ms`;
  const seconds = ms / 1000;
  // Under 60s: 1 dp when seconds < 10, 0 dp otherwise (40.6 s, but 41 s).
  if (seconds < 60) return `${seconds.toFixed(seconds < 10 ? 1 : 0)} s`;
  const roundedSeconds = Math.round(seconds);
  const minutes = Math.floor(roundedSeconds / 60);
  const remSec = roundedSeconds % 60;
  if (minutes < 60) return remSec === 0 ? `${minutes} min` : `${minutes} min ${remSec} s`;
  const hours = Math.floor(minutes / 60);
  const remMin = minutes - hours * 60;
  return remMin === 0 ? `${hours} h` : `${hours} h ${remMin} min`;
}

function count(value) {
  const n = Number(value);
  // Render only for positive counts; zero is treated as "no metric
  // reported" so a partially-populated agent_finish (pre-v0.1.24 events,
  // or a failed run whose result is the zero Result) does not appear as
  // "0 prompt · 0 completion · 0 cached".
  if (!Number.isFinite(n) || n <= 0) return "";
  return n.toLocaleString("en-US");
}

export function describeTimelineEvent(ev = {}) {
  const data = ev.data || {};
  const stage = ev.stage || "unknown stage";
  const workflow = ev.workflow ? `${ev.workflow} workflow` : "";

  if (ev.kind === "stage_start") {
    return { title: `Started stage: ${stage}`, detail: workflow };
  }
  if (ev.kind === "stage_finish") {
    const elapsed = duration(data.duration_ms);
    const context = [workflow, elapsed && `ran for ${elapsed}`].filter(Boolean).join(" · ");
    return {
      title: data.interrupted ? `Stage interrupted: ${stage}` : data.error ? `Stage failed: ${stage}` : `Finished stage: ${stage}`,
      detail: [context, data.error].filter(Boolean).join(" — "),
    };
  }
  if (ev.kind === "task_retried") {
    const count = Number(data.retry_count);
    const attempt = Number.isFinite(count) && count > 0 ? `Retry ${count}` : "Retry requested";
    const previous = [data.previous_stage && `after ${data.previous_stage}`, data.previous_reason]
      .filter(Boolean)
      .join(": ");
    return { title: attempt, detail: previous || ev.detail || "" };
  }
  if (ev.kind === "agent_finish") {
    const iterations = count(data.iterations);
    const total = count(data.tokens);
    const prompt = count(data.prompt_tokens);
    const completion = count(data.completion_tokens);
    const cached = count(data.cached_tokens);
    const usage = [
      iterations && `${iterations} iterations`,
      total && `${total} tokens`,
      prompt && `${prompt} prompt`,
      completion && `${completion} completion`,
      cached && `${cached} cached`,
      data.model,
    ].filter(Boolean);
    return {
      title: `Agent ${data.status || "finished"}: ${stage}`,
      detail: usage.join(" · ") || ev.detail || "",
    };
  }

  return { title: words(ev.kind || ev.type), detail: ev.detail || "" };
}
