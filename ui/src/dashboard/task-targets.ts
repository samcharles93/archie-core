const ATTENTION_STATUSES = new Set(["waiting_human", "parked"]);

/** The task fields these links are derived from. */
export interface TaskTarget {
  id: number | string;
  status?: string;
}

export interface TaskTargets {
  attention: { count: number; href: string };
  running: { count: number; href: string };
}

// Dashboard task links are derived from the actual task list, not just count
// aggregates, so a single item can open directly while a group opens a useful
// filtered board.
//
// The hrefs are router paths, not the `#/tasks` hashes the Preact build used.
// History routing means a hash is not a route any more, so one that survived
// here would navigate nowhere.
export function dashboardTaskTargets(tasks: TaskTarget[] = []): TaskTargets {
  const attention = tasks.filter((task) => ATTENTION_STATUSES.has(task.status ?? ""));
  const running = tasks.filter((task) => task.status === "running");
  return {
    attention: {
      count: attention.length,
      href:
        attention.length === 1
          ? `/tasks?task=${encodeURIComponent(attention[0].id)}`
          : "/tasks?status=needs_you",
    },
    running: {
      count: running.length,
      href: "/tasks?status=running",
    },
  };
}
