import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import { fileURLToPath } from "node:url";
import "./shim.js";
import {
  actionCatalog,
  changeStatusList,
  configSchema,
  statusList,
} from "../src/base/task-meta.js";

// The dashboard's freeze-dried snapshot (ui/src/base/task-meta.jsx) and the
// server's served catalog (internal/webui/api_task_meta.go buildTaskMeta) are
// two halves of one vocabulary. Before this test each half was pinned only by
// its own literal assertions, so a one-sided rename kept both suites green and
// the browser rendered a stale label. Reading the Go-generated fixture here
// couples them: TestTaskMetaPayloadMatchesFixture proves the handler serves
// this file byte-for-byte, and this test proves the snapshot deep-equals it.
// A rename on either side now fails on the side that was not updated.
const FIXTURE = fileURLToPath(
  new URL("../../internal/webui/testdata/task_meta.json", import.meta.url),
);
const catalogue = JSON.parse(fs.readFileSync(FIXTURE, "utf8"));

test("the snapshot deep-equals the served catalog, before any load", () => {
  assert.deepEqual(statusList(), catalogue.statuses);
  assert.deepEqual(actionCatalog(), catalogue.actions);
  assert.deepEqual(changeStatusList(), catalogue.change_statuses);
  assert.equal(configSchema(), catalogue.config_schema);
});
