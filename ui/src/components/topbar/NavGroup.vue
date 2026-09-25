<script setup lang="ts">
import { ref } from "vue";
import { ChevronDown } from "@lucide/vue";

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import type { NavEntry } from "@/lib/nav";
import NavItem from "./NavItem.vue";
import NavTrigger from "./NavTrigger.vue";

const props = defineProps<{
  label: string;
  items: NavEntry[];
  activePath: string;
}>();

const holds = (path: string) => props.activePath === path;

/**
 * Hover-open, click-toggling menu.
 *
 * Pointing at the group opens it; leaving it closes it after a short grace
 * period, so the move from trigger to content cannot close what you are
 * moving towards (the content carries its own enter/leave handlers, and the
 * delay covers the gap between two teleported elements). The trigger keeps
 * its click and keyboard semantics: reka still emits update:open on
 * activation, this only controls how long the menu stays open.
 */
const open = ref(false);
let closeTimer: ReturnType<typeof setTimeout> | undefined;

function openMenu() {
  clearTimeout(closeTimer);
  open.value = true;
}

function closeMenuSoon() {
  clearTimeout(closeTimer);
  closeTimer = setTimeout(() => {
    open.value = false;
  }, 140);
}
</script>

<template>
  <DropdownMenu :open="open" @update:open="open = $event">
    <div class="flex" @pointerenter="openMenu" @pointerleave="closeMenuSoon">
      <DropdownMenuTrigger as-child>
        <NavTrigger :current="items.some((item) => holds(item.path))">
          {{ props.label }}
          <ChevronDown :size="14" class="opacity-60" />
        </NavTrigger>
      </DropdownMenuTrigger>
    </div>
    <DropdownMenuContent
      align="start"
      class="w-52"
      @pointerenter="openMenu"
      @pointerleave="closeMenuSoon"
    >
      <template v-for="(item, index) in items" :key="item.path">
        <DropdownMenuSeparator v-if="item.dividerBefore && index > 0" />
        <DropdownMenuLabel v-if="item.dividerBefore">{{
          item.dividerBefore
        }}</DropdownMenuLabel>
        <NavItem :entry="item" :current="holds(item.path)" />
      </template>
    </DropdownMenuContent>
  </DropdownMenu>
</template>
