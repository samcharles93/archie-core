<script setup lang="ts">
import HistoryLink from "@/settings/HistoryLink.vue";
import { computed, onMounted } from "vue";
import { storeToRefs } from "pinia";
import { Plus } from "@lucide/vue";

import PageHeader from "@/base/PageHeader.vue";
import { Button } from "@/components/ui/button";
import { ByteSizeInput } from "@/components/ui/byte-size-input";
import { DurationInput } from "@/components/ui/duration-input";
import { Input } from "@/components/ui/input";
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from "@/components/ui/input-group";
import { SettingRow } from "@/components/ui/setting-row";
import { Switch } from "@/components/ui/switch";
import { resourcesForPage, useControlPlaneStore } from "@/stores/control-plane";
import McpServerEditor, { type McpServer } from "./McpServerEditor.vue";
import SecretRefField from "./SecretRefField.vue";

const KIND = "tool-settings";

interface ToolSettings {
  mcp_servers: McpServer[];
  minimax: { enabled: boolean; api_key_ref: { engine: string; key: string }; credential_configured?: boolean };
  policy: { max_result_chars: number; spill_dir: string };
  web_fetch: { enabled: boolean | null; timeout: string; max_bytes: number; allow_private_networks: boolean };
}

const store = useControlPlaneStore();
const { catalog, catalogError } = storeToRefs(store);
onMounted(store.load);

const resources = computed(() => resourcesForPage(catalog.value, "tools"));
const tools = computed(() => store.drafts[KIND]?.value as ToolSettings | undefined);
const error = computed(() => store.stateFor(KIND).error);

// An unset web_fetch.enabled means on.
const webFetchOn = computed({
  get: () => tools.value?.web_fetch.enabled !== false,
  set: (on: boolean) => {
    if (tools.value) tools.value.web_fetch.enabled = on;
  },
});

function addServer(transport: string) {
  tools.value?.mcp_servers.push({
    name: "",
    transport,
    parallel_tool_calls: false,
    headers_configured: false,
  });
}

const eyebrow = "mb-1 text-[11px] font-medium tracking-[0.06em] text-fg-subtle uppercase";
</script>

<template>
  <div>
    <PageHeader title="Tools & MCP">
      <HistoryLink :kinds="resources.map((r) => r.kind)" />
    </PageHeader>

    <p v-if="catalogError || error" class="mb-4 text-sm text-danger" role="alert">
      {{ catalogError || error }}
    </p>

    <template v-if="tools">
      <h2 :class="eyebrow">MCP servers</h2>
      <div class="grid gap-3 border-t border-border pt-4">
        <McpServerEditor
          v-for="(_, i) in tools.mcp_servers"
          :key="i"
          v-model="tools.mcp_servers[i]!"
          @remove="tools.mcp_servers.splice(i, 1)"
        />
        <p v-if="!tools.mcp_servers.length" class="text-sm text-fg-muted">
          No MCP servers.
        </p>
        <div class="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" @click="addServer('stdio')">
            <Plus data-icon="inline-start" /> stdio command
          </Button>
          <Button variant="outline" size="sm" @click="addServer('http')">
            <Plus data-icon="inline-start" /> HTTP endpoint
          </Button>
          <Button variant="outline" size="sm" @click="addServer('sse')">
            <Plus data-icon="inline-start" /> SSE endpoint
          </Button>
        </div>
      </div>

      <h2 :class="[eyebrow, 'mt-10']">MiniMax</h2>
      <SettingRow label="Enabled">
        <Switch v-model="tools.minimax.enabled" aria-label="MiniMax enabled" />
      </SettingRow>
      <SettingRow label="API key">
        <SecretRefField
          v-model="tools.minimax.api_key_ref"
          id-prefix="minimax-key"
          :disabled="!tools.minimax.enabled"
        />
      </SettingRow>

      <h2 :class="[eyebrow, 'mt-10']">Web fetch</h2>
      <SettingRow label="Enabled">
        <Switch v-model="webFetchOn" aria-label="Web fetch enabled" />
      </SettingRow>
      <SettingRow label="Max download" for="wf-bytes">
        <ByteSizeInput id="wf-bytes" v-model="tools.web_fetch.max_bytes" :disabled="!webFetchOn" />
      </SettingRow>
      <SettingRow label="Timeout" for="wf-timeout">
        <DurationInput id="wf-timeout" v-model="tools.web_fetch.timeout" :units="['s', 'm']" :disabled="!webFetchOn" />
      </SettingRow>
      <SettingRow
        label="Allow private networks"
        hint="Reaches this host's dashboard and Docker API."
        :tone="tools.web_fetch.allow_private_networks ? 'danger' : 'default'"
      >
        <Switch
          v-model="tools.web_fetch.allow_private_networks"
          aria-label="Allow private networks"
          :disabled="!webFetchOn"
        />
      </SettingRow>

      <h2 :class="[eyebrow, 'mt-10']">Tool output</h2>
      <SettingRow label="Max result length" for="tp-chars" :hint="`≈ ${Math.round(tools.policy.max_result_chars / 4).toLocaleString()} tokens`">
        <InputGroup class="w-48">
          <InputGroupInput id="tp-chars" v-model.number="tools.policy.max_result_chars" type="number" min="0" class="font-mono" />
          <InputGroupAddon align="inline-end">
            <InputGroupText class="font-mono text-xs">chars</InputGroupText>
          </InputGroupAddon>
        </InputGroup>
      </SettingRow>
      <SettingRow label="Spill directory" for="tp-spill" hint="Empty: truncate.">
        <Input id="tp-spill" v-model="tools.policy.spill_dir" class="max-w-md font-mono" />
      </SettingRow>
    </template>

  </div>
</template>
