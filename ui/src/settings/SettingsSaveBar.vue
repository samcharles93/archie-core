<script setup lang="ts">
import { computed, onUnmounted, ref } from "vue";
import { storeToRefs } from "pinia";

import { Button } from "@/components/ui/button";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { Spinner } from "@/components/ui/spinner";
import { StatusPill } from "@/components/ui/status-pill";
import { useControlPlaneStore } from "@/stores/control-plane";
import { formatValue } from "./changes";

const store = useControlPlaneStore();
const { reviewing } = storeToRefs(store);
onUnmounted(() => { reviewing.value = false; });
const saving = ref(false);

const titleOf = (kind: string) =>
  store.catalog.find((item) => item.kind === kind)?.title ?? kind;
const sections = computed(() =>
  store.dirtyKinds.map((kind) => ({
    kind,
    title: titleOf(kind),
    changes: store.changesFor(kind),
    issues: store.issuesFor(kind),
    error: store.stateFor(kind).error,
    restart:
      store.catalog.find((item) => item.kind === kind)?.apply_mode ===
      "restart-required",
  })),
);
const count = computed(() =>
  sections.value.reduce((n, s) => n + s.changes.length, 0),
);
// Invalid fields block Save, and are listed in the bar so the operator knows
// before opening the review. Both read the store's one draft diff.
const invalid = computed(() => store.invalidFields);
const needsRestart = computed(() => sections.value.some((s) => s.restart));

function discardAll() {
  for (const kind of store.dirtyKinds) store.resetDraft(kind);
  reviewing.value = false;
}

async function save() {
  if (invalid.value.length) return;
  saving.value = true;
  try {
    const outcome = await store.saveDrafts();
    if (!outcome.failed) reviewing.value = false;
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <div
    v-if="count > 0"
    class="sticky bottom-4 z-30 mt-auto rounded-lg border border-border bg-popover shadow-[var(--shadow-md)]"
    role="region"
    aria-label="Unsaved changes"
  >
    <div class="flex items-center gap-3 px-4 py-3">
      <span
        class="size-2 shrink-0 rounded-full"
        :class="invalid.length ? 'bg-danger' : 'bg-primary'"
        aria-hidden="true"
      />
      <p class="min-w-0 flex-1 truncate text-sm">
        <span class="font-medium">{{ count }} unsaved {{ count === 1 ? "change" : "changes" }}</span>
        <span class="text-fg-subtle"> · {{ sections.map((s) => s.title).join(", ") }}</span>
      </p>
      <Button variant="ghost" size="sm" @click="discardAll">Discard</Button>
      <Button size="sm" @click="reviewing = true">Review and save</Button>
    </div>
    <ul
      v-if="invalid.length"
      class="space-y-0.5 border-t border-danger/30 px-4 py-2 text-xs text-danger"
      role="alert"
    >
      <li v-for="issue in invalid" :key="`${issue.kind}.${issue.path}`">
        <span class="font-medium">{{ titleOf(issue.kind) }} · {{ issue.label }}</span>:
        {{ issue.message }}
      </li>
    </ul>
  </div>

  <Sheet v-model:open="reviewing">
    <SheetContent class="flex w-full flex-col gap-0 sm:max-w-[480px]">
      <SheetHeader>
        <SheetTitle>Review {{ count }} {{ count === 1 ? "change" : "changes" }}</SheetTitle>
        <SheetDescription>
          {{ sections.length }} {{ sections.length === 1 ? "section" : "sections" }}
        </SheetDescription>
      </SheetHeader>
      <div class="flex-1 space-y-3 overflow-y-auto px-4 pb-4">
        <p
          v-if="needsRestart"
          class="rounded-md border border-warn/40 bg-warn-soft px-3 py-2 text-sm text-warn"
        >
          Some changes apply after restart.
        </p>
        <p
          v-if="invalid.length"
          class="rounded-md border border-danger/40 bg-danger-soft px-3 py-2 text-sm text-danger"
          role="alert"
        >
          {{ invalid.length }} invalid {{ invalid.length === 1 ? "field" : "fields" }} must be fixed before saving.
        </p>
        <section
          v-for="section in sections"
          :key="section.kind"
          class="rounded-lg border border-border bg-card"
        >
          <header class="flex items-center gap-2 border-b border-border px-3 py-2">
            <h3 class="flex-1 truncate text-sm font-medium">{{ section.title }}</h3>
            <StatusPill v-if="section.restart" tone="warn">After restart</StatusPill>
            <Button variant="ghost" size="sm" @click="store.resetDraft(section.kind)">
              Revert
            </Button>
          </header>
          <ul class="divide-y divide-border font-mono text-xs">
            <li v-for="change in section.changes" :key="change.path" class="px-3 py-2">
              <p class="mb-1 text-fg-subtle">{{ change.path }}</p>
              <p class="truncate text-danger">− {{ formatValue(change.before) }}</p>
              <p class="truncate text-ok">+ {{ formatValue(change.after) }}</p>
            </li>
          </ul>
          <ul
            v-if="section.issues.length"
            class="space-y-0.5 border-t border-border px-3 py-2 text-xs text-danger"
            role="alert"
          >
            <li v-for="issue in section.issues" :key="issue.path">
              <span class="font-medium">{{ issue.label }}</span>: {{ issue.message }}
            </li>
          </ul>
          <p v-if="section.error" class="border-t border-border px-3 py-2 text-sm text-danger" role="alert">
            {{ section.error }}
          </p>
        </section>
      </div>
      <SheetFooter class="flex-row justify-end gap-2 border-t border-border">
        <Button variant="ghost" @click="discardAll">Discard all</Button>
        <Button :disabled="saving || invalid.length > 0" @click="save">
          <Spinner v-if="saving" data-icon="inline-start" /> Save
        </Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
