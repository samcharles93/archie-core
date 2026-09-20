<script setup lang="ts">
import { useRoute } from "vue-router";

// Every route resolves here until its view is ported. Named so an unported
// page is obvious in the running app rather than rendering blank -- the
// failure mode the Preact /chat route had.
const route = useRoute();
</script>

<template>
  <section class="p-8">
    <h1 class="text-2xl font-semibold tracking-tight">{{ route.path }}</h1>
    <p class="text-muted-foreground mt-2 text-sm">Not yet ported to Vue.</p>
  </section>
</template>
