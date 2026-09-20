/**
 * One task's run: the reads, the caches and the view state the run page and its
 * panels share.
 *
 * The page provides it and the panels inject it, rather than the page threading
 * a dozen props through a host component: the attempt a panel reads and the
 * attempt the selector shows come from the same place, so they cannot disagree.
 *
 * Every keyed fetch goes through keyedLoad: the key is what the response
 * belongs to, so a late response for an attempt the operator has already left
 * lands under its own key and is never shown against another attempt.
 */
import { computed, inject, onMounted, provide, reactive, ref, watch, type ComputedRef, type InjectionKey, type Ref } from "vue";
import { useRoute, useRouter } from "vue-router";

import { api, classifyActionError, type ActionErrorKind } from "@/lib/api";
import { actionFor } from "@/lib/task-meta";

import {
  attemptKey,
  EMPTY_LOG_FILTERS,
  initialAttempt,
  initialTab,
  logCacheKey,
  runQuery,
  type AttemptsState,
  type ChangesState,
  type DebugState,
  type LogFilters,
  type LogState,
  type TaskEvent,
  type TaskRecord,
} from "./task-run";

/** A keyed read has three states: pending, failed, or the response. */
type Cache<T> = Map<string, T | null | undefined>;

export interface RetryError {
  kind: ActionErrorKind;
  message: string;
}

export interface TaskRun {
  tab: string;
  attempts: AttemptsState | null | undefined;
  taskList: TaskRecord[] | null | undefined;
  events: TaskEvent[] | null | undefined;
  missing: boolean;
  filters: LogFilters;
  retryBusy: boolean;
  retryError: RetryError | null;

  task: TaskRecord | null;
  attemptNumber: number | null;
  currentAttempt: number | null;
  stageNames: string[];
  retryKind: string;
  canRetry: boolean;
  logState: LogState | null | undefined;
  changesState: ChangesState | null | undefined;
  debugState: DebugState | null | undefined;

  /** A tab id, as the Tabs root reports it (its model value is string|number). */
  setTab(id: string | number): void;
  selectAttempt(attempt: number): void;
  setFilters(next: LogFilters): void;
  refreshAll(): void;
  performRetry(): Promise<void>;
  loadAttempts(): void;
  loadEvents(): void;
  loadTaskList(): void;
  loadLogs(attempt: number, opts?: { force?: boolean }): void;
  loadChanges(attempt: number, opts?: { force?: boolean }): void;
  loadDebug(attempt: number, opts?: { force?: boolean }): void;
}

const TASK_RUN: InjectionKey<TaskRun> = Symbol("task-run");

/**
 * Creates the run state for one task and provides it to the page's subtree.
 * `id` is the parsed task id, or null when the route's id is not a task id --
 * in which case nothing is ever fetched.
 */
