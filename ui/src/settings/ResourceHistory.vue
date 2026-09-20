<script setup lang="ts">
import { computed, ref } from "vue";
import { RotateCcw } from "@lucide/vue";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { ago } from "@/lib/format";
import { useControlPlaneStore, type ResourceRevision } from "@/stores/control-plane";

/**
 * A resource's audit trail, and the way back to any earlier value.
 *
 * Restoring is not its own command: a revision carries the value it held, so
 * putting it back is an ordinary replace against the version currently stored.
 * That keeps the restore in the same history as every other edit rather than
 * rewriting the past.
 *
 * The list loads when it is opened. A settings page that nobody is auditing
 * should not pay for the query on every render.
 */
const props = defineProps<{ kind: string }>();

const store = useControlPlaneStore();
const state = computed(() => store.stateFor(props.kind));
const revisions = ref<ResourceRevision[]>([]);
const open = ref(false);
const loading = ref(false);
const error = ref("");

async function load(): Promise<void> {
  loading.value = true;
  error.value = "";
  try {
    revisions.value = await store.history(props.kind);
  } catch (err) {
    error.value = String((err as Error).message || err);
  } finally {
    loading.value = false;
  }
}

async function toggle(): Promise<void> {
  open.value = !open.value;
  if (open.value) await load();
}

async function restore(revision: ResourceRevision): Promise<void> {
  if (await store.replace(props.kind, revision.value)) await load();
}
</script>

<template>
  <div class="mt-5 border-t border-border pt-4">
    <Button variant="ghost" size="sm" :aria-expanded="open" @click="toggle">
      <Spinner v-if="loading" data-icon="inline-start" />
      {{ open ? "Hide history" : "History" }}
    </Button>

    <p v-if="error" class="mt-2 text-sm text-destructive" role="alert">{{ error }}</p>

    <div v-else-if="open && !loading" class="mt-3">
      <p v-if="!revisions.length" class="text-sm text-muted-foreground">No changes recorded yet.</p>
      <ul v-else class="space-y-1">
        <li
          v-for="revision in revisions"
          :key="revision.version"
          class="grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-3 border-t border-border py-2 first:border-t-0"
        >
          <Badge :variant="revision.version === state.resource?.version ? 'ok' : 'idle'">v{{ revision.version }}</Badge>
          <span class="min-w-0 truncate text-sm text-fg-muted" :title="`${revision.actor} via ${revision.source}`">
            {{ revision.actor }} <span class="text-fg-subtle">via {{ revision.source }}</span>
            <span v-if="revision.at" class="text-fg-subtle"> · {{ ago(revision.at) }}</span>
          </span>
          <Button
            v-if="revision.version !== state.resource?.version"
            variant="outline"
            size="sm"
            :disabled="state.saving"
            :title="`Replace the current value with version ${revision.version}`"
            @click="restore(revision)"
          >
            <RotateCcw /> Restore
          </Button>
        </li>
      </ul>
    </div>
  </div>
</template>
