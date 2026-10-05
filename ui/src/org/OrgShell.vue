<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useRoute } from "vue-router";

import { cn } from "@/lib/utils";
import { loadOrg, org } from "./org";

/**
 * The Org area's shared frame: the org it is about, and the tab strip that
 * moves between its sections. Each section is a route, so the strip is links
 * and the address bar names the open section.
 *
 * The org name and id are a context bar rather than the page's heading: each
 * section states its own, and /org/access is a moved page that still carries
 * its own.
 */
const TABS = [
  { path: "/org", label: "Members" },
  { path: "/org/agents", label: "Agents" },
  { path: "/org/workspaces", label: "Workspaces" },
  { path: "/org/access", label: "Access" },
  { path: "/org/tokens", label: "Tokens" },
];

const route = useRoute();
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
    <div class="mb-4 flex flex-wrap items-baseline gap-x-3 gap-y-1">
      <p class="text-lg font-semibold tracking-[-0.01em]">{{ org?.name ?? "Org" }}</p>
      <code v-if="org" class="text-xs text-fg-subtle">{{ org.id }}</code>
    </div>

    <nav aria-label="Org" class="mb-6 flex flex-wrap items-center gap-1 border-b border-border">
      <RouterLink
        v-for="tab in TABS"
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

    <!-- The sections need the org id, so they wait for the one read. -->
    <RouterView v-if="org" />
  </div>
</template>
