<script setup lang="ts">
import { computed, ref } from "vue";

import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import type { ChatUpdate } from "./state";

const props = defineProps<{ update: ChatUpdate }>();

interface Component {
  Label?: string;
  ID?: string;
  Installed?: string;
  Available?: string;
  Changelog?: string;
}

const available = computed(() => (props.update.available ?? []) as Component[]);
const state = ref<"idle" | "installing" | "deferred" | "done" | "failed">("idle");
const result = ref("");

async function install() {
  state.value = "installing";
  try {
    const out = await api.updateInstall<{ result?: { installed?: Record<string, string> } }>(props.update.snapshot);
    const installed = out.result?.installed;
    result.value = installed
      ? Object.entries(installed).map(([id, v]) => `${id} → ${v}`).join(", ") + ". Archie restarts to apply it."
      : "Update finished.";
    state.value = "done";
  } catch (err) {
    result.value = (err as Error).message;
    state.value = "failed";
  }
}

async function later() {
  try {
    await api.updateDefer(props.update.snapshot);
    state.value = "deferred";
  } catch (err) {
    result.value = (err as Error).message;
    state.value = "failed";
  }
}
</script>

<template>
  <div class="text-sm">
    <p v-if="!available.length">Archie is up to date.</p>
    <template v-else>
      <p class="font-medium">An update is available</p>
      <ul class="mt-1 space-y-0.5">
        <li v-for="c in available" :key="c.ID">
          {{ c.Label || c.ID }}: <span class="font-mono">{{ c.Installed || "?" }} → {{ c.Available }}</span>
          <a v-if="c.Changelog" :href="c.Changelog" target="_blank" rel="noopener" class="ml-1 text-xs underline">changes</a>
        </li>
      </ul>
      <div v-if="state === 'idle'" class="mt-3 flex gap-2">
        <Button v-if="update.can_install" size="sm" @click="install">Install</Button>
        <Button size="sm" variant="ghost" @click="later">Not now</Button>
      </div>
      <p v-else-if="state === 'installing'" class="mt-2 text-fg-muted">Installing… this can take a few minutes.</p>
      <p v-else-if="state === 'deferred'" class="mt-2 text-fg-muted">Skipped until a newer release.</p>
      <p v-else-if="state === 'done'" class="mt-2 text-ok">{{ result }}</p>
      <p v-else class="mt-2 text-danger">{{ result }}</p>
      <p v-if="state === 'idle' && !update.can_install" class="mt-2 text-xs text-fg-muted">
        This host has no install script, so the update can't be installed from here.
      </p>
    </template>
  </div>
</template>
