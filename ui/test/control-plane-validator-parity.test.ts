import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

import {
  validateModelRoles,
  validateSchedules,
  validateSchedulingPolicy,
  type DraftValidator,
} from "../src/settings/validation.ts";

/**
 * Parity with the control plane's own validators.
 *
 * The dashboard refuses a save before the round-trip, but the server is the
 * authority, and ui/src/settings/validation.ts is a hand-written mirror of
 * three of its validators. Nothing used to tie the two together, and the
 * model-role rule had already drifted: the dashboard required
 * `^[^/\s]+\/\S+$` while the server only asks for a slash, so the dashboard
 * refused values the server accepted.
 *
 * This runs the real dashboard validators over
 * internal/app/controlplane/testdata/dashboard-validators.json -- the same
 * fixture the Go test in that package runs the real server validators over.
 * Either side changing its rule without the other fails a test here or there
 * (archie-core-ui-dashboard-3).
 */

interface ValidatorCase {
  why: string;
  valid: boolean;
  value: unknown;
}

const fixture = JSON.parse(
  readFileSync(
    new URL(
      "../../internal/app/controlplane/testdata/dashboard-validators.json",
      import.meta.url,
    ),
    "utf8",
  ),
) as { cases: Record<string, ValidatorCase[]> };

/** The dashboard's rule for each kind, keyed to match the fixture. */
const validators: Record<string, DraftValidator> = {
  "model-role-assignments": validateModelRoles,
  "scheduling-policy": validateSchedulingPolicy,
  schedules: validateSchedules,
};

test("the dashboard validators agree with the control plane's fixture", () => {
  for (const kind of Object.keys(validators)) {
    const cases = fixture.cases[kind];
    assert.ok(cases && cases.length > 0, `fixture has no cases for ${kind}`);

    let accepted = 0;
    let rejected = 0;
    for (const testCase of cases) {
      const issues = validators[kind](testCase.value);
      if (testCase.valid) {
        assert.deepEqual(
          issues,
          [],
          `${kind}: the fixture says "${testCase.why}" is valid, but the dashboard refused it`,
        );
        accepted += 1;
      } else {
        assert.notDeepEqual(
          issues,
          [],
          `${kind}: the fixture says "${testCase.why}" is invalid, but the dashboard accepted it`,
        );
        rejected += 1;
      }
    }
    assert.ok(
      accepted > 0 && rejected > 0,
      `${kind}: the fixture must pin both an accepted and a refused case`,
    );
  }

  for (const kind of Object.keys(fixture.cases)) {
    assert.ok(kind in validators, `fixture names ${kind}, which the dashboard has no validator for`);
  }
});
