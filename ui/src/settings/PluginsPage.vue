<script setup lang="ts">
import { computed, onMounted } from "vue";
import { storeToRefs } from "pinia";

import PageHeader from "@/base/PageHeader.vue";
import { Input } from "@/components/ui/input";
import { SettingRow } from "@/components/ui/setting-row";
import { resourcesForPage, useControlPlaneStore } from "@/stores/control-plane";
import ApplyStatusRows from "./ApplyStatusRows.vue";
import DraftHint from "./DraftHint.vue";
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
    </PageHeader>

    <p v-if="catalogError || error" class="mb-4 text-sm text-danger" role="alert">{{ catalogError || error }}</p>

    <template v-if="dirs">
      <SettingRow label="Plugin directory" for="pl-plugins">
        <div class="flex flex-wrap items-center gap-3">
          <Input id="pl-plugins" v-model="dirs.plugin_dir" class="max-w-md font-mono" />
          <DraftHint :kind="KIND" path="plugin_dir" />
        </div>
      </SettingRow>
      <SettingRow label="Module directory" for="pl-modules">
        <div class="flex flex-wrap items-center gap-3">
          <Input id="pl-modules" v-model="dirs.module_dir" class="max-w-md font-mono" />
          <DraftHint :kind="KIND" path="module_dir" />
        </div>
      </SettingRow>
      <SettingRow label="Secret engine directory" for="pl-secrets" hint="Empty: built-in engines only.">
        <div class="flex flex-wrap items-center gap-3">
          <Input id="pl-secrets" v-model="dirs.secret_engine_dir" class="max-w-md font-mono" />
          <DraftHint :kind="KIND" path="secret_engine_dir" />
        </div>
      </SettingRow>
      <SettingRow label="Skills directory" for="pl-skills" hint="Empty: work directory.">
        <div class="flex flex-wrap items-center gap-3">
          <Input id="pl-skills" v-model="dirs.skills_dir" class="max-w-md font-mono" />
          <DraftHint :kind="KIND" path="skills_dir" />
        </div>
      </SettingRow>
      <!-- A live-apply kind earns no restart banner, so its per-process
           versions are where a process still on an older one shows. -->
      <ApplyStatusRows :kind="KIND" class="border-t border-border py-4" />
    </template>
  </div>
</template>
