<script setup lang="ts">
import { onMounted, ref } from "vue";
import { Plus } from "@lucide/vue";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

interface PersonalToken {
  id: string;
  created_at: string;
}

const tokens = ref<PersonalToken[]>([]);
const created = ref("");
const error = ref("");
const busy = ref(false);

async function call<T>(path: string, init: RequestInit = {}): Promise<T | undefined> {
  const response = await fetch(path, {
    ...init,
    headers: { Accept: "application/json", "X-Archie-CSRF": "1" },
  });
  if (!response.ok) throw new Error((await response.text()).trim());
  return response.status === 204 ? undefined : ((await response.json()) as T);
}

async function run(action: () => Promise<void>): Promise<void> {
  busy.value = true;
  try {
    await action();
    error.value = "";
  } catch (cause) {
    error.value = String((cause as Error).message || cause);
  } finally {
    busy.value = false;
  }
}

const load = () =>
  run(async () => {
    tokens.value = (await call<{ tokens: PersonalToken[] }>("/api/tokens"))?.tokens ?? [];
  });

const create = () =>
  run(async () => {
    created.value = (await call<{ token: string }>("/api/tokens", { method: "POST" }))?.token ?? "";
    await load();
  });

const revoke = (id: string) =>
  run(async () => {
    await call(`/api/tokens/${id}`, { method: "DELETE" });
    await load();
  });

onMounted(load);
</script>

<template>
  <section class="mb-8" aria-labelledby="personal-tokens">
    <div class="mb-3 flex items-center justify-between gap-2">
      <h2 id="personal-tokens" class="text-sm font-medium">Your API tokens</h2>
      <Button size="sm" :disabled="busy" @click="create"><Plus data-icon="inline-start" /> New token</Button>
    </div>
    <p class="mb-3 text-xs text-fg-subtle">
      A token acts as you. Send it as <code>Authorization: Bearer &lt;token&gt;</code>.
    </p>
    <p v-if="error" role="alert" class="mb-3 text-sm text-danger">{{ error }}</p>
    <div v-if="created" class="mb-3 rounded-lg border border-border bg-card p-3">
      <p class="mb-2 text-xs">Copy this token now. It is not shown again.</p>
      <Input :model-value="created" readonly aria-label="New token" @focus="($event.target as HTMLInputElement).select()" />
    </div>
    <ul v-if="tokens.length" class="divide-y divide-border rounded-lg border border-border bg-card" aria-label="API tokens">
      <li v-for="token in tokens" :key="token.id" class="flex items-center gap-3 px-4 py-2.5">
        <code class="flex-1 truncate text-xs">{{ token.id.slice(0, 12) }}</code>
        <span class="text-xs text-fg-subtle">{{ new Date(token.created_at).toLocaleString() }}</span>
        <Button variant="ghost" size="sm" class="text-danger" :disabled="busy" @click="revoke(token.id)">Revoke</Button>
      </li>
    </ul>
  </section>
</template>
