<script setup lang="ts">
import { computed } from "vue";
import { GitBranch, Split } from "@lucide/vue";

import {
  DropdownMenuItem,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
} from "@/components/ui/dropdown-menu";
import { stepGroups } from "./step-icons";
import { stepTitle } from "./workflow-graph";

const props = defineProps<{ types: string[] }>();
const emit = defineEmits<{ pick: [string] }>();

const groups = computed(() => stepGroups(props.types));
</script>

<template>
  <DropdownMenuItem v-if="types.includes('parallel')" @select="emit('pick', 'parallel')"><GitBranch />Parallel</DropdownMenuItem>
  <DropdownMenuItem v-if="types.includes('switch')" @select="emit('pick', 'switch')"><Split />Switch</DropdownMenuItem>
  <template v-for="group in groups" :key="group.title">
    <!-- A group of one is its step: no submenu to open for a single choice. -->
    <DropdownMenuItem v-if="group.types.length === 1" @select="emit('pick', group.types[0])">
      <component :is="group.icon" />
      {{ stepTitle(group.types[0]) }}
    </DropdownMenuItem>
    <DropdownMenuSub v-else>
      <DropdownMenuSubTrigger>
        <component :is="group.icon" />
        {{ group.title }}
      </DropdownMenuSubTrigger>
      <DropdownMenuSubContent class="w-52">
        <DropdownMenuItem v-for="type in group.types" :key="type" @select="emit('pick', type)">
          {{ stepTitle(type) }}
        </DropdownMenuItem>
      </DropdownMenuSubContent>
    </DropdownMenuSub>
  </template>
</template>
