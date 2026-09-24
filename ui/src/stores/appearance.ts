import { defineStore } from "pinia";
import { ref } from "vue";

export const THEME_KEY = "archie.theme";
export const DENSITY_KEY = "archie.density";
export const MOTION_KEY = "archie.motion";
export const TOOLTIPS_KEY = "archie.tooltips";

export type Theme = "system" | "dark" | "light";
export type Density = "comfortable" | "compact";
export type Motion = "system" | "reduced";

type ResolvedTheme = Exclude<Theme, "system">;

function storedChoice<T extends string>(
  key: string,
  choices: readonly T[],
  fallback: T,
): T {
  try {
    const value = localStorage.getItem(key);
    return choices.includes(value as T) ? (value as T) : fallback;
  } catch {
    return fallback;
  }
}

function persist(key: string, value: string): void {
  try {
    localStorage.setItem(key, value);
  } catch {
    // The preference still applies for this page view; only persistence is lost.
  }
}

function resolveTheme(preference: Theme): ResolvedTheme {
  if (preference !== "system") return preference;
  try {
    return matchMedia("(prefers-color-scheme: dark)").matches
      ? "dark"
      : "light";
  } catch {
    return "dark";
  }
}

export const useAppearanceStore = defineStore("appearance", () => {
  const theme = ref<Theme>(
    storedChoice(THEME_KEY, ["system", "dark", "light"], "dark"),
  );
  const density = ref<Density>(
    storedChoice(DENSITY_KEY, ["comfortable", "compact"], "comfortable"),
  );
  const motion = ref<Motion>(
    storedChoice(MOTION_KEY, ["system", "reduced"], "system"),
  );
  const tooltipsEnabled = ref(
    storedChoice(TOOLTIPS_KEY, ["on", "off"], "on") === "on",
  );
  let initialized = false;

  function applyTheme(): void {
    document.documentElement.dataset.theme = resolveTheme(theme.value);
  }

  function setTheme(value: Theme): void {
    theme.value = value;
    applyTheme();
    persist(THEME_KEY, value);
  }

  function setDensity(value: Density): void {
    density.value = value;
    document.documentElement.dataset.density = value;
    persist(DENSITY_KEY, value);
  }

  function setMotion(value: Motion): void {
    motion.value = value;
    document.documentElement.dataset.motion = value;
    persist(MOTION_KEY, value);
  }

  function setTooltips(enabled: boolean): void {
    tooltipsEnabled.value = enabled;
    persist(TOOLTIPS_KEY, enabled ? "on" : "off");
  }

  function initialize(): void {
    if (initialized) return;
    initialized = true;
    applyTheme();
    document.documentElement.dataset.density = density.value;
    document.documentElement.dataset.motion = motion.value;
    try {
      matchMedia("(prefers-color-scheme: dark)").addEventListener(
        "change",
        () => {
          if (theme.value === "system") applyTheme();
        },
      );
    } catch {
      // matchMedia is absent in older embedded views. The dark fallback stands.
    }
  }

  return {
    theme,
    density,
    motion,
    tooltipsEnabled,
    initialize,
    setTheme,
    setDensity,
    setMotion,
    setTooltips,
  };
});
