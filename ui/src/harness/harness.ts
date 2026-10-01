/**
 * The harness page's wire shapes and payload building, kept in a plain
 * module beside the components so it runs under the dashboard's module test
 * runner (docs/development/frontend-ui.md, "Testing the dashboard").
 *
 * The setup terminal is a WebSocket because the browser cannot set headers on
 * a terminal connection: the shared-token gate reads the same HttpOnly cookie
 * the SSE stream uses.
 */

/** One credential binding as GET /api/harness/bindings reports it. */
export interface HarnessBinding {
  service: string;
  captured: boolean;
  /** RFC 3339; absent when no token set has been captured. */
  expires_at?: string;
  updated_at?: string;
  scopes?: string[];
}

/** The page's whole read. */
export interface HarnessState {
  /** Whether this process can open a setup terminal at all. */
  terminal: boolean;
  bindings: HarnessBinding[];
  /** The agent profiles that name a Kit, i.e. what a terminal can target. */
  profiles: string[];
}

/** How a captured token reads on the page. */
export type BindingStatus = "unconfigured" | "expired" | "expiring" | "ready";

/** A captured token this close to expiry reads as "expiring". */
export const EXPIRING_WINDOW_MS = 30 * 60 * 1000;

/**
 * bindingStatus classifies a binding's capture state. A binding with no
 * captured set is "unconfigured" whatever else it carries; a captured set
 * with no parseable expiry counts as ready, because the provider did not tell
 * archie when it lapses and guessing would be wrong.
 */
export function bindingStatus(
  binding: HarnessBinding,
  now: number = Date.now(),
): BindingStatus {
  if (!binding.captured) return "unconfigured";
  if (!binding.expires_at) return "ready";
  const expires = Date.parse(binding.expires_at);
  if (Number.isNaN(expires)) return "ready";
  if (expires <= now) return "expired";
  if (expires - now <= EXPIRING_WINDOW_MS) return "expiring";
  return "ready";
}

/**
 * setupTerminalURL builds the WebSocket URL for one Kit profile. The location
 * is a parameter rather than window so the module stays runnable outside a
 * browser.
 */
export function setupTerminalURL(
  profile: string,
  location: Pick<Location, "protocol" | "host">,
): string {
  const scheme = location.protocol === "https:" ? "wss:" : "ws:";
  const query = new URLSearchParams({ profile }).toString();
  return `${scheme}//${location.host}/api/harness/terminal?${query}`;
}
