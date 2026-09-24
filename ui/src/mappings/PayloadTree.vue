<script setup lang="ts">
import { computed } from "vue";

import {
  fieldTypeFromValue,
  pathAppendIndex,
  pathAppendKey,
  type FieldPick,
} from "./mapping-fields";

/**
 * One level of a captured payload, with every key and every leaf clickable, so
 * a field is bound by clicking through the real payload rather than by typing a
 * JSON path -- "click through its payload, bind JSON paths" per
 * docs/prds/payload-field-mapping.md. Recurses into itself for child objects
 * and arrays, so depth costs nothing to support.
 *
 * A key is clickable in its own right: binding `data` whole is a legitimate
 * choice, and the type is inferred from whatever that node happens to hold.
 */
const props = withDefaults(
  defineProps<{ value: unknown; path?: string; name?: string }>(),
  { path: "", name: "" },
);
const emit = defineEmits<{ pick: [pick: FieldPick] }>();

const isBranch = computed(
  () => props.value !== null && typeof props.value === "object",
);

/** This level's children, each with the path that addresses it. */
const entries = computed<Array<{ key: string; value: unknown; path: string }>>(
  () => {
    if (!isBranch.value) return [];
    if (Array.isArray(props.value)) {
      return props.value.map((child, index) => ({
        key: String(index),
        value: child,
        path: pathAppendIndex(props.path, index),
      }));
    }
    return Object.entries(props.value as Record<string, unknown>).map(
      ([key, child]) => ({
        key,
        value: child,
        path: pathAppendKey(props.path, key),
      }),
    );
  },
);

function pick(path: string, key: string, value: unknown): void {
  emit("pick", { path, type: fieldTypeFromValue(value), name: key });
}
</script>

<template>
  <ul
    v-if="isBranch"
    class="flex list-none flex-col gap-0.5 pl-4 font-mono text-xs"
  >
    <li v-for="entry in entries" :key="entry.path">
      <button
        type="button"
        class="focus-visible:ring-ring rounded-sm px-1 text-left outline-none hover:bg-muted focus-visible:ring-2"
        @click="pick(entry.path, entry.key, entry.value)"
      >
        {{ entry.key }}
      </button>
      <PayloadTree
        :value="entry.value"
        :path="entry.path"
        :name="entry.key"
        @pick="emit('pick', $event)"
      />
    </li>
  </ul>

  <button
    v-else
    type="button"
    class="focus-visible:ring-ring ml-1 rounded-sm px-1 text-fg-subtle outline-none hover:bg-muted focus-visible:ring-2"
    @click="pick(path, name, value)"
  >
    {{ JSON.stringify(value) }}
  </button>
</template>
