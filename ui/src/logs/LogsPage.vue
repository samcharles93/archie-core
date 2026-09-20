<script setup lang="ts">
import LogsHeader from "./LogsHeader.vue";
import LogStream from "./LogStream.vue";
import { useLogs } from "./state";

/**
 * The daemon log: the history /api/logs serves with the live stream tailed
 * onto it. Composes the page; it holds no state of its own.
 */
useLogs();
</script>

<template>
  <LogsHeader />
  <LogStream />
</template>
