import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

import { createPinia, setActivePinia } from "pinia";

class MemoryStorage {
  readonly values = new Map<string, string>();

  getItem(key: string): string | null {
    return this.values.get(key) ?? null;
  }

  setItem(key: string, value: string): void {
    this.values.set(key, value);
  }
}

function browser(matchesDark = false): MemoryStorage {
  const storage = new MemoryStorage();
  Object.defineProperty(globalThis, "localStorage", { configurable: true, value: storage });
  Object.defineProperty(globalThis, "document", {
    configurable: true,
    value: { documentElement: { dataset: {} as Record<string, string> } },
  });
  Object.defineProperty(globalThis, "matchMedia", {
    configurable: true,
    value: () => ({ matches: matchesDark }),
  });
  return storage;
}

Object.defineProperty(globalThis, "localStorage", { configurable: true, value: new MemoryStorage() });
const appearance = await import("../src/stores/appearance.ts");

function freshStore(matchesDark = false): { storage: MemoryStorage; store: ReturnType<typeof appearance.useAppearanceStore> } {
  const storage = browser(matchesDark);
  setActivePinia(createPinia());
  return { storage, store: appearance.useAppearanceStore() };
}

test("appearance preferences are owned by Pinia and stay close to their labels", async () => {
  const page = await readFile(new URL("../src/settings/SystemAppearancePage.vue", import.meta.url), "utf8");
  const tooltip = await readFile(new URL("../src/components/ui/tooltip/Tooltip.vue", import.meta.url), "utf8");

  assert.match(page, /useAppearanceStore/);
  assert.match(tooltip, /useAppearanceStore/);
  assert.match(page, /min-\[700px\]:grid-cols-2/);
  assert.match(page, /min-\[1100px\]:grid-cols-4/);
});

test("system theme follows the browser preference and remains the stored choice", () => {
  const { storage, store } = freshStore(true);

  store.setTheme("system");

  assert.equal(store.theme, "system");
  assert.equal(document.documentElement.dataset.theme, "dark");
  assert.equal(storage.getItem(appearance.THEME_KEY), "system");
});

test("density applies immediately and persists", () => {
  const { storage, store } = freshStore();

  store.setDensity("compact");

  assert.equal(store.density, "compact");
  assert.equal(document.documentElement.dataset.density, "compact");
  assert.equal(storage.getItem(appearance.DENSITY_KEY), "compact");
});

test("reduced motion applies immediately and persists", () => {
  const { storage, store } = freshStore();

  store.setMotion("reduced");

  assert.equal(store.motion, "reduced");
  assert.equal(document.documentElement.dataset.motion, "reduced");
  assert.equal(storage.getItem(appearance.MOTION_KEY), "reduced");
});
