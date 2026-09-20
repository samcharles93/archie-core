<script setup lang="ts">
import { computed, nextTick, ref } from "vue";
import { useRouter } from "vue-router";
import { Search } from "@lucide/vue";

import { Input } from "@/components/ui/input";
import { navEntries } from "@/lib/nav";

const props = defineProps<{ hidden: string[] }>();

const router = useRouter();
const field = ref<{ $el: HTMLInputElement } | null>(null);
const query = ref("");
const open = ref(false);

const entries = computed(() => navEntries(props.hidden).filter((entry) => !entry.soon));

// Jumping between sections, deliberately not a data search: each section owns
// its own filtering, and one box that means something different on every page
// is worse than none.
const hit = computed(() => {
  const wanted = query.value.trim().toLowerCase();
  if (!wanted) return undefined;
  return entries.value.find((entry) => entry.label.toLowerCase().startsWith(wanted));
});

async function toggle() {
  open.value = !open.value;
  if (!open.value) return;
  await nextTick();
  field.value?.$el.focus();
}

function onKeydown(event: KeyboardEvent) {
  const input = event.target as HTMLInputElement;
  if (event.key === "Escape") {
    // Consumed here so a dismissal of the field is not also read as a
    // dismissal of whatever is open behind it.
    event.preventDefault();
    query.value = "";
    open.value = false;
    input.blur();
    return;
  }
  if (event.key !== "Enter" || !hit.value) return;
  void router.push(hit.value.path);
  query.value = "";
  open.value = false;
  input.blur();
}
</script>

<template>
  <div class="flex items-center">
    <button
      type="button"
      class="text-muted-foreground hover:text-foreground hover:bg-accent focus-visible:ring-ring/50 inline-flex size-8 items-center justify-center rounded-md outline-none focus-visible:ring-[3px] lg:hidden"
      aria-label="Jump to a section"
      :aria-expanded="open"
      @click="toggle()"
    >
      <Search :size="16" />
    </button>
    <div class="relative" :class="open ? 'block' : 'hidden lg:block'">
      <Search :size="15" class="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2" />
      <Input
        ref="field"
        v-model="query"
        type="search"
        class="h-8 w-44 pl-8 text-sm"
        placeholder="Jump to…"
        aria-label="Jump to a section"
        @keydown="onKeydown"
      />
    </div>
  </div>
</template>
