import "./settings.css";
import { api } from "../base/api.js";
import { el, empty, mount, pill } from "../base/dom.js";
import { statusKind, statusLabel } from "../base/task-meta.js";
import { row } from "./config-row.js";
import { updateStatusCard } from "./update-status.js";

/**
 * A view of the config archied is actually running with, grouped by
 * domain and explained in plain language. Backed by GET /api/config,
 * which is built from an explicit, hand-picked allowlist on the Go side
 * (internal/webui/api_config.go handleConfig) -- secrets never reach
 * this page, so nothing here needs to be hidden or redacted client-side.
 *
 * Scalar rows that are not denylisted are editable inline: Edit swaps
 * the value for an input, Save PATCHes the dotted key to /api/config,
 * which validates the materialised config before persisting to the
 * runtime overlay and republishing. Structured values (repositories,
 * models, providers -- arrays and maps) render read-only; denylisted
 * keys (db_path, work_dir) render disabled with the server's reason.
 */
export function settingsPage() {
  // .cfg-page caps and centres the whole view. Constraining the container
  // rather than the individual lists keeps the cards, key/value lists and
  // tables on one shared measure; see settings.css.
  const root = el("div.cfg-page");
  const body = el("div");
  const versionSlot = el("div");
  const lifecycleSlot = el("div");

  render();
  load();
  loadVersionStatus();
  loadLifecycle();

  function render() {
    mount(
      root,
      el(
        "div.page-head",
        el(
          "div",
          el("h1.page-title", "Configuration"),
          el("p.page-sub", "What archied is actually running with right now."),
        ),
        el("div.page-actions", el("button.btn", { onclick: () => { load(); loadVersionStatus(); loadLifecycle(); } }, "Refresh")),
      ),
      versionSlot,
      lifecycleSlot,
      body,
    );
  }

  async function load() {
    try {
      const cfg = await api.config();
      renderBody(cfg, load);
    } catch (err) {
      mount(body, el("div.card", empty("Cannot reach archied", String(err.message || err))));
    }
  }

  async function loadVersionStatus() {
    try {
      const data = await api.version();
      mount(versionSlot, updateStatusCard(data?.components || []));
    } catch (err) {
      // 501 means updates just aren't configured for this deployment --
      // that's the ordinary state for most installs, not a failure to
      // report as loudly as an unreachable daemon.
      if (err?.status === 501) {
        mount(versionSlot);
        return;
      }
      mount(versionSlot, el("div.card", empty("Update status unavailable", String(err.message || err))));
    }
  }

  async function loadLifecycle() {
    try {
      const data = await api.taskMeta();
      mount(lifecycleSlot, lifecycleCard(data));
    } catch (err) {
      // The lifecycle vocabulary is optional metadata; if archied does not
      // answer, show a quiet placeholder rather than a loud failure.
      mount(
        lifecycleSlot,
        section(
          "Work lifecycle",
          "The task statuses and operator actions archied ships.",
          empty("Lifecycle unavailable", String(err.message || err)),
        ),
      );
    }
  }

  function renderBody(cfg, reload) {
    if (!cfg || Object.keys(cfg).length === 0) {
      mount(
        body,
        el(
          "div.card",
          empty(
            "No configuration loaded",
            "archied is running without a config file wired into the dashboard, so there is nothing to show.",
          ),
        ),
      );
      return;
    }

    const ctx = { locked: cfg.locked || {}, overridden: cfg.overridden || [], onSaved: reload };
    const banners = [
      cfg.reload?.overlay_unavailable &&
        el("div.card.cfg-notice", el("p", `The runtime config overlay is not in effect: ${cfg.reload.overlay_unavailable}`)),
      cfg.reload?.last_error &&
        el("div.card.cfg-notice", el("p", `The last config reload failed; the running config is unchanged: ${cfg.reload.last_error}`)),
    ];
    mount(
      body,
      ...banners,
      ...schemaCards(cfg.schema || []),
      repositoriesCard(cfg.repositories),
      modelsAndProvidersCard(cfg.models, cfg.providers),
      provenanceCard(cfg.provenance),
    );

    // schemaCards renders one card per backend-defined section, generically,
    // from cfg.schema (archie-core-b6ew) -- a scalar field's label, type,
    // description and editability all come from the backend, not from a
    // hardcoded row(...) call here. A section whose fields are all
    // type=structured (repositories, models & providers) renders no card
    // here; those keep the dedicated renderers above, called directly off
    // the flat cfg fields (archie-core-b6ew.4 gives them their own editors).
    function schemaCards(sections) {
      return sections
        .map((s) => {
          const rows = s.fields
            .filter((f) => f.type !== "structured")
            .map((f) =>
              row(f.label, f.value, {
                key: f.key,
                type: f.type,
                options: f.options,
                raw: f.value,
                hint: f.description,
                editable: f.editable,
                ...ctx,
              }),
            );
          if (!rows.length) return null;
          return section(s.label, s.description, el("div.kv-list", ...rows));
        })
        .filter(Boolean);
    }
  }

  return root;
}

function provenanceCard(origins) {
  if (!origins?.length) return section("Configuration sources", "The files that supplied the running configuration.", empty("Source provenance is unavailable."));
  return section(
    "Configuration sources",
    "Applied from top to bottom; later entries take precedence over earlier ones.",
    el("div.kv-list", ...origins.map((origin) => row(`${origin.layer} ${origin.role}${origin.feature ? ` (${origin.feature})` : ""}`, origin.path))),
  );
}

function section(title, sub, ...children) {
  return el(
    "div.card.cfg-section",
    el("div.card-head", el("div", el("h2.card-title", title), el("p.card-sub", sub))),
    ...children,
  );
}

