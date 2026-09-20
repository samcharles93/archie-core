<script setup lang="ts">
import { useRoute } from "vue-router";

const route = useRoute();
</script>

<template>
  <section class="p-8">
    <h1 class="text-2xl font-semibold tracking-tight">{{ route.path }}</h1>
    <p class="text-muted-foreground mt-2 text-sm">Not yet ported to Vue.</p>
  </section>
</template>
