<script setup lang="ts">
import NewTaskButton from "@/tasks/NewTaskButton.vue";
import Brand from "./Brand.vue";
import ConnectionPill from "./ConnectionPill.vue";
import Nav from "./Nav.vue";
import ProfileDropdown from "./ProfileDropdown.vue";
import SearchBar from "./SearchBar.vue";
import SupportLink from "./SupportLink.vue";

const props = defineProps<{ hidden: string[] }>();
</script>

<template>
  <header
    class="bg-background/80 sticky top-0 z-40 flex h-14 items-center gap-3 border-b px-3 backdrop-blur-xl sm:px-4"
  >
    <Brand />
    <Nav :hidden="props.hidden" />
    <div class="flex shrink-0 items-center gap-2">
      <SearchBar :hidden="props.hidden" />
      <NewTaskButton v-if="!props.hidden.includes('/workflows')" />
      <ConnectionPill />
      <SupportLink />
      <ProfileDropdown />
    </div>
  </header>
</template>
