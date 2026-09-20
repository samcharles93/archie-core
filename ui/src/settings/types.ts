/**
 * What GET /api/config and the operator endpoints answer, as far as the six
 * System pages read them.
 *
 * Written by hand rather than generated: internal/webui/api_config.go's
 * ConfigView is an explicit allowlist whose whole purpose is to keep secrets
 * (forge tokens, provider API keys) out of this payload, so the type describes
 * what is served, not what config.Config holds.
 */

/** How the generic renderer decides what control a field gets. A structured
 * field (repositories, models, providers) is owned by its own editor and
 * never appears in a generic row. */
export type ConfigFieldType = "string" | "int" | "bool" | "duration" | "enum" | "structured";

export interface ConfigField {
  /** The dotted path PATCH /api/config accepts this value under. */
  key: string;
  label: string;
  description?: string;
  type: ConfigFieldType;
  value?: unknown;
  /** The dashboard may change it. Distinct from locked_reason, which is a
   * runtime property of the running config rather than of the field. */
  editable: boolean;
  locked_reason?: string;
  overridden?: boolean;
  options?: string[];
  /** Changing it will not take effect until archied restarts. */
  restart_required?: boolean;
}

export interface ConfigSection {
  id: string;
  label: string;
  description?: string;
  fields: ConfigField[];
}

/** One file in the chain that produced the running configuration. */
export interface ConfigOrigin {
  layer: string;
  role: string;
  feature?: string;
  path: string;
}

export interface ReloadStatus {
  overlay_unavailable?: string;
  last_error?: string;
}

export interface RepoView {
  owner: string;
  name: string;
  base: string;
  gate: string[][];
  protect?: string[];
  ecosystem?: string;
  allow_concurrent: boolean;
  max_retries: number;
  review_enabled: boolean;
}

/** A provider's shape, never its key: api_key_env is the NAME of an
 * environment variable, and configured says whether either credential form is
 * set without revealing which. */
export interface ProviderView {
  class: string;
  base_url?: string;
  api_key_env?: string;
  configured: boolean;
}

export interface ConfigView {
  repositories?: RepoView[];
  models?: Record<string, string>;
  providers?: Record<string, ProviderView>;
  provenance?: ConfigOrigin[];
  reload?: ReloadStatus;
  /** Whether this process can apply configuration changes at all. False makes
   * the pages render values rather than controls, which is the honest offer
   * from a process serving a published snapshot: its write routes answer 503
   * (archie-core-ymut). */
  editable: boolean;
  schema?: ConfigSection[];
}

/** A rollback point a dangerous action can target. Number and Label arrive in
 * either case depending on the source, so both are carried. */
export interface DangerCheckpoint {
  Number?: number;
  number?: number;
  Label?: string;
  label?: string;
}

export interface DangerousAction {
  id: string;
  description?: string;
}

export interface DangerousActions {
  pending?: DangerousAction[];
  checkpoints?: DangerCheckpoint[];
  /** Set when the read failed rather than being unwired. */
  error?: string;
}
