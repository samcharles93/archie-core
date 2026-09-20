<script setup lang="ts">
import { ref } from "vue";
import PageHeader from "@/base/PageHeader.vue";
import { useIdentitiesStore, type Identity } from "@/stores/identities";

const store = useIdentitiesStore();
const name = ref("");
const kind = ref<Identity["kind"]>("bot");
store.watch();

async function create(): Promise<void> { if (await store.create(name.value, kind.value)) name.value = ""; }
async function rename(value: Identity): Promise<void> { const next = window.prompt("Display name", value.display_name); if (next && next !== value.display_name) await store.command(value, "rename", next); }
async function retire(value: Identity): Promise<void> { if (window.confirm(`Retire ${value.display_name}? This cannot be undone.`)) await store.command(value, "retire"); }
</script>

<template>
  <main class="page identities-page">
    <PageHeader title="Identities" />
    <p v-if="store.error" role="alert" class="error">{{ store.error }}</p>
    <form class="identity-create" @submit.prevent="create">
      <input v-model.trim="name" required aria-label="Display name" placeholder="Display name">
      <select v-model="kind" aria-label="Kind"><option value="bot">Bot</option><option value="service_account">Service account</option><option value="user">User</option></select>
      <button type="submit" :disabled="!name || !!store.busy">Create</button>
    </form>
    <section aria-label="Identity list" class="identity-list">
      <article v-for="value in store.identities" :key="value.id" class="identity-row">
        <div><strong>{{ value.display_name }}</strong><span>{{ value.kind.replace('_', ' ') }} · {{ value.lifecycle }}</span></div>
        <div v-if="value.kind !== 'system'" class="actions">
          <button :disabled="!!store.busy || value.lifecycle === 'retired'" @click="rename(value)">Rename</button>
          <button v-if="value.lifecycle === 'active'" :disabled="!!store.busy" @click="store.command(value, 'suspend')">Suspend</button>
          <button v-if="value.lifecycle === 'suspended'" :disabled="!!store.busy" @click="store.command(value, 'reactivate')">Reactivate</button>
          <button class="danger" :disabled="!!store.busy || value.lifecycle === 'retired'" @click="retire(value)">Retire</button>
        </div>
      </article>
    </section>
  </main>
</template>

<style scoped>
.identities-page{max-width:960px}.identity-create{display:flex;gap:.75rem;margin:1.5rem 0}.identity-create input{flex:1}.identity-create input,.identity-create select,.identity-create button,.actions button{border:1px solid var(--border);border-radius:.5rem;background:var(--surface);color:inherit;padding:.6rem .8rem}.identity-list{border-top:1px solid var(--border)}.identity-row{display:flex;align-items:center;justify-content:space-between;gap:1rem;padding:1rem 0;border-bottom:1px solid var(--border)}.identity-row span{display:block;color:var(--muted-foreground);font-size:.85rem;margin-top:.2rem;text-transform:capitalize}.actions{display:flex;gap:.5rem}.actions .danger{color:var(--destructive)}.error{color:var(--destructive)}
</style>
