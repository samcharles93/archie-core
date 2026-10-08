<script setup lang="ts">
import PageHeader from "./PageHeader.vue";
</script>

<template>
  <div>
    <PageHeader title="Page not found" />
    <p class="mb-4 text-sm text-fg-muted">This address does not match a page.</p>
    <RouterLink to="/" class="text-sm underline">Return to dashboard</RouterLink>
  </div>
</template>
