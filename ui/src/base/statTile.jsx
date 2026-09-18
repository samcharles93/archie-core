import { h } from "preact";
import { Icon } from "./icons.jsx";

/**
 * A KPI tile: label, big value, trend against a stated comparison window, and
 * a sparkline. The comparison line is not decoration -- a number without a
 * baseline cannot be acted on, so `compare` is required.
 *
 * trend > 0 renders as an increase. `goodDirection` says whether that is good,
 * because rising latency and rising success rate are not the same news.
 *
 * The headline value is rendered whatever its type, including 0 and "": a tile
 * that showed a label and a comparison line with no number above them read as
 * a broken dashboard on every fresh install.
 */
export function StatTile({ label, value, unit, trend, compare, series = [], goodDirection = "up" }) {
  const dir = trend == null ? null : trend >= 0 ? "up" : "down";
  const good = dir == null ? null : dir === goodDirection;

  return (
    <div className="card stat">
      <div className="stat-head">
        <span className="stat-label">{label}</span>
        {dir ? (
          <span className={`stat-trend ${good ? "up" : "down"}`}>
            <Icon name={dir === "up" ? "trendUp" : "trendDown"} size={13} />
            {`${Math.abs(trend).toFixed(1)}%`}
          </span>
        ) : null}
      </div>
      <div className="stat-value">
        {value}
        {unit ? <span className="stat-unit">{unit}</span> : null}
      </div>
      {compare ? <div className="stat-compare">{compare}</div> : null}
      {series.length > 1 ? <Sparkline series={series} stroke={good === false ? "var(--danger)" : "var(--ok)"} /> : null}
    </div>
  );
}

/**
 * Inline SVG sparkline. Shows shape, not exact values -- the tile's headline
 * number carries the precision.
 */
export function Sparkline({ series, stroke = "var(--ok)", width = 220, height = 34 }) {
  const max = Math.max(...series, 1);
  const min = Math.min(...series, 0);
  const span = max - min || 1;
  const step = series.length > 1 ? width / (series.length - 1) : width;

  const points = series
    .map((v, i) => `${(i * step).toFixed(1)},${(height - ((v - min) / span) * height).toFixed(1)}`)
    .join(" ");

  return (
    <svg className="spark" viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none" aria-hidden="true">
      <polyline
        points={points}
        fill="none"
        stroke={stroke}
        stroke-width="1.5"
        stroke-linecap="round"
        stroke-linejoin="round"
        vector-effect="non-scaling-stroke"
      />
    </svg>
  );
}
