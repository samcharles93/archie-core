import { ref, type Ref } from "vue";

/**
 * The theme is an attribute on <html>, not a class, because index.html sets it
 * from localStorage before the first paint. Everything downstream reads the CSS
 * variables once that attribute is present, so nothing in the app branches on
 * the theme itself.
 */
export const THEME_KEY = "archie.theme";

export type Theme = "dark" | "light";

export function isTheme(value: unknown): value is Theme {
  return value === "dark" || value === "light";
}

/** Dark is the default: archied is a daemon you check at odd hours. */
export function storedTheme(): Theme {
  try {
    const stored = localStorage.getItem(THEME_KEY);
    return isTheme(stored) ? stored : "dark";
  } catch {
    // Private windows and blocked site data throw on access. The default is
    // correct there, so this is not worth surfacing.
    return "dark";
  }
}

export function applyTheme(theme: Theme): void {
  document.documentElement.dataset.theme = theme;
  try {
    localStorage.setItem(THEME_KEY, theme);
  } catch {
    // The theme still applies for this page view; only persistence is lost.
  }
}

const current = ref<Theme>(storedTheme());

/** The live theme, shared app-wide. Apply it through `setTheme` or `toggleTheme`. */
export const theme: Ref<Theme> = current;

export function setTheme(next: Theme): void {
  current.value = next;
  applyTheme(next);
}

export function toggleTheme(): void {
  setTheme(current.value === "dark" ? "light" : "dark");
}
