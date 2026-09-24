<script setup lang="ts">
import {
  fmtValue,
  LEVEL_COLOR,
  levelKind,
  shortTime,
  type LogEntry,
} from "@/lib/log";

/**
 * Shared log-entry rendering. Both the daemon-wide log page and a task's own
 * attempt log render the same entry shape, so the row lives here rather than
 * being duplicated a second time.
 */
const props = defineProps<{ entry: LogEntry }>();

const fields = () => Object.entries(props.entry.fields ?? {});
</script>

<template>
  <div
    class="grid grid-cols-[62px_52px_1fr] items-baseline gap-3 border-b border-border px-1 py-2 hover:bg-accent"
  >
    <span class="font-mono text-fg-subtle">{{
      shortTime(props.entry.time)
    }}</span>
    <span
      class="text-[10px] font-semibold tracking-[0.04em]"
      :class="LEVEL_COLOR[levelKind(props.entry.level)]"
    >
      {{ (props.entry.level || "info").toUpperCase() }}
    </span>
    <span class="flex min-w-0 flex-wrap items-baseline gap-2">
      <span class="break-words text-foreground">{{
        props.entry.message || props.entry.msg || ""
      }}</span>
      <span
        v-for="[k, v] in fields()"
        :key="k"
        class="max-w-full truncate rounded-sm bg-muted px-2 text-fg-muted"
      >
        <span class="text-fg-subtle after:opacity-60 after:content-['=']">{{
          k
        }}</span
        >{{ fmtValue(v) }}
      </span>
    </span>
  </div>
</template>
