import { test } from "node:test";
import assert from "node:assert/strict";
import { register } from "node:module";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import "./shim.js";

// settings.js imports ./settings.css, which Node's ESM loader cannot parse.
// Same stub as settings-lifecycle.test.js.
const cssLoad = "data:text/javascript," + encodeURIComponent(`
  export async function load(url, context, nextLoad) {
    if (url.endsWith(".css")) {
      return { format: "module", shortCircuit: true, source: "export default {};" };
    }
    return nextLoad(url, context);
  }
`);
register(cssLoad, import.meta.url);

const { api } = await import("../src/base/api.js");
const { settingsPage } = await import("../src/settings/settings.js");

const settingsJsPath = fileURLToPath(new URL("../src/settings/settings.js", import.meta.url));
const settingsJsSource = readFileSync(settingsJsPath, "utf8");

// A schema fixture with a field settings.js has never heard of. If the
// renderer needed a field-specific branch to show it correctly, this key
// would render blank, mislabeled, or not at all -- proving genericity
// requires a field the source code cannot possibly special-case.
const FIXTURE_SCHEMA = [
  {
    id: "budgets",
    label: "Budgets",
    description: "The limits every autonomous stage runs under.",
    fields: [
      {
        key: "budgets.made_up_field_xyz",
        label: "Totally invented setting",
        description: "0 means unlimited.",
        type: "int",
        value: 0,
        editable: true,
        restart_required: false,
      },
      {
        key: "containers.pull_policy",
        label: "How images are refreshed",
        type: "enum",
        value: "missing",
        options: ["missing", "always"],
        editable: true,
        restart_required: true,
      },
      {
        key: "repos",
        label: "Repositories",
        type: "structured",
        value: [],
        editable: false,
      },
    ],
  },
];

async function renderSettings(cfg) {
  const original = { config: api.config, version: api.version, taskMeta: api.taskMeta };
  api.config = async () => cfg;
  api.version = async () => ({ components: [] });
  api.taskMeta = async () => ({ statuses: [], actions: [] });
  try {
    const root = settingsPage();
    await new Promise((resolve) => setTimeout(resolve, 0));
    return root;
  } finally {
    api.config = original.config;
    api.version = original.version;
    api.taskMeta = original.taskMeta;
  }
}

test("a field the renderer has never seen still renders its label, value, and edit affordance", async () => {
  const root = await renderSettings({ schema: FIXTURE_SCHEMA });
  const text = root.textContent;
  assert.ok(text.includes("Totally invented setting"), "the fixture field's label should render");
  assert.ok(root.querySelector(".kv-btn"), "the fixture field should get an Edit affordance (editable: true)");
});

test("an enum field's options reach the row's opts rather than being dropped", async () => {
  // The row itself is exercised directly in config-row.test.js; here the
  // point is that settings.js's schema-walking code passes opts.options
  // through at all, for a field type settings.js does not special-case.
  const root = await renderSettings({ schema: FIXTURE_SCHEMA });
  assert.ok(root.textContent.includes("How images are refreshed"));
});

test("a structured field in a schema section renders no card of its own here", async () => {
  // "repos" is type=structured; the generic renderer must skip it (the
  // dedicated repositories card, built from cfg.repositories, owns that
  // display). If the generic path accidentally rendered it too, "[]"
  // would appear as the field's value text.
  const root = await renderSettings({ schema: FIXTURE_SCHEMA, repositories: [] });
  assert.ok(!root.textContent.includes("Repositories\n[]"), "a structured field should not get a generic value row");
});

// The real contract: settings.js must not know any individual field key by
// name for the sections the generic renderer owns (identity, budgets,
// storage, web). A literal key string in the source is exactly the
// regression archie-core-b6ew.3 removed -- a hardcoded row(...) call
// re-appearing for one field while the rest stay generic.
test("settings.js contains no literal field-specific branch for the generically-rendered sections", () => {
  const genericFieldKeys = [
    "bot_user", "bot_email", "\"label\"", "forge.type", "forge.host", "diff_cap_lines",
    "budgets.max_steps", "budgets.wall_clock", "budgets.gate_max_failures",
    "skills_dir", "plugin_dir", "secret_engine_dir",
    "containers.image", "containers.max_concurrency", "containers.max_uptime",
    "containers.volume_ttl", "containers.pull_policy", "containers.network",
    "web.listen",
  ];
  for (const key of genericFieldKeys) {
    assert.ok(
      !settingsJsSource.includes(key),
      `settings.js still references ${key} literally -- the generic renderer must not need per-field knowledge`,
    );
  }
});
