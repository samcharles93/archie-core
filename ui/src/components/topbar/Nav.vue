<script setup lang="ts">
import { computed } from "vue";
import { useRoute } from "vue-router";

import { activeNavPath, navTree } from "@/lib/nav";
import NavGroup from "./NavGroup.vue";
import NavTrigger from "./NavTrigger.vue";

const props = defineProps<{ hidden: string[] }>();

const route = useRoute();
const tree = computed(() => navTree(props.hidden));
const activePath = computed(() => activeNavPath(route.path));
</script>

<template>
  <nav
    aria-label="Sections"
    class="scroll-fade-x flex min-w-0 flex-1 items-center gap-0.5 overflow-x-auto [scrollbar-width:none]"
  >
    <template
      v-for="node in tree"
      :key="node.kind === 'link' ? node.entry.path : node.label"
    >
      <NavTrigger
        v-if="node.kind === 'link'"
        :to="node.entry.path"
        :current="activePath === node.entry.path"
      >
        {{ node.entry.label }}
      </NavTrigger>
      <NavGroup
        v-else
        :label="node.label"
        :items="node.items"
        :active-path="activePath"
      />
    </template>
  </nav>
</template>
