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
  skills_dir: string;
}

const store = useControlPlaneStore();
const { catalog, catalogError } = storeToRefs(store);
onMounted(store.load);

const resources = computed(() => resourcesForPage(catalog.value, "plugins"));
const dirs = computed(() => store.drafts[KIND]?.value as PluginSettings | undefined);
const error = computed(() => store.pageErrorFor(KIND));
</script>

<template>
  <div>
    <PageHeader title="Plugins">
      <HistoryLink :kinds="resources.map((r) => r.kind)" />
    </PageHeader>

    <p v-if="catalogError || error" class="mb-4 text-sm text-danger" role="alert">{{ catalogError || error }}</p>

    <template v-if="dirs">
      <SettingRow label="Skills directory" for="pl-skills" hint="Empty: work directory. Applies on restart.">
        <div class="flex flex-wrap items-center gap-3">
          <Input id="pl-skills" v-model="dirs.skills_dir" class="max-w-md font-mono" />
          <DraftHint :kind="KIND" path="skills_dir" />
        </div>
      </SettingRow>
      <!-- A restart-required kind can only be applied by a restart, so its
           per-process versions live below: a process on an older one is the
           restart owed. -->
      <ApplyStatusRows :kind="KIND" class="border-t border-border py-4" />
    </template>
  </div>
</template>
