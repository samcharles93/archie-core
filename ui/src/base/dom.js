// Tiny DOM helpers. Deliberately not a framework: the dashboard is a handful
// of pages over a read-mostly API, and a build-free vanilla layer keeps the
// embedded bundle small.

/**
 * el("div.card", {onclick}, child, child) -> HTMLElement
 * The tag string accepts CSS-ish shorthand: "button.btn.btn-primary".
 */
export function el(spec, attrs, ...children) {
  const [tag, ...classes] = spec.split(".");
  const node = document.createElement(tag || "div");
  if (classes.length) node.className = classes.join(" ");

  // A falsy scalar first child (0, "", false) is a value, not a missing
  // attrs object: unshift it so append() renders it instead of dropping it.
  // undefined stays "no attrs" so `el("div", undefined, child)` still works.
  if (attrs !== undefined && (typeof attrs !== "object" || attrs instanceof Node)) {
    children.unshift(attrs);
    attrs = null;
  }
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v == null || v === false) continue;
    if (k.startsWith("on") && typeof v === "function") {
      node.addEventListener(k.slice(2).toLowerCase(), v);
    } else if (k === "class") {
      node.className = [node.className, v].filter(Boolean).join(" ");
    } else if (k === "html") {
      node.innerHTML = v;
    } else {
      node.setAttribute(k, v === true ? "" : String(v));
    }
  }
  append(node, children);
  return node;
}

function append(node, children) {
  for (const c of children.flat(Infinity)) {
    if (c == null || c === false) continue;
    node.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
}

export function mount(parent, ...children) {
  parent.replaceChildren();
  append(parent, children);
  return parent;
}

/** Status pill. Kind maps to the semantic colour tokens. */
export function pill(text, kind = "idle") {
  return el(`span.pill.pill-${kind}`, text);
}

/**
 * Map an archie task status onto a pill kind. Centralised so "merged" is the
 * same green everywhere it appears.
 */
export function statusKind(status) {
  switch (status) {
    case "running":
      return "info";
    case "merged":
    case "pr_open":
      return "ok";
    case "parked":
    case "waiting_human":
      return "warn";
    case "dead":
    case "rejected":
      return "danger";
    default:
      return "idle";
  }
}

/** Humanised relative time: "2 min ago". Raw timestamps make people do maths. */
export function ago(value) {
  if (!value) return "—";
  const then = value instanceof Date ? value : new Date(value);
  const secs = Math.floor((Date.now() - then.getTime()) / 1000);
  if (!Number.isFinite(secs)) return "—";
  if (secs < 45) return "just now";
  const units = [
    ["min", 60],
    ["hr", 3600],
    ["day", 86400],
    ["wk", 604800],
  ];
  let label = "min", size = 60;
  for (const [l, s] of units) {
    if (secs >= s) [label, size] = [l, s];
  }
  const n = Math.floor(secs / size);
  return `${n} ${label}${n === 1 ? "" : "s"} ago`;
}

/** Compact number: 2_400_000 -> "2.4M". Long digit strings do not scan. */
export function compact(n) {
  if (n == null || Number.isNaN(n)) return "—";
  return new Intl.NumberFormat(undefined, { notation: "compact", maximumFractionDigits: 1 }).format(n);
}

export function empty(title, detail) {
  return el("div.empty", el("div.empty-title", title), detail && el("div", detail));
}
