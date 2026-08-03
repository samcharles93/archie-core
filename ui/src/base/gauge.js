import { el } from "./dom.js";

/**
 * Arc gauge for a single headline percentage.
 *
 * A percentage that decides whether you need to act deserves more than a line
 * of text -- the arc is readable at a glance from across a room, which a
 * number is not. Colour follows the value, so "is this bad" is answered before
 * the digits are read.
 */
export function gauge({ value, label, size = 168 }) {
  const pct = Math.max(0, Math.min(100, Number(value) || 0));
  const stroke = pct >= 90 ? "var(--ok)" : pct >= 70 ? "var(--warn)" : "var(--danger)";

  // 240-degree arc, opening downward, so the value reads left-to-right.
  const r = 62;
  const cx = size / 2;
  const cy = size / 2;
  const sweep = 240;
  const start = 150;
  const circumference = 2 * Math.PI * r * (sweep / 360);

  const arc = (from, deg) => {
    const a0 = (from * Math.PI) / 180;
    const a1 = ((from + deg) * Math.PI) / 180;
    const large = deg > 180 ? 1 : 0;
    return [
      `M ${cx + r * Math.cos(a0)} ${cy + r * Math.sin(a0)}`,
      `A ${r} ${r} 0 ${large} 1 ${cx + r * Math.cos(a1)} ${cy + r * Math.sin(a1)}`,
    ].join(" ");
  };

  const svg = el("svg.gauge", {
    viewBox: `0 0 ${size} ${size * 0.78}`,
    role: "img",
    "aria-label": `${label}: ${pct}%`,
  });
  svg.innerHTML = `
    <path d="${arc(start, sweep)}" fill="none" stroke="var(--border)"
          stroke-width="12" stroke-linecap="round"/>
    <path d="${arc(start, sweep)}" fill="none" stroke="${stroke}"
          stroke-width="12" stroke-linecap="round"
          stroke-dasharray="${circumference}"
          stroke-dashoffset="${circumference * (1 - pct / 100)}"
          class="gauge-value"/>`;

  return el(
    "div.gauge-wrap",
    svg,
    el("div.gauge-center", el("div.gauge-pct", `${Math.round(pct)}%`), el("div.gauge-label", label)),
  );
}

/**
 * Segmented proportional bar with a legend.
 *
 * segments: [{ label, value, kind }] where kind maps to a status token.
 * Used for budget composition -- what is spent, committed and still free --
 * because three related quantities are easier to compare in one bar than in
 * three separate numbers.
 */
export function segmentBar(segments) {
  const total = segments.reduce((a, s) => a + (s.value || 0), 0) || 1;
  return el(
    "div",
    el(
      "div.segbar",
      ...segments
        .filter((s) => s.value > 0)
        .map((s) =>
          el("span", {
            class: `segbar-part segbar-${s.kind || "idle"}`,
            style: `width:${((s.value / total) * 100).toFixed(1)}%`,
            title: `${s.label}: ${s.value}`,
          }),
        ),
    ),
    el(
      "div.segbar-legend",
      ...segments.map((s) =>
        el(
          "span.segbar-key",
          el("span", { class: `segbar-dot segbar-${s.kind || "idle"}` }),
          s.label,
        ),
      ),
    ),
  );
}
