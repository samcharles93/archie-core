import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const navModel = await readFile(
  new URL("../src/lib/nav.ts", import.meta.url),
  "utf8",
);
const navGroup = await readFile(
  new URL("../src/components/topbar/NavGroup.vue", import.meta.url),
  "utf8",
);

test("System separates operational pages from settings before Task settings", () => {
  assert.match(
    navModel,
    /label:\s*"System"[\s\S]*dividerBefore:\s*\{\s*path:\s*"\/system\/tasks",\s*label:\s*"Settings"\s*\}/,
  );
  assert.match(navGroup, /DropdownMenuSeparator/);
  assert.match(navGroup, /DropdownMenuLabel/);
  assert.match(navGroup, /item\.dividerBefore/);
});
