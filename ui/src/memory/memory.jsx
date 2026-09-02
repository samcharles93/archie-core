import "./memory.css";
import { api } from "../base/api.jsx";
import { el, empty, mount, pill } from "../base/dom.jsx";
import { icon } from "../base/icons.jsx";

/**
 * Memory: what Archie can recall, and with what.
 *
 * Memory is otherwise invisible -- it is a set of tools handed to the model at
 * discovery time, so without this page the only way to know what the agent can
 * remember is to read the provider source.
 */
export function memoryPage() {
  const root = el("div");
  const body = el("div");

  render();
  load();

  function render() {
    mount(
      root,
      el(
        "div.page-head",
        el(
          "div",
          el("h1.page-title", "Memory"),
          el("p.page-sub", "What Archie can recall between conversations, and the tools it uses to do it."),
        ),
        el("div.page-actions", el("button.btn", { onclick: load }, icon("refresh", { size: 15 }), "Refresh")),
      ),
      body,
    );
  }

  async function load() {
    mount(body, el("div.card", el("div.mem-loading", "Loading…")));
    try {
      const res = await api.memory();
      if (!res.enabled || !res.providers?.length) {
        mount(
          body,
          el(
            "div.card",
            empty(
              "Memory is not configured",
              "Without a provider, Archie starts every conversation with no recollection of earlier ones.",
            ),
          ),
        );
        return;
      }
      mount(body, el("div.grid.grid-2", ...res.providers.map(providerCard)));
    } catch (err) {
      mount(body, el("div.card", empty("Cannot read memory providers", String(err.message || err))));
    }
  }

  return root;
}

function providerCard(p) {
  const tools = p.tools || [];
  return el(
    "div.card",
    el(
      "div.card-head",
      el(
        "div",
        el("h2.card-title", p.name || "provider"),
        el("p.card-sub", roleLabel(p.role)),
      ),
      p.available
        ? pill("ready", "ok")
        : pill("unavailable", "danger"),
    ),
    !p.available &&
      el(
        "p.mem-warning",
        "Configured but not usable right now — a required binary or connection may be missing.",
      ),
    tools.length
      ? el(
          "ul.mem-tools",
          ...tools.map((t) =>
            el(
              "li.mem-tool",
              el("div.mem-tool-name.mono", t.name),
              t.description && el("div.mem-tool-desc", t.description),
            ),
          ),
        )
      : el("p.mem-warning", "This provider exposes no tools, so the agent cannot use it."),
  );
}

// Plain language: "builtin" and "external" are implementation words.
function roleLabel(role) {
  return role === "external"
    ? "Added by you — an external store Archie was given"
    : "Built in — always available to Archie";
}
