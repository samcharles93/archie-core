import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

test("page headings do not narrate their own labels", async () => {
  const header = await readFile(new URL("../src/base/PageHeader.vue", import.meta.url), "utf8");
  assert.doesNotMatch(header, /subtitle/);
});

test("labeled navigation does not repeat itself in tooltips", async () => {
  for (const path of ["../src/components/topbar/Nav.vue", "../src/components/topbar/NavItem.vue"]) {
    const source = await readFile(new URL(path, import.meta.url), "utf8");
    assert.doesNotMatch(source, /Tooltip(Content|Trigger)?/u, path);
  }
});

test("Status shows runtime state instead of configuration summaries", async () => {
  const page = await readFile(new URL("../src/settings/SystemStatusPage.vue", import.meta.url), "utf8");
  assert.match(page, /HealthStatusCard/);
  assert.doesNotMatch(page, /ReadOnlyNotice|ConfigSections|ProvenanceCard|UpdateStatusCard|UpdateActionsCard/);
});

test("Task settings shows settings instead of lifecycle reference data", async () => {
  const page = await readFile(new URL("../src/settings/SystemTasksPage.vue", import.meta.url), "utf8");
  assert.doesNotMatch(page, /LifecycleCard|ReadOnlyNotice/);

  const sections = await readFile(new URL("../src/settings/ConfigSections.vue", import.meta.url), "utf8");
  assert.doesNotMatch(sections, /section\.description/);
});
