<script setup lang="ts">
import HistoryLink from "@/settings/HistoryLink.vue";
import { computed, onMounted, reactive, ref } from "vue";
import { storeToRefs } from "pinia";
import { Globe, Mail, Send } from "@lucide/vue";

import PageHeader from "@/base/PageHeader.vue";
import { Button } from "@/components/ui/button";
import { DurationInput } from "@/components/ui/duration-input";
import { Input } from "@/components/ui/input";
import {
  NumberField,
  NumberFieldContent,
  NumberFieldDecrement,
  NumberFieldIncrement,
  NumberFieldInput,
} from "@/components/ui/number-field";
import { SegmentedControl } from "@/components/ui/segmented-control";
import { SettingRow } from "@/components/ui/setting-row";
import { StatusPill } from "@/components/ui/status-pill";
import { Switch } from "@/components/ui/switch";
import {
  TagsInput,
  TagsInputInput,
  TagsInputItem,
  TagsInputItemDelete,
  TagsInputItemText,
} from "@/components/ui/tags-input";
import { api } from "@/lib/api";
import SecretRefField from "@/settings/SecretRefField.vue";
import { resourcesForPage, useControlPlaneStore } from "@/stores/control-plane";
import { useLiveResource } from "@/stores/live-updates";

const KIND = "channel-settings";

interface SecretRef {
  engine: string;
  key: string;
}
interface ChannelSettings {
  operator: string;
  show_tool_calls: boolean;
  max_steps: number;
  models: string[] | null;
  email: { listen_addr: string; relay_addr: string };
  webhook_addr: string;
  webhook: { path: string; secret_ref: SecretRef; credential_configured: boolean; template: string; deliver_to: string };
  telegram: { allowed_user_ids: number[] | null; token_ref: SecretRef; credential_configured: boolean };
  rate_limit: { window: string; max_requests: number };
  unrestricted_filesystem: boolean;
  workspace: string;
}

/** What the messaging service reports about a running channel. */
interface ChannelStatus {
  id?: string;
  state?: string;
  detail?: string;
}

const store = useControlPlaneStore();
const { catalog, catalogError } = storeToRefs(store);
const resources = computed(() => resourcesForPage(catalog.value, "channels"));
const chat = computed(() => store.drafts[KIND]?.value as ChannelSettings | undefined);
const error = computed(() => store.stateFor(KIND).error);

const statuses = ref<ChannelStatus[]>([]);
async function loadStatus() {
  try {
    statuses.value = (await api.channels<{ channels?: ChannelStatus[] }>())?.channels ?? [];
  } catch {
    statuses.value = [];
  }
}
useLiveResource(null, () => void loadStatus());
onMounted(() => Promise.all([loadStatus(), store.load()]));

// Running state comes only from the messaging service's report; a channel it
// does not mention shows none rather than one guessed from its settings.
function statusOf(id: string) {
  const state = statuses.value.find((s) => s.id === id)?.state;
  if (!state) return undefined;
  if (state === "running") return { label: "Running", tone: "neutral", dot: "live" } as const;
  if (state === "failed") return { label: "Failed", tone: "danger", dot: "danger" } as const;
  if (state === "degraded") return { label: "Degraded", tone: "warn", dot: "warn" } as const;
  return { label: state[0]!.toUpperCase() + state.slice(1), tone: "neutral", dot: "idle" } as const;
}

// A channel is set up once its enabling field is filled in, the rule the
// daemon applies when it decides what to start.
const configured = computed(() => ({
  telegram: !!chat.value?.telegram.token_ref.key,
  email: !!chat.value?.email.listen_addr,
  webhook: !!chat.value?.webhook_addr,
}));
const opened = reactive({ telegram: false, email: false, webhook: false });
const shown = (id: keyof typeof opened) => configured.value[id] || opened[id];

const channels = [
  { id: "telegram", title: "Telegram", icon: Send },
  { id: "email", title: "Email", icon: Mail },
  { id: "webhook", title: "Webhook", icon: Globe },
] as const;

const allowedUsers = computed({
  get: () => (chat.value?.telegram.allowed_user_ids ?? []).map(String),
  set: (ids: string[]) => {
    if (chat.value)
      chat.value.telegram.allowed_user_ids = ids.filter((id) => /^\d+$/.test(id)).map(Number);
  },
});
const models = computed({
  get: () => chat.value?.models ?? [],
  set: (list: string[]) => {
    if (chat.value) chat.value.models = list;
  },
});
const replyModes = [
  { value: "", label: "No reply" },
  { value: "origin", label: "HTTP response" },
];
</script>

