<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted } from "vue";
import { useRoute, useRouter } from "vue-router";

import { hidden } from "@/lib/capabilities";
import { settingsNav } from "@/lib/nav";
import { cn } from "@/lib/utils";
import { useControlPlaneStore } from "@/stores/control-plane";
import RestartPendingBanner from "./RestartPendingBanner.vue";
import SettingsSaveBar from "./SettingsSaveBar.vue";

const route = useRoute();
const items = computed(() => settingsNav(hidden.value));

// Drafts live in the store, so moving between Settings pages keeps them;
// leaving Settings, or the page, with edits unsaved asks first.
const store = useControlPlaneStore();
const dirty = () => store.dirtyKinds.length > 0;
const removeGuard = useRouter().beforeEach((to) => {
  if (to.meta.settings || !dirty()) return true;
  return window.confirm("Leave Settings with unsaved changes?");
});
const warnUnload = (event: BeforeUnloadEvent) => {
  if (dirty()) event.preventDefault();
};
onMounted(() => window.addEventListener("beforeunload", warnUnload));
onBeforeUnmount(() => {
  removeGuard();
  window.removeEventListener("beforeunload", warnUnload);
});
</script>

<template>
  <div class="flex gap-10 max-lg:flex-col max-lg:gap-4">
    <nav
      aria-label="Settings"
      class="w-60 shrink-0 max-lg:-mx-4 max-lg:w-auto max-lg:scroll-fade-x max-lg:overflow-x-auto max-lg:[scrollbar-width:none] max-lg:border-b max-lg:px-4"
    >
      <ul class="flex flex-col gap-0.5 lg:sticky lg:top-6 max-lg:flex-row max-lg:gap-1">
        <li v-for="(item, index) in items" :key="item.path" class="contents">
          <p
            v-if="item.dividerBefore"
            :class="cn('mb-1.5 px-3 text-[11px] font-medium tracking-[0.06em] text-fg-subtle uppercase max-lg:hidden', index > 0 && 'mt-5')"
          >
            {{ item.dividerBefore }}
          </p>
          <RouterLink
            :to="item.path"
            :aria-current="route.path === item.path ? 'page' : undefined"
            :class="
              cn(
                'flex h-9 items-center rounded-md px-3 text-[13px] whitespace-nowrap text-muted-foreground transition-colors hover:bg-secondary hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring',
                route.path === item.path &&
                  'bg-secondary font-medium text-foreground',
              )
            "
          >
            {{ item.label }}
          </RouterLink>
        </li>
      </ul>
    </nav>
    <div class="min-w-0 max-w-[960px] flex-1">
      <RestartPendingBanner />
      <!-- Room below the last setting, so it can be scrolled up to eye level. -->
      <div class="pb-[40vh]">
        <slot />
      </div>
      <SettingsSaveBar />
    </div>
  </div>
</template>
