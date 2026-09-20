import { ref, type Ref } from "vue";

export const TOOLTIPS_KEY = "archie.tooltips";
export function storedTooltips(): boolean {
  try {
    return localStorage.getItem(TOOLTIPS_KEY) !== "off";
  } catch {
    // Private windows and blocked site data throw on access. On is the
    // default, so this is not worth surfacing.
    return true;
  }
}

const current = ref<boolean>(storedTooltips());

/** The live preference, shared app-wide. Change it through `setTooltips`. */
export const tooltipsEnabled: Ref<boolean> = current;

export function setTooltips(enabled: boolean): void {
  current.value = enabled;
  try {
    localStorage.setItem(TOOLTIPS_KEY, enabled ? "on" : "off");
  } catch {
    // Still applies for this page view; only persistence is lost.
  }
}