// Work lifecycle: the vocabulary archied ships as a server catalog. Both the
// statuses (label + pill severity) and the operator actions (label + button
// variant) are rendered from the /api/task-meta payload, so a status or action
// added on the backend appears here on the next load with no frontend change.
// statusLabel/statusKind are used as fallbacks for an entry that arrives
// without its own label/kind so the card degrades gracefully.
function lifecycleCard(data) {
  const statuses = data?.statuses || [];
  const actions = data?.actions || [];

  const statusList = statuses.length
    ? el("div.kv-list", ...statuses.map((s) => statusLifecycleRow(s)))
    : empty("No statuses reported", "The server did not return any lifecycle statuses.");

  const actionList = actions.length
    ? el("div.kv-list", ...actions.map((a) => actionLifecycleRow(a)))
    : empty("No actions reported", "The server did not return any operator actions.");

  return section(
    "Work lifecycle",
    "The task statuses and operator actions archied ships. Add one on the backend and it appears here without a frontend change.",
    el("h3.cfg-subhead", "Statuses"),
    statusList,
    el("h3.cfg-subhead", "Actions"),
    actionList,
  );
}

function statusLifecycleRow(s) {
  const label = s.label ?? statusLabel(s.id);
  const kind = s.kind ?? statusKind(s.id);
  return el("div.kv", el("span.kv-label", s.id), el("span.kv-value", pill(label, kind)));
}

function actionLifecycleRow(a) {
  const label = a.label ?? a.id;
  const cls = roleButtonClass(a.kind);
  const spec = cls ? `button.btn.btn-small.${cls}` : "button.btn.btn-small";
  return el("div.kv", el("span.kv-label", a.id), el("span.kv-value", el(spec, { type: "button", disabled: true }, label)));
}

// The button variant token for an action kind, matching the tasks board's
// mapping: quiet/danger get their own tokens, everything else (primary, link)
// falls back to the default .btn styling. A new kind never needs a new case.
function roleButtonClass(kind) {
  return kind === "quiet" ? "btn-quiet" : kind === "danger" ? "btn-danger" : "";
}

function repositoriesCard(repos) {
  if (!repos?.length) {
    return section(
      "Repositories",
      "The repositories Archie polls for work.",
      empty("No repositories configured", "Add a [[repos]] entry in config.toml so Archie has somewhere to work."),
    );
  }
  return section(
    "Repositories",
    "Each repository Archie polls, and the quality gate a change must pass before it opens a pull request. " +
      "Concurrent tasks, retries, and self-review are set per repository in config.toml -- read-only here " +
      "until archie-core-b6ew.4's follow-up gives them an inline editor.",
    el(
      "div.table-scroll",
      el(
        "table.table",
        el(
          "thead",
          el(
            "tr",
            ...["Repository", "Base branch", "Ecosystem", "Quality gate", "Protected paths", "Concurrent", "Max retries", "Self-review"].map((h) =>
              el("th", h),
            ),
          ),
        ),
        el(
          "tbody",
          ...repos.map((r) =>
            el(
              "tr",
              el("td.strong", `${r.owner}/${r.name}`),
              el("td.mono", r.base),
              el("td", r.ecosystem || "go"),
              el("td.mono", gateSummary(r.gate)),
              el("td.mono", r.protect?.length ? r.protect.join(", ") : "—"),
              el("td", boolPill(r.allow_concurrent)),
              el("td.mono", String(r.max_retries ?? 0)),
              el("td", boolPill(r.review_enabled)),
            ),
          ),
        ),
      ),
    ),
  );
}

// boolPill renders a config flag as the same pill used elsewhere on this
// page (e.g. provider credential status), so "on"/"off" reads consistently
// with the rest of the dashboard rather than as raw true/false text.
function boolPill(value) {
  return value ? pill("on", "ok") : pill("off", "idle");
}

function gateSummary(gate) {
  if (!gate?.length) return "—";
  return gate.map((cmd) => cmd.join(" ")).join("  →  ");
}

function modelsAndProvidersCard(models, providers) {
  const modelEntries = Object.entries(models || {});
  const providerEntries = Object.entries(providers || {});

  const modelRows = modelEntries.length
    ? el("div.kv-list", ...modelEntries.map(([role, ref]) => row(roleLabel(role), ref)))
    : empty("No model roles configured", "Assign a model to at least one role (e.g. \"builder\") in [models].");

  const providerRows = providerEntries.length
    ? el(
        "div.table-scroll",
        el(
          "table.table",
          el("thead", el("tr", ...["Provider", "Class", "Base URL", "API key env var", "Status"].map((h) => el("th", h)))),
          el(
            "tbody",
            ...providerEntries.map(([name, p]) =>
              el(
                "tr",
                el("td.strong", name),
                el("td", p.class),
                el("td.mono", p.base_url || "default"),
                el("td.mono", p.api_key_env || "—"),
                el("td", p.configured ? pill("configured", "ok") : pill("missing credentials", "warn")),
              ),
            ),
          ),
        ),
      )
    : empty("No providers configured", "Add a [providers.<name>] entry so a model role above has something to run on.");

  return section(
    "Models & providers",
    "Which model handles each stage of work, and which LLM providers are wired up. Only the environment variable NAME is shown, never its value.",
    el("h3.cfg-subhead", "Model roles"),
    modelRows,
    el("h3.cfg-subhead", "Providers"),
    providerRows,
  );
}

function roleLabel(role) {
  if (!role) return "Unknown role";
  return role.charAt(0).toUpperCase() + role.slice(1).replace(/_/g, " ");
}