export function provideTaskRun(id: ComputedRef<number | null>): TaskRun {
  const route = useRoute();
  const router = useRouter();

  const tab = ref(initialTab(route.query));
  const requestedAttempt = ref<number | null>(initialAttempt(route.query));
  const taskList = ref<TaskRecord[] | null | undefined>(undefined);
  const attempts = ref<AttemptsState | null | undefined>(undefined);
  const missing = ref(false);
  const events = ref<TaskEvent[] | null | undefined>(undefined);
  const filters = ref<LogFilters>({ ...EMPTY_LOG_FILTERS });
  const logs = ref<Cache<LogState>>(new Map());
  const changes = ref<Cache<ChangesState>>(new Map());
  const debug = ref<Cache<DebugState>>(new Map());
  const retryBusy = ref(false);
  const retryError = ref<RetryError | null>(null);
  const refreshToken = ref(0);
  const started = new Set<string>();

  const task = computed<TaskRecord | null>(() =>
    Array.isArray(taskList.value)
      ? taskList.value.find((t) => String(t.id) === String(id.value)) || null
      : null,
  );
  const currentAttempt = computed<number | null>(() =>
    Number(attempts.value?.current_attempt) > 0 ? Number(attempts.value?.current_attempt) : null,
  );
  // The wire spells "the task's current attempt" as 0, which is not an attempt
  // number: a task with no run at all must not address attempt 0.
  const attemptNumber = computed<number | null>(() => requestedAttempt.value ?? currentAttempt.value);
  const selectedAttempt = computed(() =>
    (attempts.value?.attempts || []).find((a) => Number(a.attempt) === Number(attemptNumber.value)),
  );
  const stageNames = computed(() => [
    ...new Set((selectedAttempt.value?.stages || []).map((stage) => stage.name).filter(Boolean) as string[]),
  ]);

  const retryMeta = computed(() => actionFor("retry"));
  const canRetry = computed(() => Boolean(retryMeta.value && (task.value?.actions || []).includes("retry")));

  const logKey = computed(() =>
    id.value == null || attemptNumber.value == null ? null : logCacheKey(id.value, attemptNumber.value, filters.value),
  );
  const changeKey = computed(() => (id.value == null ? null : attemptKey(id.value, attemptNumber.value)));
  const debugKey = changeKey;

  const logState = computed<LogState | null | undefined>(() =>
    logKey.value == null ? undefined : logs.value.get(logKey.value),
  );
  const changesState = computed<ChangesState | null | undefined>(() =>
    changeKey.value == null ? undefined : changes.value.get(changeKey.value),
  );
  const debugState = computed<DebugState | null | undefined>(() =>
    debugKey.value == null ? undefined : debug.value.get(debugKey.value),
  );

  function keyedLoad<T>(
    cacheKey: string,
    fetcher: () => Promise<T>,
    cache: Ref<Cache<T>>,
    { force = false }: { force?: boolean } = {},
  ): void {
    if (started.has(cacheKey) && !force) return;
    started.add(cacheKey);
    cache.value = new Map(cache.value).set(cacheKey, undefined);
    fetcher()
      .then((res) => {
        cache.value = new Map(cache.value).set(cacheKey, res);
      })
      .catch(() => {
        cache.value = new Map(cache.value).set(cacheKey, null);
      });
  }

  function loadTaskList(): void {
    if (id.value == null) return;
    taskList.value = undefined;
    api
      .tasks<TaskRecord[]>()
      .then((res) => {
        taskList.value = res;
      })
      .catch(() => {
        taskList.value = null;
      });
  }

  function loadAttempts(): void {
    if (id.value == null) return;
    attempts.value = undefined;
    missing.value = false;
    api
      .taskAttempts<AttemptsState>(String(id.value))
      .then((res) => {
        attempts.value = res;
      })
      .catch((err) => {
        if ((err as { status?: number })?.status === 404) missing.value = true;
        attempts.value = null;
      });
  }

  function loadEvents(): void {
    if (id.value == null) return;
    events.value = undefined;
    api
      .task<TaskEvent[]>(String(id.value))
      .then((res) => {
        events.value = res || [];
      })
      .catch(() => {
        events.value = null;
      });
  }

  function loadLogs(attempt: number, opts?: { force?: boolean }): void {
    if (id.value == null) return;
    const current = filters.value;
    keyedLoad(
      logCacheKey(id.value, attempt, current),
      () =>
        api.taskLogs<LogState>(String(id.value), {
          attempt: attempt || undefined,
          level: current.level,
          stage: current.stage,
          limit: 500,
        }),
      logs,
      opts,
    );
  }

  function loadChanges(attempt: number, opts?: { force?: boolean }): void {
    if (id.value == null) return;
    keyedLoad(
      attemptKey(id.value, attempt),
      () => api.taskChanges<ChangesState>(String(id.value), { attempt }),
      changes,
      opts,
    );
  }

  function loadDebug(attempt: number, opts?: { force?: boolean }): void {
    if (id.value == null) return;
    keyedLoad(
      attemptKey(id.value, attempt),
      () => api.taskDebug<DebugState>(String(id.value), { attempt }),
      debug,
      opts,
    );
  }

  /** Drop every cache so a manual refresh cannot show a stale capture or log
   * beside fresh attempt history. */
  function dropCaches(): void {
    started.clear();
    logs.value = new Map();
    changes.value = new Map();
    debug.value = new Map();
  }

  function refreshAll(): void {
    dropCaches();
    loadTaskList();
    loadAttempts();
    loadEvents();
    refreshToken.value += 1;
  }

  async function performRetry(): Promise<void> {
    if (id.value == null) return;
    retryError.value = null;
    retryBusy.value = true;
    try {
      await api.taskAction<void>(String(id.value), "retry");
      // A new attempt starts here. Everything cached belongs to the attempt
      // that just ended, so it is dropped rather than shown against the new
      // run: the attempt history, the events, every cached log, capture and
      // debug payload. The new attempt then becomes the selected run without a
      // reload.
      dropCaches();
      requestedAttempt.value = null;
      loadTaskList();
      loadAttempts();
      loadEvents();
      refreshToken.value += 1;
    } catch (err) {
      retryError.value = classifyActionError(err);
    } finally {
      retryBusy.value = false;
    }
  }

  onMounted(() => {
    // The page loads once for the task it was opened for; the route remounts it
    // when that changes (App.vue keys the outlet on the path).
    if (id.value == null) return;
    loadTaskList();
    loadAttempts();
    loadEvents();
  });

  watch([tab, attemptNumber, filters, refreshToken], () => {
    const attempt = attemptNumber.value;
    if (attempt == null) return;
    // Only the visible tab's lazy payload is fetched: the debug envelope
    // repeats the whole event list, so fetching it on every page load would
    // duplicate a large body for nothing.
    if (tab.value === "log") loadLogs(attempt);
    else if (tab.value === "changes") loadChanges(attempt);
    else if (tab.value === "debug") loadDebug(attempt);
  });

  watch([tab, attemptNumber], () => {
    if (id.value == null) return;
    // The open tab and the pinned attempt are written back with router.replace
    // rather than a hash assignment or a push: replace adds no history entry,
    // so a tab click does not remount and refetch the page, while the address
    // bar still holds the full state for a copy, a bookmark or a reload.
    void router.replace({ query: runQuery(tab.value, attemptNumber.value) });
  });

  const run = reactive({
    tab,
    attempts,
    taskList,
    events,
    missing,
    filters,
    retryBusy,
    retryError,
    task,
    attemptNumber,
    currentAttempt,
    stageNames,
    retryKind: computed(() => retryMeta.value?.kind || ""),
    canRetry,
    logState,
    changesState,
    debugState,
    setTab(next: string | number) {
      tab.value = String(next);
    },
    selectAttempt(attempt: number) {
      requestedAttempt.value = attempt;
    },
    setFilters(next: LogFilters) {
      filters.value = { ...next };
    },
    refreshAll,
    performRetry,
    loadAttempts,
    loadEvents,
    loadTaskList,
    loadLogs,
    loadChanges,
    loadDebug,
  });

  provide(TASK_RUN, run as TaskRun);
  return run as TaskRun;
}

/** The run state provided by the nearest TaskDetailPage. */
export function useTaskRun(): TaskRun {
  const run = inject(TASK_RUN);
  if (!run) throw new Error("useTaskRun() must be called under a TaskDetailPage");
  return run;
}
