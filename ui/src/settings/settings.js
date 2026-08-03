import "./settings.css";
import { api } from "../base/api.js";
import { el, empty, mount, pill } from "../base/dom.js";

/**
 * A read-only view of the config archied is actually running with, grouped
 * by domain and explained in plain language. Backed by GET /api/config,
 * which is built from an explicit, hand-picked allowlist on the Go side
 * (internal/webui/api_config.go handleConfig) -- secrets never reach this
 * page, so nothing here needs to be hidden or redacted client-side.
 */
export function settingsPage() {
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
          el("h1.page-title", "Configuration"),
          el("p.page-sub", "What archied is actually running with right now."),
        ),
        el("div.page-actions", el("button.btn", { onclick: load }, "Refresh")),
      ),
      body,
    );
  }

  async function load() {
    try {
      const cfg = await api.config();
      renderBody(cfg);
    } catch (err) {
      mount(body, el("div.card", empty("Cannot reach archied", String(err.message || err))));
    }
  }

  function renderBody(cfg) {
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

    mount(
      body,
      identityCard(cfg.identity),
      repositoriesCard(cfg.repositories),
      modelsAndProvidersCard(cfg.models, cfg.providers),
      budgetsCard(cfg.budgets),
      agentCard(cfg.agent),
      storageCard(cfg.storage, cfg.containers),
      webCard(cfg.web),
    );
  }

  return root;
}

function section(title, sub, ...children) {
  return el(
    "div.card.cfg-section",
    el("div.card-head", el("div", el("h2.card-title", title), el("p.card-sub", sub))),
    ...children,
  );
}

function row(label, value) {
  return el("div.cfg-row", el("span.cfg-label", label), el("span.cfg-value.mono", value ?? "—"));
}

function identityCard(identity) {
  if (!identity) return section("Identity", "Who Archie is on the forge.", empty("Not available"));
  return section(
    "Identity",
    "Who Archie is on the forge, and how it addresses commits and comments.",
    el(
      "div.cfg-rows",
      row("Bot account", identity.bot_user),
      row("Commit author email", identity.bot_email),
      row("Pickup label", identity.label),
      row("Forge type", identity.forge_type),
      row("Forge host", identity.forge_host),
      row("Max diff size (lines)", identity.diff_cap_lines),
    ),
  );
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
    "Each repository Archie polls, and the quality gate a change must pass before it opens a pull request.",
    el(
      "table.table",
      el(
        "thead",
        el("tr", ...["Repository", "Base branch", "Ecosystem", "Quality gate", "Protected paths"].map((h) => el("th", h))),
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
          ),
        ),
      ),
    ),
  );
}

function gateSummary(gate) {
  if (!gate?.length) return "—";
  return gate.map((cmd) => cmd.join(" ")).join("  →  ");
}

function modelsAndProvidersCard(models, providers) {
  const modelEntries = Object.entries(models || {});
  const providerEntries = Object.entries(providers || {});

  const modelRows = modelEntries.length
    ? el("div.cfg-rows", ...modelEntries.map(([role, ref]) => row(roleLabel(role), ref)))
    : empty("No model roles configured", "Assign a model to at least one role (e.g. \"builder\") in [models].");

  const providerRows = providerEntries.length
    ? el(
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

function budgetsCard(budgets) {
  if (!budgets) return section("Budgets", "Limits on every agent stage.", empty("Not available"));
  return section(
    "Budgets",
    "The limits every autonomous stage runs under, so a stuck task cannot run forever.",
    el(
      "div.cfg-rows",
      row("Max steps", budgets.max_steps || "Unlimited"),
      row("Max tokens", budgets.max_tokens || "Unlimited"),
      row("Wall clock", budgets.wall_clock),
      row("Max gate failures before parking", budgets.gate_max_failures || "Unlimited"),
    ),
  );
}

function agentCard(agent) {
  if (!agent) return section("Agent execution", "How Archie runs autonomous stages.", empty("Not available"));
  return section(
    "Agent execution",
    "How archied actually runs a stage of work.",
    el(
      "div.cfg-rows",
      row("Execution mode", agentModeLabel(agent.mode)),
      row("Worker command", agent.command),
      row("Extra environment variables passed through", agent.env?.length ? agent.env.join(", ") : "None"),
    ),
  );
}

function agentModeLabel(mode) {
  switch (mode) {
    case "inprocess":
      return "In-process (same binary as the daemon)";
    case "subprocess":
      return "Subprocess (separate archie-agent process)";
    case "nats":
      return "NATS (dispatched to a worker over the message bus)";
    default:
      return mode || "Not set";
  }
}

function storageCard(storage, containers) {
  const children = [];
  if (storage) {
    children.push(
      el("h3.cfg-subhead", "Paths"),
      el(
        "div.cfg-rows",
        row("Work directory", storage.work_dir),
        row("Task database", storage.db_path),
        row("Shared skills directory", storage.skills_dir || "None (uses the work directory)"),
        row("Daemon plugin directory", storage.plugin_dir || "None"),
        row("Secret engine plugin directory", storage.secret_engine_dir || "None (built-in engines only)"),
      ),
    );
  }
  if (containers) {
    children.push(
      el("h3.cfg-subhead", "Sandboxed containers"),
      el(
        "div.cfg-rows",
        row("Sandboxing enabled", containers.enabled ? "Yes" : "No"),
        row("Agent image", containers.image || "Not set"),
        row("Max concurrent tasks", containers.max_concurrency || "Unlimited"),
        row("Container max lifetime", containers.max_uptime),
        row("Persistent volume retention", containers.volume_ttl),
        row("How images are refreshed", pullPolicyLabel(containers.pull_policy)),
        row("Docker network", containers.network || "Auto-detected"),
      ),
    );
  }
  if (!children.length) children.push(empty("Not available"));
  return section("Storage & sandboxing", "Where archied keeps its state, and how it isolates task execution.", ...children);
}

function pullPolicyLabel(policy) {
  if (policy === "always") return "Always pull the latest image before running";
  return "Only pull when the image is missing locally";
}

function webCard(web) {
  if (!web) return section("Dashboard", "This dashboard's own listen address.", empty("Not available"));
  return section(
    "Dashboard",
    "This dashboard's own listen address.",
    el("div.cfg-rows", row("Listen address", web.listen === "off" ? "Disabled" : web.listen)),
  );
}
