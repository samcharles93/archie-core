import assert from "node:assert/strict";
import test from "node:test";

import { validateSchedules } from "../src/settings/validation.ts";

test("schedules refuse an empty id and a duplicate, keeping each row's path", () => {
  const issues = validateSchedules([
    { id: "daily" },
    { id: "" },
    { id: "daily" },
  ]);
  assert.deepEqual(issues, [
    { path: "1.id", label: "Schedule 2", message: "ID is required." },
    { path: "2.id", label: "Schedule 3", message: 'Duplicate schedule "daily".' },
  ]);
  assert.deepEqual(validateSchedules([{ id: "daily" }, { id: "weekly" }]), []);
});
