const ATTENTION_STATUSES = new Set(["waiting_human", "parked"]);

// Dashboard task links are derived from the actual task list, not just count
// aggregates, so a single item can open directly while a group opens a useful
// filtered board.
export function dashboardTaskTargets(tasks = []) {
  const attention = tasks.filter((task) => ATTENTION_STATUSES.has(task.status));
  const running = tasks.filter((task) => task.status === "running");
  return {
    attention: {
      count: attention.length,
      href: attention.length === 1
        ? `#/tasks?task=${encodeURIComponent(attention[0].id)}`
        : "#/tasks?status=needs_you",
    },
    running: {
      count: running.length,
      href: "#/tasks?status=running",
    },
  };
}
