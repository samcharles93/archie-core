/**
 * Icon set.
 *
 * A single stroked 24-grid system rather than Unicode glyphs: glyphs render at
 * different weights and baselines per platform, cannot inherit stroke width,
 * and have no consistent optical size. Every icon here shares the same grid,
 * stroke and cap style, so a row of them lines up.
 */

const paths = {
  dashboard: '<rect x="3" y="3" width="7" height="9" rx="1.5"/><rect x="14" y="3" width="7" height="5" rx="1.5"/><rect x="14" y="12" width="7" height="9" rx="1.5"/><rect x="3" y="16" width="7" height="5" rx="1.5"/>',
  chat: '<path d="M4 5.5A2.5 2.5 0 0 1 6.5 3h11A2.5 2.5 0 0 1 20 5.5v7a2.5 2.5 0 0 1-2.5 2.5H11l-4.5 4v-4.1A2.5 2.5 0 0 1 4 12.5z"/><path d="M8 8h8M8 11h5"/>',
  tasks: '<path d="M9 6h11M9 12h11M9 18h11"/><path d="M4 6l1 1 2-2M4 12l1 1 2-2M4 18l1 1 2-2"/>',
  logs: '<path d="M4 5h16M4 10h16M4 15h10M4 20h7"/>',
  skills: '<path d="M12 3l2.6 5.6 6.1.8-4.5 4.2 1.2 6-5.4-3-5.4 3 1.2-6L3.3 9.4l6.1-.8z"/>',
  workflows: '<path d="M4 7h6a3 3 0 0 1 3 3v4a3 3 0 0 0 3 3h4"/><path d="M17 4l3 3-3 3"/><path d="M7 21l-3-3 3-3"/>',
  memory: '<circle cx="12" cy="12" r="3.2"/><path d="M12 3v3.4M12 17.6V21M3 12h3.4M17.6 12H21M5.6 5.6l2.4 2.4M16 16l2.4 2.4M18.4 5.6L16 8M8 16l-2.4 2.4"/>',
  channels: '<rect x="3" y="5" width="18" height="14" rx="2"/><path d="M3.5 7l8.5 6 8.5-6"/>',
  curators: '<circle cx="12" cy="12" r="8"/><path d="M12 8v4l3 2"/>',
  captures: '<path d="M12 3v11M12 14l-4-4M12 14l4-4"/><path d="M4 14v4a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-4"/>',
  mappings: '<circle cx="5" cy="7" r="2"/><circle cx="5" cy="17" r="2"/><circle cx="19" cy="12" r="2"/><path d="M7 7l10 5M7 17l10-5"/>',
  bindings: '<rect x="3" y="9" width="8" height="6" rx="2.5"/><rect x="13" y="9" width="8" height="6" rx="2.5"/><path d="M9 12h6"/>',
  settings: '<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.9l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-2.9 1.2V21a2 2 0 1 1-4 0v-.1A1.7 1.7 0 0 0 7 19.4a1.7 1.7 0 0 0-1.9.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1A1.7 1.7 0 0 0 3 15H3a2 2 0 1 1 0-4h.1A1.7 1.7 0 0 0 4.6 7a1.7 1.7 0 0 0-.3-1.9l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1A1.7 1.7 0 0 0 9 3.6V3a2 2 0 1 1 4 0v.1A1.7 1.7 0 0 0 15 4.6h.1a1.7 1.7 0 0 0 1.9-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.9V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/>',
  search: '<circle cx="11" cy="11" r="7"/><path d="M20 20l-3.6-3.6"/>',
  moon: '<path d="M20 14.5A8.5 8.5 0 0 1 9.5 4a8.5 8.5 0 1 0 10.5 10.5z"/>',
  sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M2 12h2M20 12h2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M19.1 4.9l-1.4 1.4M6.3 17.7l-1.4 1.4"/>',
  refresh: '<path d="M20 11a8 8 0 1 0-1.2 5.2"/><path d="M20 4v7h-7"/>',
  bell: '<path d="M18 9a6 6 0 1 0-12 0c0 6-2 7-2 7h16s-2-1-2-7"/><path d="M13.7 20a2 2 0 0 1-3.4 0"/>',
  trendUp: '<path d="M4 17L10 11l4 4 6-6"/><path d="M14 7h6v6"/>',
  trendDown: '<path d="M4 7l6 6 4-4 6 6"/><path d="M14 17h6v-6"/>',
  check: '<path d="M4.5 12.5l5 5 10-11"/>',
  close: '<path d="M6 6l12 12M18 6L6 18"/>',
  help: '<circle cx="12" cy="12" r="9"/><path d="M9.6 9.4a2.5 2.5 0 1 1 3.3 2.4c-.6.2-.9.8-.9 1.4v.4"/><path d="M12 17.2h.01"/>',
};

/**
 * icon("tasks") -> SVGElement. size is the rendered box; the grid is always 24
 * so stroke weight stays optically consistent at any size.
 */
export function icon(name, { size = 18, className = "" } = {}) {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("width", String(size));
  svg.setAttribute("height", String(size));
  svg.setAttribute("fill", "none");
  svg.setAttribute("stroke", "currentColor");
  svg.setAttribute("stroke-width", "1.6");
  svg.setAttribute("stroke-linecap", "round");
  svg.setAttribute("stroke-linejoin", "round");
  svg.setAttribute("aria-hidden", "true");
  svg.setAttribute("class", `ico ${className}`.trim());
  svg.innerHTML = paths[name] || paths.dashboard;
  return svg;
}
