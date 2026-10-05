<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";

import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";
import NewOrgDialog from "./NewOrgDialog.vue";
import { homeOrg, instanceAdmin, loadOrg, org, orgs, viewOrg } from "./org";

/**
 * The Org area's shared frame: the org it is about, and the tab strip that
 * moves between its sections. Each section is a route, so the strip is links
 * and the address bar names the open section.
 *
 * The org name and id are a context bar rather than the page's heading: each
 * section states its own, and /org/access is a moved page that still carries
 * its own. An instance admin gets a switcher over every org in its place.
 */
const TABS = [
  { path: "/org", label: "Members" },
  { path: "/org/agents", label: "Agents" },
  { path: "/org/workspaces", label: "Workspaces" },
  { path: "/org/access", label: "Access", home: true },
  { path: "/org/tokens", label: "Tokens", home: true },
];

const route = useRoute();
const router = useRouter();

// Access and Tokens act on the caller's own org server-side, so they are
// hidden while an instance admin views another org rather than editing the
// wrong one.
const away = computed(() => !!org.value && org.value.id !== homeOrg.value);
const tabs = computed(() => TABS.filter((tab) => !(away.value && tab.home)));
watch(away, (isAway) => {
  if (isAway && TABS.some((tab) => tab.home && route.path.startsWith(tab.path))) {
    void router.replace("/org");
  }
});
const error = ref("");

onMounted(async () => {
  try {
    await loadOrg();
  } catch (cause) {
    error.value = String((cause as Error).message || cause);
  }
});

const currentPath = computed(() => route.path);
const current = (path: string) =>
  path === "/org" ? currentPath.value === "/org" : currentPath.value.startsWith(path);
</script>

<template>
  <div>
    <div v-if="instanceAdmin" class="mb-4 flex flex-wrap items-center gap-2">
      <Select :model-value="org?.id" @update:model-value="(id) => viewOrg(String(id))">
        <SelectTrigger class="w-72" aria-label="Org">
          <SelectValue placeholder="Org" />
        </SelectTrigger>
        <SelectContent>
          <SelectItem v-for="o in orgs" :key="o.id" :value="o.id">
            {{ o.name }}
          </SelectItem>
        </SelectContent>
      </Select>
      <code v-if="org" class="text-xs text-fg-subtle">{{ org.id }}</code>
      <NewOrgDialog />
    </div>
    <div v-else class="mb-4 flex flex-wrap items-baseline gap-x-3 gap-y-1">
      <p class="text-lg font-semibold tracking-[-0.01em]">{{ org?.name ?? "Org" }}</p>
      <code v-if="org" class="text-xs text-fg-subtle">{{ org.id }}</code>
    </div>

    <nav aria-label="Org" class="mb-6 flex flex-wrap items-center gap-1 border-b border-border">
      <RouterLink
        v-for="tab in tabs"
        :key="tab.path"
        :to="tab.path"
        :aria-current="current(tab.path) ? 'page' : undefined"
        :class="
          cn(
            '-mb-px border-b-2 border-transparent px-3 py-2 text-sm text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring',
            current(tab.path) && 'border-primary font-medium text-foreground',
          )
        "
      >
        {{ tab.label }}
      </RouterLink>
    </nav>

    <p v-if="error" role="alert" class="mb-4 text-sm text-danger">{{ error }}</p>

    <!-- The sections need the org id, so they wait for the one read; keying on
         it remounts the open section when an instance admin switches org. -->
    <RouterView v-if="org" :key="org.id" />
  </div>
</template>
