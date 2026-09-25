<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";

import { hidden } from "@/lib/capabilities";
import { settingsNav } from "@/lib/nav";
import { cn } from "@/lib/utils";
import { useControlPlaneStore } from "@/stores/control-plane";
import RestartPendingBanner from "./RestartPendingBanner.vue";
import SettingsSaveBar from "./SettingsSaveBar.vue";

const route = useRoute();
const items = computed(() => settingsNav(hidden.value));

// The sidebar's width, dragged or keyed on its edge, kept per browser.
const MIN_WIDTH = 180;
const MAX_WIDTH = 420;
const DEFAULT_WIDTH = 240;
const WIDTH_KEY = "archie.settingsSidebarWidth";
const clampWidth = (w: number) => Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, Math.round(w)));
const width = ref(clampWidth(Number(localStorage.getItem(WIDTH_KEY)) || DEFAULT_WIDTH));
watch(width, (w) => localStorage.setItem(WIDTH_KEY, String(w)));

function startResize(event: PointerEvent) {
  event.preventDefault();
  const handle = event.currentTarget as HTMLElement;
  const startX = event.clientX;
  const startWidth = width.value;
  handle.setPointerCapture(event.pointerId);
  const move = (e: PointerEvent) => (width.value = clampWidth(startWidth + e.clientX - startX));
  const stop = () => {
    handle.removeEventListener("pointermove", move);
    handle.removeEventListener("pointerup", stop);
    handle.removeEventListener("pointercancel", stop);
  };
  handle.addEventListener("pointermove", move);
  handle.addEventListener("pointerup", stop);
  handle.addEventListener("pointercancel", stop);
}

function keyResize(event: KeyboardEvent) {
  const step = event.shiftKey ? 48 : 16;
  const next: Record<string, number> = {
    ArrowLeft: width.value - step,
    ArrowRight: width.value + step,
    Home: MIN_WIDTH,
    End: MAX_WIDTH,
  };
  if (!(event.key in next)) return;
  event.preventDefault();
  width.value = clampWidth(next[event.key]!);
}

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
  <div class="flex max-lg:flex-col max-lg:gap-4" :style="{ '--sidebar-w': `${width}px` }">
    <nav
      id="settings-sidebar"
      aria-label="Settings"
      class="shrink-0 lg:w-[var(--sidebar-w)] max-lg:-mx-4 max-lg:w-auto max-lg:scroll-fade-x max-lg:overflow-x-auto max-lg:[scrollbar-width:none] max-lg:border-b max-lg:px-4"
    >
      <ul class="flex flex-col gap-0.5 lg:sticky lg:top-20 max-lg:flex-row max-lg:gap-1">
        <li v-for="(item, index) in items" :key="item.path" class="contents">
          <hr v-if="item.dividerBefore === ''" class="my-4 border-border max-lg:hidden" />
          <p
            v-else-if="item.dividerBefore"
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
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label="Resize settings sidebar"
      aria-controls="settings-sidebar"
      :aria-valuenow="width"
      :aria-valuemin="MIN_WIDTH"
      :aria-valuemax="MAX_WIDTH"
      tabindex="0"
      class="group relative w-10 shrink-0 cursor-col-resize touch-none outline-none max-lg:hidden"
      @pointerdown="startResize"
      @keydown="keyResize"
      @dblclick="width = DEFAULT_WIDTH"
    >
      <span
        class="absolute inset-y-0 left-1/2 w-px -translate-x-1/2 bg-transparent transition-colors group-hover:bg-border-strong group-focus-visible:bg-primary group-active:bg-primary"
        aria-hidden="true"
      />
    </div>
    <div class="min-w-0 flex-1" :class="route.meta.wide ? '' : 'max-w-[960px]'">
      <RestartPendingBanner />
      <!-- Room below the last setting, so it can be scrolled up to eye level. -->
      <div class="pb-[40vh]">
        <slot />
      </div>
      <SettingsSaveBar />
    </div>
  </div>
</template>
