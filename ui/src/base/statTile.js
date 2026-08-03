import { el } from "./dom.js";
import { icon } from "./icons.js";

/**
 * A KPI tile: label, big value, trend against a stated comparison window, and
 * a sparkline. The comparison line is not decoration -- a number without a
 * baseline cannot be acted on, so `compare` is required.
 *
 * trend > 0 renders as an increase. `goodDirection` says whether that is good,
 * because rising latency and rising success rate are not the same news.
 */
export function statTile({ label, value, unit, trend, compare, series = [], goodDirection = "up" }) {
  const dir = trend == null ? null : trend >= 0 ? "up" : "down";
  const good = dir == null ? null : dir === goodDirection;

  return el(
    "div.card.stat",
    el(
      "div.stat-head",
      el("span.stat-label", label),
      dir &&
        el(
          `span.stat-trend.${good ? "up" : "down"}`,
          icon(dir === "up" ? "trendUp" : "trendDown", { size: 13 }),
          `${Math.abs(trend).toFixed(1)}%`,
        ),
    ),
    el("div.stat-value", value, unit && el("span.stat-unit", unit)),
    compare && el("div.stat-compare", compare),
    series.length > 1 && sparkline(series, good === false ? "var(--danger)" : "var(--ok)"),
  );
}

/**
 * Inline SVG sparkline. Shows shape, not exact values -- the tile's headline
 * number carries the precision.
 */
export function sparkline(series, stroke = "var(--ok)", { width = 220, height = 34 } = {}) {
  const max = Math.max(...series, 1);
  const min = Math.min(...series, 0);
  const span = max - min || 1;
  const step = series.length > 1 ? width / (series.length - 1) : width;

  const points = series
    .map((v, i) => `${(i * step).toFixed(1)},${(height - ((v - min) / span) * height).toFixed(1)}`)
    .join(" ");

  const svg = el("svg.spark", {
    viewBox: `0 0 ${width} ${height}`,
    preserveAspectRatio: "none",
    "aria-hidden": "true",
  });
  svg.innerHTML =
    `<polyline points="${points}" fill="none" stroke="${stroke}" stroke-width="1.5" ` +
    `stroke-linecap="round" stroke-linejoin="round" vector-effect="non-scaling-stroke"/>`;
  return svg;
}
