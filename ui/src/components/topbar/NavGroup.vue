<script setup lang="ts">
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

const props = defineProps<{ label: string; items: NavEntry[]; activePath: string }>();

const holds = (path: string) => props.activePath === path;
</script>

<template>
  <DropdownMenu>
    <DropdownMenuTrigger as-child>
      <NavTrigger :current="items.some((item) => holds(item.path))">
        {{ label }}
        <ChevronDown :size="14" class="opacity-60" />
      </NavTrigger>
    </DropdownMenuTrigger>
    <DropdownMenuContent align="start" class="w-52">
      <template v-for="item in items" :key="item.path">
        <DropdownMenuSeparator v-if="item.dividerBefore" />
        <DropdownMenuLabel v-if="item.dividerBefore">{{ item.dividerBefore }}</DropdownMenuLabel>
        <NavItem :entry="item" :current="holds(item.path)" />
      </template>
    </DropdownMenuContent>
  </DropdownMenu>
</template>