<template>
  <div>
    <PageHeader title="Channels">
      <HistoryLink :kinds="resources.map((r) => r.kind)" />
      <StatusPill tone="warn">Applies after restart</StatusPill>
    </PageHeader>

    <p v-if="catalogError || error" class="mb-4 text-sm text-danger" role="alert">
      {{ catalogError || error }}
    </p>

    <template v-if="chat">
      <section
        v-for="channel in channels"
        :key="channel.id"
        class="mb-4 rounded-lg border border-border bg-card"
        :aria-labelledby="`ch-${channel.id}`"
      >
        <header class="flex items-center gap-3 px-5 py-4">
          <span class="grid size-8 shrink-0 place-items-center rounded-md bg-secondary text-muted-foreground">
            <component :is="channel.icon" class="size-4" aria-hidden="true" />
          </span>
          <div class="min-w-0 flex-1">
            <h2 :id="`ch-${channel.id}`" class="text-[15px] font-medium">{{ channel.title }}</h2>
          </div>
          <StatusPill v-if="statusOf(channel.id)" :tone="statusOf(channel.id)!.tone" :dot="statusOf(channel.id)!.dot">
            {{ statusOf(channel.id)!.label }}
          </StatusPill>
          <template v-if="!configured[channel.id]">
            <StatusPill>Not set up</StatusPill>
            <Button v-if="!opened[channel.id]" variant="outline" size="sm" @click="opened[channel.id] = true">
              Set up
            </Button>
          </template>
        </header>

        <div v-if="shown(channel.id)" class="px-5 pb-2">
          <template v-if="channel.id === 'telegram'">
            <SettingRow label="Bot token" hint="Empty disables Telegram.">
              <SecretRefField
                v-model="chat.telegram.token_ref"
                id-prefix="tg-token"
              />
            </SettingRow>
            <SettingRow label="Allowed users" hint="Everyone else is ignored.">
              <TagsInput v-model="allowedUsers" aria-label="Allowed users" class="font-mono">
                <TagsInputItem v-for="id in allowedUsers" :key="id" :value="id">
                  <TagsInputItemText />
                  <TagsInputItemDelete />
                </TagsInputItem>
                <TagsInputInput placeholder="Add user ID" />
              </TagsInput>
            </SettingRow>
          </template>

          <template v-else-if="channel.id === 'email'">
            <SettingRow label="Listen address" for="em-listen" hint="Empty disables email.">
              <Input id="em-listen" v-model="chat.email.listen_addr" class="max-w-sm font-mono" placeholder=":2525" />
            </SettingRow>
            <SettingRow label="Relay address" for="em-relay">
              <Input id="em-relay" v-model="chat.email.relay_addr" class="max-w-sm font-mono" />
            </SettingRow>
          </template>

          <template v-else>
            <SettingRow label="Listen address" for="wh-addr" hint="Empty disables the webhook.">
              <Input id="wh-addr" v-model="chat.webhook_addr" class="max-w-sm font-mono" placeholder=":8686" />
            </SettingRow>
            <SettingRow label="Path" for="wh-path">
              <Input id="wh-path" v-model="chat.webhook.path" class="max-w-sm font-mono" placeholder="/webhook" />
            </SettingRow>
            <SettingRow label="Message field" for="wh-template" hint="Dot path. Empty: whole body.">
              <Input id="wh-template" v-model="chat.webhook.template" class="max-w-sm font-mono" placeholder="issue.title" />
            </SettingRow>
            <SettingRow label="Replies">
              <SegmentedControl v-model="chat.webhook.deliver_to" label="Replies" :options="replyModes" />
            </SettingRow>
            <SettingRow
              label="Signing secret"
              hint="Unset accepts unsigned requests."
              :tone="chat.webhook.secret_ref.key ? 'default' : 'danger'"
            >
              <SecretRefField
                v-model="chat.webhook.secret_ref"
                id-prefix="wh-secret"
              />
            </SettingRow>
          </template>
        </div>
      </section>

      <section class="mt-8 rounded-lg border border-border bg-card px-5 pt-4 pb-2" aria-labelledby="ch-defaults">
        <h2 id="ch-defaults" class="text-[11px] font-medium tracking-[0.06em] text-fg-subtle uppercase">
          Chat session defaults · all channels
        </h2>
        <SettingRow label="Workspace" for="cs-workspace" hint="Empty disables file tools.">
          <Input id="cs-workspace" v-model="chat.workspace" class="max-w-md font-mono" />
        </SettingRow>
        <SettingRow
          label="Unrestricted filesystem"
          hint="File tools can write anywhere."
          :tone="chat.unrestricted_filesystem ? 'danger' : 'default'"
        >
          <Switch v-model="chat.unrestricted_filesystem" aria-label="Unrestricted filesystem" />
        </SettingRow>
        <SettingRow label="Show tool calls">
          <Switch v-model="chat.show_tool_calls" aria-label="Show tool calls" />
        </SettingRow>
        <SettingRow label="Max steps" hint="0 = default (100).">
          <NumberField v-model="chat.max_steps" :min="0" class="w-32">
            <NumberFieldContent>
              <NumberFieldDecrement />
              <NumberFieldInput class="font-mono" aria-label="Max steps" />
              <NumberFieldIncrement />
            </NumberFieldContent>
          </NumberField>
        </SettingRow>
        <SettingRow label="Rate limit" hint="Per sender. Off while either is 0.">
          <div class="flex flex-wrap items-center gap-2 text-sm text-fg-muted">
            <NumberField v-model="chat.rate_limit.max_requests" :min="0" class="w-28">
              <NumberFieldContent>
                <NumberFieldInput class="font-mono" aria-label="Requests" />
              </NumberFieldContent>
            </NumberField>
            requests per
            <DurationInput v-model="chat.rate_limit.window" :units="['s', 'm', 'h']" />
          </div>
        </SettingRow>
        <SettingRow label="Models" hint="Empty: role models.">
          <TagsInput v-model="models" aria-label="Models" class="font-mono">
            <TagsInputItem v-for="model in models" :key="model" :value="model">
              <TagsInputItemText />
              <TagsInputItemDelete />
            </TagsInputItem>
            <TagsInputInput placeholder="provider/model" />
          </TagsInput>
        </SettingRow>
        <SettingRow label="Operator" for="cs-operator">
          <Input id="cs-operator" v-model="chat.operator" class="max-w-sm" />
        </SettingRow>
      </section>
    </template>

  </div>
</template>
