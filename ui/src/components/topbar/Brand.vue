<script setup lang="ts">
import Logo from "./Logo.vue";
import BrandName from "./BrandName.vue";
</script>

<template>
  <RouterLink
    to="/"
    class="flex items-center gap-2 rounded-md px-1 py-1 outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50"
  >
    <Logo />
    <BrandName class="hidden sm:inline" />
  </RouterLink>
</template>
