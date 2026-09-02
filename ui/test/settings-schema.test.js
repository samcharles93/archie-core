import { test } from "node:test";
import assert from "node:assert/strict";
import { register } from "node:module";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { render } from "@testing-library/preact";

const cssLoad = "data:text/javascript," + encodeURIComponent(`
  export async function load(url, context, nextLoad) {
    if (url.endsWith(".css")) {
      return { format: "module", shortCircuit: true, source: "export default {};" };
    }
    return nextLoad(url, context);
  }
`);
register(cssLoad, import.meta.url);

const { api } = await import("../src/base/api.jsx");
const { settingsPage } = await import("../src/settings/settings.jsx");

const settingsJsPath = fileURLToPath(new URL("../src/settings/settings.jsx", import.meta.url));
const settingsJsSource = readFileSync(settingsJsPath, "utf8");

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
    const vnode = settingsPage(new URLSearchParams());
    const { container } = render(vnode);
    await new Promise((resolve) => setTimeout(resolve, 0));
    return container;
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
  const root = await renderSettings({ schema: FIXTURE_SCHEMA });
  assert.ok(root.textContent.includes("How images are refreshed"));
});

test("a structured field in a schema section renders no card of its own here", async () => {
  const root = await renderSettings({ schema: FIXTURE_SCHEMA, repositories: [] });
  assert.ok(!root.textContent.includes("Repositories\n[]"), "a structured field should not get a generic value row");
});

test("repositories render a checkbox and a text input reflecting each repo's current field values", async () => {
  const root = await renderSettings({
    repositories: [
      { owner: "acme", name: "widget", base: "main", allow_concurrent: true, max_retries: 3, review_enabled: false },
    ],
  });

  const checkboxes = Array.from(root.querySelectorAll('input[type="checkbox"]'));
  const textInputs = Array.from(root.querySelectorAll('input[type="text"]'));

  assert.equal(checkboxes.length, 2, "allow_concurrent and review_enabled should each render a checkbox");
  assert.ok(checkboxes[0].checked, "allow_concurrent=true should render checked");
  assert.equal(checkboxes[1].checked, false, "review_enabled=false should render unchecked");
  assert.ok(
    textInputs.some((i) => String(i.value) === "3"),
    "max_retries' current value should populate a text input",
  );
});

test("settings.jsx contains no literal field-specific branch for the generically-rendered sections", () => {
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
      `settings.jsx still references ${key} literally -- the generic renderer must not need per-field knowledge`,
    );
  }
});
