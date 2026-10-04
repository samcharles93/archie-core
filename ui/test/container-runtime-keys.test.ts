import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

/**
 * The keys of the container-runtime-policies document.
 *
 * internal/app/controlplane/containerRuntimePolicies owns this shape and
 * normalises every stored document to it (archie-core-qna6). These are the keys
 * the control plane serves, so they are the keys the page must bind.
 */
const documentKeys = [
  "image",
  "max_concurrency",
  "max_uptime",
  "volume_ttl",
  "pull_policy",
  "network",
];

/** The Go-cased spellings the wire carried before archie-core-qna6. */
const retiredKeys = ["Image", "MaxConcurrency", "MaxUptime", "VolumeTTL", "PullPolicy", "Network"];

test("the container runtime page binds the document's own keys", async () => {
  const page = await readFile(
    new URL("../src/settings/ContainerRuntimePage.vue", import.meta.url),
    "utf8",
  );
  // Only the template binds fields; the script's own reads go through the
  // `runtime` ref, whose `.value` is not a document key.
  const template = page.slice(page.indexOf("<template>"));
  const bound = [...template.matchAll(/runtime\.([A-Za-z_]+)/g)].map((m) => m[1]);
  assert.ok(bound.length > 0, "the page binds no fields at all");
  for (const key of new Set(bound)) {
    assert.ok(
      documentKeys.includes(key),
      `runtime.${key} is not a container-runtime-policies key`,
    );
  }
});

test("the container runtime page does not bind a retired Go-cased key", async () => {
  const page = await readFile(
    new URL("../src/settings/ContainerRuntimePage.vue", import.meta.url),
    "utf8",
  );
  for (const key of retiredKeys) {
    assert.doesNotMatch(
      page,
      new RegExp(`\\.${key}\\b`),
      `the document no longer carries ${key}`,
    );
  }
});
