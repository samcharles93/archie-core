import "./curators.css";
import { api } from "../base/api.js";
import { el, empty, mount, pill, ago } from "../base/dom.js";

/**
 * Curator observability (archie-core-1786637489932-6). Backed by GET
 * /api/curators, which reads the daemon's live curator.Registry: which
 * curators are registered, their point-in-time health, and their recent
 * activity (last run, action count, and the actions themselves with the
 * reason each one happened) -- see internal/webui/api_curators.go.
 */
export function curatorsPage() {
  const root = el("div");
  const cardGrid = el("div.grid.grid-2");

  render();
  load();

  function render() {
    mount(
      root,
      el(
        "div.page-head",
        el(
          "div",
          el("h1.page-title", "Curators"),
          el("p.page-sub", "Background agents that maintain memory and skills: what ran, and why."),
        ),
        el("div.page-actions", el("button.btn", { onclick: load }, "Refresh")),
      ),
      cardGrid,
    );
  }

  async function load() {
    try {
      const res = await api.curators();
      renderCards(res?.curators || []);
    } catch (err) {
      mount(cardGrid, empty("Cannot reach archied", String(err.message || err)));
    }
  }

  function renderCards(curators) {
    if (!curators.length) {
      mount(
        cardGrid,
        empty(
          "No curators registered",
          "Curators are background agent loops that maintain memory and skills. None are registered on this daemon.",
        ),
      );
      return;
    }
    mount(cardGrid, ...curators.map(curatorCard));
  }

  function healthKind(status) {
    switch (status) {
      case "healthy":
        return "ok";
      case "degraded":
        return "warn";
      case "unhealthy":
        return "danger";
      default:
        return "idle";
    }
  }

  function curatorCard(c) {
    const actions = c.recent_actions || [];
    return el(
      "div.card.curator-card",
      el(
        "div.card-head",
        el("h3.card-title", c.name),
        pill(c.health?.status || "unknown", healthKind(c.health?.status)),
      ),
      c.health?.message && el("p.curator-health-message", c.health.message),
      el(
        "p.curator-meta",
        c.last_run_at
          ? `Last ran ${ago(c.last_run_at)} · ${c.last_run_actions} action${c.last_run_actions === 1 ? "" : "s"}`
          : "Has not run yet",
      ),
      actions.length
        ? el(
            "ul.curator-actions",
            ...actions.map((a) =>
              el(
                "li.curator-action",
                el("div.curator-action-head", el("span.curator-action-type", a.type || "action"), el("span.curator-action-time", ago(a.at))),
                a.detail && el("p.curator-action-detail", a.detail),
                a.reason && el("p.curator-action-reason", `Why: ${a.reason}`),
              ),
            ),
          )
        : el("p.curator-meta", "No recorded activity yet."),
    );
  }

  return root;
}
