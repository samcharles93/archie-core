import { ref, watchEffect } from "vue";

export type Theme = "dark" | "light";

const STORAGE_KEY = "archie.theme";

function stored(): Theme | null {
  try {
    const value = localStorage.getItem(STORAGE_KEY);
    return value === "dark" || value === "light" ? value : null;
  } catch {
    return null;
  }
}

export const theme = ref<Theme>(stored() ?? "dark");

watchEffect(() => {
  const root = document.documentElement;
  root.classList.toggle("dark", theme.value === "dark");
  // Binds the UA's own painting (form controls, scrollbars, the canvas behind
  // an overscroll) to the theme.
  root.style.colorScheme = theme.value;
  try {
    localStorage.setItem(STORAGE_KEY, theme.value);
  } catch {
    // A blocked store costs persistence, not the toggle.
  }
});

export function toggleTheme() {
  theme.value = theme.value === "dark" ? "light" : "dark";
}
