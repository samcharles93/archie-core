<script setup lang="ts">
import Brand from "./Brand.vue";
import Nav from "./Nav.vue";
import ProfileDropdown from "./ProfileDropdown.vue";
import SearchBar from "./SearchBar.vue";
import SupportLink from "./SupportLink.vue";

defineProps<{ hidden: string[] }>();
</script>

<template>
  <header
    class="bg-background/80 sticky top-0 z-40 flex h-14 items-center gap-3 border-b px-3 backdrop-blur-xl sm:px-4"
  >
    <Brand />
    <Nav :hidden="hidden" />
    <div class="flex shrink-0 items-center gap-1">
      <SearchBar :hidden="hidden" />
      <SupportLink />
      <!--
        The theme control lives on /system/appearance, not here. Two switches
        for one preference is two places to look and one of them to be wrong
        (docs/prds/dashboard-navigation-groups.md).
      -->
      <ProfileDropdown />
    </div>
  </header>
</template>
