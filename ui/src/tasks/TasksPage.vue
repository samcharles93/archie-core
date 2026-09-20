<script lang="ts">
// The shell's global reduced-motion rule switches CSS transitions and
// animations, NOT the JS smooth scroll this page performs when it reveals a
// deep-linked row. The decision therefore has to be made here, in JavaScript,
// and is exported so it can be tested directly (jsdom implements neither
// matchMedia nor scrollIntoView).
export function prefersReducedMotion(): boolean {
  return typeof window !== "undefined" && window.matchMedia?.("(prefers-reduced-motion: reduce)")?.matches === true;
}

export function revealBehavior(): ScrollBehavior {
  return prefersReducedMotion() ? "auto" : "smooth";
}
</script>

<script setup lang="ts">
import { RefreshCw } from "@lucide/vue";
import { computed, nextTick, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { api } from "@/lib/api";
import TaskFilters, { initialTaskFilter, taskMatchesStatus } from "./TaskFilters.vue";
import type { Task } from "./TaskRow.vue";
import TaskTable from "./TaskTable.vue";
import TasksState from "./TasksState.vue";
import TasksSummary from "./TasksSummary.vue";

/**
 * The task board: every issue archied has picked up, and where it stands. It
 * owns the two things the query string names -- which status is filtered and
 * which task a link pointed at -- and composes the rest.
 */

const route = useRoute();
const router = useRouter();

// null means loading: an empty list and a list that has not arrived are
// different states, and only one of them is worth a spinner.
const tasks = ref<Task[] | null>(null);
const error = ref<string | null>(null);
const search = ref("");

const statusQuery = computed(() => (typeof route.query.status === "string" ? route.query.status : ""));
const requestedTaskId = computed(() => {
  const raw = route.query.task;
  const id = Number(typeof raw === "string" ? raw : NaN);
  return Number.isSafeInteger(id) && id > 0 ? id : null;
});

// The filter follows the URL as well as writing to it: a dashboard link or a
// back-button step has to move the control, not just load the page behind it.
const status = ref(initialTaskFilter(statusQuery.value));
watch(statusQuery, (value) => {
  status.value = initialTaskFilter(value);
});

const visible = computed(() =>
  (tasks.value ?? []).filter((task) => {
    if (!taskMatchesStatus(task, status.value)) return false;
    const needle = search.value.trim().toLowerCase();
    if (!needle) return true;
    return `${task.title ?? ""} ${task.repo ?? ""}`.toLowerCase().includes(needle);
  }),
);

// The repo column only earns its width when the rows differ. On a
// single-repository deployment it repeated one value down every row while the
// title was squeezed, so it goes when every visible row shares it.
const showRepo = computed(() => {
  const rows = visible.value;
  return rows.length < 2 || rows.some((task) => task.repo !== rows[0].repo);
});

const state = computed<"loading" | "error" | "empty" | "no-match">(() => {
  if (error.value) return "error";
  if (tasks.value === null) return "loading";
  if (!tasks.value.length) return "empty";
  return "no-match";
});

onMounted(() => {
  void load();
});

async function load() {
  error.value = null;
  try {
    tasks.value = await api.tasks<Task[]>();
  } catch (err) {
    error.value = String((err as Error).message || err);
    tasks.value = null;
  }
}

// A filter change is a query-only route change, so it must not remount the
// list: the table keeps its identity and only its rows move. An empty status is
// dropped from the query rather than written as `?status=`.
function setStatus(value: string) {
  status.value = initialTaskFilter(value);
  const query = { ...route.query };
  if (status.value) query.status = status.value;
  else delete query.status;
  void router.replace({ query });
}

function clearFilters() {
  search.value = "";
  setStatus("");
}

// A deep link (?task=<id>) reveals the row it names: focus it, so keyboard and
// screen-reader users land where the link pointed, then scroll it into view.
// Once per id -- a reload after a row action must not move the page under the
// operator.
const revealed = ref<number | null>(null);

watch([requestedTaskId, tasks], () => {
  const id = requestedTaskId.value;
  if (id === null || tasks.value === null || revealed.value === id) return;
  revealed.value = id;
  void reveal(id);
});

async function reveal(id: number) {
  // The row has to be in the DOM before it can be found or scrolled to.
  await nextTick();
  requestAnimationFrame(() => {
    const row = document.getElementById(`task-row-${id}`);
    if (!row) return;
    row.focus({ preventScroll: true });
    // Optional call: focusing a deep-linked row is a nicety, and not every
    // environment implements scrollIntoView on an element.
    row.scrollIntoView?.({ block: "center", behavior: revealBehavior() });
  });
}
</script>

<template>
  <div>
    <div class="mb-5 flex flex-wrap items-start justify-between gap-5">
      <div>
        <h1 class="text-3xl font-semibold tracking-[-0.03em]">Tasks</h1>
        <p class="text-fg-muted mt-2 text-sm">Every issue archied has picked up, and where it stands.</p>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <Button @click="load">
          <RefreshCw data-icon="inline-start" />
          Refresh
        </Button>
      </div>
    </div>

    <Card v-if="error">
      <CardContent>
        <TasksState kind="error" :detail="error" @retry="load" />
      </CardContent>
    </Card>

    <template v-else>
      <TasksSummary v-if="tasks" :tasks="tasks" />

      <Card>
        <CardHeader>
          <CardTitle>All tasks</CardTitle>
          <CardDescription>Click a row for its timeline</CardDescription>
        </CardHeader>
        <CardContent class="flex flex-col gap-3">
          <TaskFilters
            :status="status"
            :search="search"
            @update:status="setStatus"
            @update:search="search = $event"
          />
          <!--
            An action can change what a task is and what it still offers, so the
            list is re-read from the server rather than patched in place: the
            server owns both the status vocabulary and the controls a status
            allows.
          -->
          <TaskTable :tasks="visible" :show-repo="showRepo" @done="load">
            <template #state>
              <TasksState :kind="state" @retry="load" @clear="clearFilters" />
            </template>
          </TaskTable>
        </CardContent>
      </Card>
    </template>
  </div>
</template>
