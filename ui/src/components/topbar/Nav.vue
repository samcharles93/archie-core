<script setup lang="ts">
import { computed } from "vue";
import { useRoute } from "vue-router";

import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { activeNavPath, navTree } from "@/lib/nav";
import NavGroup from "./NavGroup.vue";
import NavTrigger from "./NavTrigger.vue";

const props = defineProps<{ hidden: string[] }>();

const route = useRoute();
const tree = computed(() => navTree(props.hidden));
const activePath = computed(() => activeNavPath(route.path));
</script>

<template>
  <nav aria-label="Sections" class="flex min-w-0 flex-1 items-center gap-0.5 overflow-x-auto">
    <template v-for="node in tree" :key="node.kind === 'link' ? node.entry.path : node.label">
      <Tooltip v-if="node.kind === 'link'">
        <TooltipTrigger as-child>
          <NavTrigger :to="node.entry.path" :current="activePath === node.entry.path">
            {{ node.entry.label }}
          </NavTrigger>
        </TooltipTrigger>
        <TooltipContent :side-offset="8" class="max-w-64">{{ node.entry.description }}</TooltipContent>
      </Tooltip>
      <NavGroup v-else :label="node.label" :items="node.items" :active-path="activePath" />
    </template>
  </nav>
</template>
