<script setup lang="ts">
import { computed, onMounted } from "vue";
import { storeToRefs } from "pinia";

import PageHeader from "@/base/PageHeader.vue";
import { Input } from "@/components/ui/input";
import { SettingRow } from "@/components/ui/setting-row";
import { StatusPill } from "@/components/ui/status-pill";
import { resourcesForPage, useControlPlaneStore } from "@/stores/control-plane";
import HistoryLink from "./HistoryLink.vue";

const KIND = "plugin-settings";

interface PluginSettings {
  plugin_dir: string;
  module_dir: string;
  secret_engine_dir: string;
  skills_dir: string;
}

const store = useControlPlaneStore();
const { catalog, catalogError } = storeToRefs(store);
onMounted(store.load);

const resources = computed(() => resourcesForPage(catalog.value, "plugins"));
const dirs = computed(() => store.drafts[KIND]?.value as PluginSettings | undefined);
const error = computed(() => store.stateFor(KIND).error);
</script>

<template>
  <div>
    <PageHeader title="Plugins">
      <HistoryLink :kinds="resources.map((r) => r.kind)" />
      <StatusPill tone="warn">Applies after restart</StatusPill>
    </PageHeader>
    <p class="-mt-4 mb-8 text-sm text-fg-muted">Directories Archie loads extensions from at startup.</p>

    <p v-if="catalogError || error" class="mb-4 text-sm text-danger" role="alert">{{ catalogError || error }}</p>

    <template v-if="dirs">
      <SettingRow label="Plugin directory" for="pl-plugins" hint="Daemon plugins. Empty loads none.">
        <Input id="pl-plugins" v-model="dirs.plugin_dir" class="max-w-md font-mono" />
      </SettingRow>
      <SettingRow label="Module directory" for="pl-modules" hint="Workflow modules. Empty loads none.">
        <Input id="pl-modules" v-model="dirs.module_dir" class="max-w-md font-mono" />
      </SettingRow>
      <SettingRow label="Secret engine directory" for="pl-secrets" hint="Secret engine plugins. Empty uses the built-in engines only.">
        <Input id="pl-secrets" v-model="dirs.secret_engine_dir" class="max-w-md font-mono" />
      </SettingRow>
      <SettingRow label="Skills directory" for="pl-skills" hint="Shared SKILL.md skills. Empty uses the work directory.">
        <Input id="pl-skills" v-model="dirs.skills_dir" class="max-w-md font-mono" />
      </SettingRow>
    </template>
  </div>
</template>
