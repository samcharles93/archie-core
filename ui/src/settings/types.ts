/** The read-only configuration summary served by /api/config. */

export interface ReloadStatus {
  last_error?: string;
}

/** The effective pr-review policy after the review-settings resource is
 * layered over the file's [review] section. */
export interface ReviewView {
  precision_gate: boolean;
  approve_before_post: boolean;
}

/** A provider the model catalog found usable, with its model ids. */
export interface CatalogProvider {
  id: string;
  name: string;
  class: string;
  api_key_env?: string;
  base_url?: string;
  models: string[];
}

export interface ConfigView {
  catalog?: CatalogProvider[];
  review?: ReviewView;
  reload?: ReloadStatus;
 bootstrap?:Record<string,string>;
}
