import { ref } from "vue";

import { api } from "@/lib/api";
import type { Source } from "./source-signing";

/**
 * The sources list and its writes. A write that generates a secret keeps the
 * returned source in `revealed` until dismissed: it is the only time the
 * browser ever holds that secret.
 */
export function useSources() {
  // null means loading, [] means loaded and empty.
  const sources = ref<Source[] | null>(null);
  const failure = ref<string | null>(null);
  const revealed = ref<Source | null>(null);
  const busy = ref(false);

  function message(err: unknown): string {
    return err instanceof Error && err.message ? err.message : String(err);
  }

  async function load(): Promise<void> {
    try {
      const res = await api.sources<{ sources?: Source[] }>();
      sources.value = res.sources || [];
      failure.value = null;
    } catch (err) {
      failure.value = message(err);
    }
  }

  async function run(write: () => Promise<Source | void>): Promise<void> {
    busy.value = true;
    failure.value = null;
    try {
      const result = await write();
      if (result?.secret) revealed.value = result;
      await load();
    } catch (err) {
      failure.value = message(err);
    } finally {
      busy.value = false;
    }
  }

  return {
    sources,
    failure,
    revealed,
    busy,
    load,
    create: (path: string) => run(() => api.sourceCreate<Source>(path.trim())),
    newSecret: (source: Source) => run(() => api.sourceSecret<Source>(source.path)),
    requestUnsigned: (source: Source) => run(() => api.sourceSigning<Source>(source.path, false)),
    requireSigning: (source: Source) => run(() => api.sourceSigning<Source>(source.path, true)),
    approveUnsigned: (source: Source) => run(() => api.sourceApproveUnsigned<Source>(source.path)),
  };
}
