import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const { resourcesForEvent } = await import("../src/stores/live-events.ts");

const passiveRefreshSurfaces = [
  "../src/dashboard/DashboardHero.vue",
  "../src/dashboard/NeedsYouCard.vue",
  "../src/tasks/TasksPage.vue",
  "../src/tasks/TaskHeader.vue",
  "../src/tasks/TasksState.vue",
  "../src/workflows/WorkflowsPage.vue",
  "../src/skills/SkillsPage.vue",
  "../src/curators/CuratorsPage.vue",
  "../src/channels/ChannelsPage.vue",
  "../src/bindings/BindingsPage.vue",
  "../src/logs/LogActions.vue",
  "../src/settings/SystemStatusPage.vue",
  "../src/settings/SystemTasksPage.vue",
  "../src/settings/SystemModelsPage.vue",
  "../src/settings/SystemReposPage.vue",
  "../src/settings/SystemAdvancedPage.vue",
  "../src/tasks/PanelError.vue",
  "../src/tasks/TaskLogs.vue",
];

test("pages do not expose passive refresh controls", async () => {
  for (const path of passiveRefreshSurfaces) {
    const source = await readFile(new URL(path, import.meta.url), "utf8");
    assert.doesNotMatch(source, />\s*(Refresh|Retry)\s*</, path);
  }
});

test("a 401 does not offer a reload that cannot restore authentication", async () => {
  const source = await readFile(
    new URL("../src/tasks/TaskRowActions.vue", import.meta.url),
    "utf8",
  );
  assert.doesNotMatch(source, /Reload to sign in|window\.location\.reload/);
});

test("the application starts one Pinia-owned live update stream", async () => {
  const main = await readFile(
    new URL("../src/main.ts", import.meta.url),
    "utf8",
  );
  assert.match(main, /useLiveUpdatesStore\(pinia\)\.initialize\(\)/);
});

test("backend events invalidate only their owning projections", () => {
  assert.deepEqual(resourcesForEvent({ kind: "stage_finish", task_id: 42 }), [
    "tasks",
  ]);
  assert.deepEqual(resourcesForEvent({ kind: "task_queued" }), ["tasks"]);
  assert.deepEqual(resourcesForEvent({ kind: "capture" }), ["captures"]);
  assert.deepEqual(resourcesForEvent({ kind: "curator_action" }), [
    "curators",
    "skills",
  ]);
  assert.deepEqual(resourcesForEvent({ kind: "update_report" }), ["updates"]);
  assert.deepEqual(resourcesForEvent({ kind: "log" }), []);
});

test("a closed stream is retried with doubling delay, capped at 30s", async () => {
  const { reconnectDelay } = await import("../src/lib/stream-state.ts");
  assert.deepEqual(
    [0, 1, 2, 3, 4, 5, 6, 10].map(reconnectDelay),
    [1000, 2000, 4000, 8000, 16000, 30000, 30000, 30000],
  );
});
