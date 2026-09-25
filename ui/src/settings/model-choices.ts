import type { CatalogProvider } from "./types.ts";

/** roleModelOptions lists "provider/model" for every model of a configured
 * provider: a role can only run on a provider Archie is set up to call. */
export function roleModelOptions(catalog: CatalogProvider[], configured: string[]): string[] {
  return catalog
    .filter((p) => configured.includes(p.id))
    .flatMap((p) => p.models.map((m) => `${p.id}/${m}`));
}

/** availableProviders is what the catalog found usable but is not configured
 * yet, the providers Settings offers to enable. */
export function availableProviders(catalog: CatalogProvider[], configured: string[]): CatalogProvider[] {
  return catalog.filter((p) => !configured.includes(p.id));
}
