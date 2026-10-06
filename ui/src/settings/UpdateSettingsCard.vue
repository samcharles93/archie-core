<script setup lang="ts">
import { computed, onMounted } from "vue";

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { SettingRow } from "@/components/ui/setting-row";
import { useControlPlaneStore } from "@/stores/control-plane";

const KIND = "update-settings";
type Commands = { check_command: string[] | null; install_command: string[] | null };

const store = useControlPlaneStore();
onMounted(store.load);

const draft = computed(() => store.drafts[KIND]?.value as Commands | undefined);
const error = computed(() => store.stateFor(KIND).error);

// Each command is an argv array, edited as one line split on whitespace.
function argv(field: keyof Commands) {
  return computed({
    get: () => (draft.value?.[field] ?? []).join(" "),
    set: (line: string) => {
      if (draft.value) draft.value[field] = line.split(/\s+/).filter(Boolean);
    },
  });
}
const check = argv("check_command");
const install = argv("install_command");
</script>

<template>
  <Card v-if="draft">
    <CardHeader>
      <CardTitle>Updates</CardTitle>
    </CardHeader>
    <CardContent>
      <p v-if="error" class="mb-2 text-sm text-danger" role="alert">{{ error }}</p>
      <SettingRow label="Check command" for="update-check" hint="Prints the available releases as JSON.">
        <Input id="update-check" v-model="check" class="font-mono" placeholder="archie-update-check" />
      </SettingRow>
      <SettingRow label="Install command" for="update-install" hint="Installs the release you approve. Empty hides Install.">
        <Input id="update-install" v-model="install" class="font-mono" placeholder="archie-update-install" />
      </SettingRow>
    </CardContent>
  </Card>
</template>
