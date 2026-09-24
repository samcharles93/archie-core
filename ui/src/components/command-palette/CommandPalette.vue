<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import { useRouter } from "vue-router";
import { Search } from "@lucide/vue";

import { navEntries, type NavEntry } from "@/lib/nav";

/**
 * The command palette: every section the nav can reach, opened with Cmd/Ctrl+K
 * or '/' from anywhere. It is a jump list, not a data search -- the topbar's
 * SearchBar owns the same list with prefix matching from a visible field; this
 * one adds substring matching and arrow-key selection from a shortcut.
 *
 * Styling and keyboard handling mirror ChatSlashPalette, the in-app palette
 * precedent: a bordered popover surface, mousedown prevented on the options so
 * choosing with the pointer never steals focus from the filter, and the list
 * scrolled so the highlighted row stays visible. Nothing here animates, so the
 * shell's prefers-reduced-motion rule has nothing to quiet.
 */
const props = defineProps<{ hidden: string[] }>();

const router = useRouter();

const open = ref(false);
const query = ref("");
const selection = ref(0);
const field = ref<HTMLInputElement | null>(null);
const list = ref<HTMLElement | null>(null);

// The same list the topbar's SearchBar and Nav read: navEntries flattens the
// nav tree and drops what this composition cannot back, so the palette can
// never offer a destination the server cannot serve.
const allEntries = computed(() =>
  navEntries(props.hidden).filter((entry) => !entry.soon),
);

// Substring, not prefix: an operator who remembers "where do channels live"
// types a word from the description, not the start of the label.
const matches = computed(() => {
  const needle = query.value.trim().toLowerCase();
  if (!needle) return allEntries.value;
  return allEntries.value.filter((entry) =>
    `${entry.label} ${entry.description} ${entry.path}`
      .toLowerCase()
      .includes(needle),
  );
});
watch(query, () => {
  selection.value = 0;
});
// Capabilities can land while the palette is open and shrink the list; the
// highlight must not point past the last row.
watch(matches, () => {
  if (selection.value >= matches.value.length) selection.value = 0;
});

function optionId(index: number): string {
  return `command-palette-option-${index}`;
}

async function show(): Promise<void> {
  query.value = "";
  selection.value = 0;
  open.value = true;
  await nextTick();
  field.value?.focus();
}

function close(): void {
  open.value = false;
  field.value?.blur();
}

function toggle(): void {
  if (open.value) close();
  else void show();
}

// '/' opens only when the keystroke was meant for a field, not for the page:
// typing a slash into the composer or the topbar search must stay typing.
function isEditableTarget(event: KeyboardEvent): boolean {
  const el = event.target;
  return (
    el instanceof HTMLElement &&
    (el.isContentEditable ||
      el.tagName === "INPUT" ||
      el.tagName === "TEXTAREA" ||
      el.tagName === "SELECT")
  );
}

// Registered on document in the bubble phase (capture: false): the palette is
// the last thing that should see a keystroke, after any focused field has had
// its turn.
function onGlobalKeydown(event: KeyboardEvent): void {
  if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
    event.preventDefault();
    toggle();
    return;
  }
  if (event.key === "/" && !open.value && !isEditableTarget(event)) {
    event.preventDefault();
    void show();
  }
}

onMounted(() => document.addEventListener("keydown", onGlobalKeydown));
onUnmounted(() => document.removeEventListener("keydown", onGlobalKeydown));

function onKeydown(event: KeyboardEvent): void {
  if (event.key === "Escape") {
    // Consumed here so closing the palette is not also read as a dismissal of
    // whatever is open behind it (ChatLauncher watches defaultPrevented).
    event.preventDefault();
    close();
    return;
  }
  if (event.key === "ArrowDown") {
    event.preventDefault();
    if (matches.value.length)
      selection.value = Math.min(selection.value + 1, matches.value.length - 1);
    return;
  }
  if (event.key === "ArrowUp") {
    event.preventDefault();
    selection.value = Math.max(selection.value - 1, 0);
    return;
  }
  if (event.key === "Enter") {
    event.preventDefault();
    const entry = matches.value[selection.value];
    if (entry) choose(entry);
  }
}

function choose(entry: NavEntry): void {
  void router.push(entry.path);
  close();
}

// Keep the highlighted row visible once matches run past the rows that fit.
// block: 'nearest' scrolls the minimum distance and never animates, so reduced
// motion needs no handling here. Optional call: not every environment
// implements scrollIntoView.
watch([selection, matches], async () => {
  if (!open.value) return;
  await nextTick();
  list.value
    ?.querySelector('[aria-selected="true"]')
    ?.scrollIntoView?.({ block: "nearest" });
});
</script>

<template>
  <div v-if="open" class="fixed inset-0 isolate z-50" @mousedown.self="close">
    <div
      class="bg-black/10 supports-backdrop-filter:backdrop-blur-xs absolute inset-0"
      aria-hidden="true"
    />
    <div
      role="dialog"
      aria-label="Command palette"
      class="bg-popover text-popover-foreground border-border-strong shadow-md mx-auto mt-[15vh] w-[calc(100%-2rem)] max-w-lg overflow-hidden rounded-xl border"
    >
      <div class="border-border flex items-center gap-2 border-b px-3">
        <Search :size="15" class="text-muted-foreground shrink-0" />
        <input
          ref="field"
          v-model="query"
          type="text"
          role="combobox"
          aria-label="Filter sections"
          aria-expanded="true"
          aria-controls="command-palette-list"
          :aria-activedescendant="
            matches[selection] ? optionId(selection) : undefined
          "
          class="h-11 w-full bg-transparent text-sm outline-none placeholder:text-muted-foreground"
          placeholder="Jump to a section…"
          @keydown="onKeydown"
        />
      </div>
      <div
        id="command-palette-list"
        ref="list"
        role="listbox"
        aria-label="Sections"
        class="max-h-72 overflow-auto p-1.5"
      >
        <button
          v-for="(entry, index) in matches"
          :id="optionId(index)"
          :key="entry.path"
          type="button"
          role="option"
          :aria-selected="index === selection"
          class="hover:bg-muted aria-selected:bg-muted flex flex-col gap-0.5 rounded-md px-2.5 py-2 text-left outline-none"
          @mousedown.prevent
          @mousemove="selection = index"
          @click="choose(entry)"
        >
          <span class="truncate text-sm text-foreground">{{
            entry.label
          }}</span>
          <span
            v-if="entry.description"
            class="text-fg-muted truncate text-xs"
            >{{ entry.description }}</span
          >
        </button>
        <p v-if="!matches.length" class="text-fg-muted px-2.5 py-3 text-sm">
          No matching section.
        </p>
      </div>
    </div>
  </div>
</template>
