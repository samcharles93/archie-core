import "./skills.css";
import { api } from "../base/api.js";
import { el, empty, mount } from "../base/dom.js";

/**
 * What Archie can actually do, beyond what is in the source. Backed by
 * GET /api/skills, which scans .agents/skills/<name>/SKILL.md (see
 * internal/skill.Catalog) and returns each skill's name, plain-language
 * description, and where it was found.
 *
 * This page exists for someone who has never read the source: a search box
 * over a card grid, and an empty state that explains what a skill is and
 * how to add one rather than a blank box.
 */
export function skillsPage() {
  const root = el("div");
  const cardGrid = el("div.grid.grid-2");
  const searchInput = el("input.skill-search", {
    type: "search",
    placeholder: "Search skills…",
    oninput: (e) => {
      search = e.target.value.trim().toLowerCase();
      renderCards();
    },
  });

  let skills = [];
  let search = "";
  let loadError = null;

  render();
  load();

  function render() {
    mount(
      root,
      el(
        "div.page-head",
        el(
          "div",
          el("h1.page-title", "Skills"),
          el("p.page-sub", "What Archie can do, in plain language."),
        ),
        el("div.page-actions", el("button.btn", { onclick: load }, "Refresh")),
      ),
      el(
        "div.card",
        el(
          "div.card-head",
          el("div", el("h2.card-title", "Catalogue"), el("p.card-sub", "Discovered from .agents/skills/")),
          searchInput,
        ),
        cardGrid,
      ),
    );
    renderCards();
  }

  async function load() {
    try {
      const res = await api.skills();
      skills = res?.skills || [];
      loadError = null;
    } catch (err) {
      skills = [];
      loadError = String(err.message || err);
    }
    renderCards();
  }

  function renderCards() {
    if (loadError) {
      return mount(cardGrid, empty("Cannot reach archied", loadError));
    }
    if (!skills.length) {
      return mount(
        cardGrid,
        empty(
          "No skills discovered yet",
          "Skills live as SKILL.md files under .agents/skills/<name>/. Add one, or point skills_dir in config.toml at a shared skills directory, and it will appear here.",
        ),
      );
    }

    const filtered = skills.filter((s) => matches(s, search));
    if (!filtered.length) {
      return mount(cardGrid, empty("No skills match", `Nothing found for "${search}".`));
    }

    mount(cardGrid, ...filtered.map(skillCard));
  }

  function matches(skill, term) {
    if (!term) return true;
    const haystack = `${skill.name} ${skill.description}`.toLowerCase();
    return haystack.includes(term);
  }

  function skillCard(skill) {
    return el(
      "div.card.skill-card",
      el(
        "div.card-head",
        el("h3.card-title", skill.name || "Untitled skill"),
        skill.workflow && el("span.pill.pill-info", skill.workflow),
      ),
      el("p.skill-desc", skill.description || "No description provided."),
      el("div.skill-source", skill.source || "Unknown source"),
    );
  }

  return root;
}
