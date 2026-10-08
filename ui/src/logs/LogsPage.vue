<script setup lang="ts">
import LogsHeader from "./LogsHeader.vue";
import LogStream from "./LogStream.vue";
import { useLogs } from "./state";

useLogs();
</script>

<template>
  <LogsHeader />
  <LogStream />
</template>
