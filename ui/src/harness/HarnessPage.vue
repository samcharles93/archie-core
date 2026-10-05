<script setup lang="ts">
/**
 * Harness: the OAuth credential bindings a Kit profile resolves, and the
 * setup terminal that captures a binding's tokens
 * (docs/prds/external-agent-harness.md). The binding list is a State Store
 * read; the terminal is the webui route's WebSocket, and it reports
 * explicitly when this process cannot open one rather than offering a
 * control that would fail.
 */
import { onMounted, ref } from "vue";

import PageHeader from "@/base/PageHeader.vue";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import type { BadgeVariants } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { api, ApiError } from "@/lib/api";
import SetupTerminal from "./SetupTerminal.vue";
import {
  bindingStatus,
  type BindingStatus,
  type HarnessBinding,
  type HarnessState,
} from "./harness";

const state = ref<HarnessState | null>(null);
const loading = ref(true);
const failure = ref<string | null>(null);
// "unconfigured" is a process with no control-plane resource or no token
// store (501/503) -- the feature is off, which reads differently from a
// process that answered badly.
const unconfigured = ref(false);
const profile = ref("");
const terminalOpen = ref(false);

const statuses: Record<
  BindingStatus,
  { label: string; variant: BadgeVariants["variant"] }
> = {
  ready: { label: "Ready", variant: "ok" },
  expiring: { label: "Expiring soon", variant: "warn" },
  expired: { label: "Expired", variant: "danger" },
  unconfigured: { label: "Not set up", variant: "idle" },
};

onMounted(load);

async function load(): Promise<void> {
  loading.value = true;
  failure.value = null;
  unconfigured.value = false;
  try {
    const got = await api.harnessBindings<HarnessState>();
    state.value = {
      terminal: got.terminal,
      bindings: got.bindings ?? [],
      profiles: got.profiles ?? [],
    };
    profile.value = state.value.profiles[0] ?? "";
  } catch (err) {
    const status = err instanceof ApiError ? err.status : undefined;
    if (status === 501 || status === 503) {
      unconfigured.value = true;
    } else {
      failure.value = err instanceof Error ? err.message : String(err);
    }
    state.value = null;
  } finally {
    loading.value = false;
  }
}

function openTerminal(): void {
  if (!state.value?.terminal || !profile.value) return;
  terminalOpen.value = true;
}

function formatTimestamp(value?: string): string {
  if (!value) return "—";
  const at = new Date(value);
  return Number.isNaN(at.getTime()) ? value : at.toLocaleString();
}

function scopes(binding: HarnessBinding): string {
  return binding.scopes?.length ? binding.scopes.join(", ") : "—";
}
</script>

<template>
  <div>
    <PageHeader title="Harness" />

    <p v-if="failure" class="mb-4 text-sm text-danger" role="alert">
      {{ failure }}
    </p>

    <div v-if="loading" class="space-y-2">
      <Skeleton class="h-24 w-full" />
      <Skeleton class="h-40 w-full" />
    </div>

    <Empty v-else-if="unconfigured">
      <EmptyHeader>
        <EmptyTitle>Harness bindings are not configured</EmptyTitle>
        <EmptyDescription>
          This process cannot read credential bindings or captured OAuth tokens.
        </EmptyDescription>
      </EmptyHeader>
    </Empty>

    <template v-else-if="state">
      <Alert v-if="!state.terminal" class="mb-4">
        <AlertTitle>Setup terminal unavailable</AlertTitle>
        <AlertDescription>
          This process cannot open a Kit container. Bindings are still listed
          below.
        </AlertDescription>
      </Alert>

      <Card v-else class="mb-4">
        <CardContent class="flex flex-wrap items-end gap-3">
          <label class="flex flex-col gap-1 text-sm">
            <span class="text-muted-foreground">Kit profile</span>
            <select
              v-model="profile"
              class="h-9 min-w-48 rounded-md border border-input bg-background px-2 text-sm"
              :disabled="!state.profiles.length"
            >
              <option v-if="!state.profiles.length" value="">
                No Kit profiles configured
              </option>
              <option v-for="name in state.profiles" :key="name" :value="name">
                {{ name }}
              </option>
            </select>
          </label>
          <Button :disabled="!profile" @click="openTerminal">
            Open setup terminal
          </Button>
        </CardContent>
      </Card>

      <Card v-if="terminalOpen && profile" class="mb-4">
        <CardContent class="h-96">
          <SetupTerminal :profile="profile" />
        </CardContent>
      </Card>

      <Card>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Service</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Expires</TableHead>
                <TableHead>Scopes</TableHead>
                <TableHead>Updated</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              <TableRow v-if="!state.bindings.length">
                <TableCell colspan="5" class="text-muted-foreground">
                  No credential bindings are configured.
                </TableCell>
              </TableRow>
              <TableRow v-for="binding in state.bindings" :key="binding.service">
                <TableCell class="font-medium">{{ binding.service }}</TableCell>
                <TableCell>
                  <Badge :variant="statuses[bindingStatus(binding)].variant">
                    {{ statuses[bindingStatus(binding)].label }}
                  </Badge>
                </TableCell>
                <TableCell>{{ formatTimestamp(binding.expires_at) }}</TableCell>
                <TableCell class="text-muted-foreground">{{ scopes(binding) }}</TableCell>
                <TableCell class="text-muted-foreground">
                  {{ formatTimestamp(binding.updated_at) }}
                </TableCell>
              </TableRow>
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </template>
  </div>
</template>
