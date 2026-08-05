export function taskRowA11y(task, expanded) {
  const title = task.title || "untitled task";
  return {
    "aria-expanded": String(expanded),
    "aria-controls": `task-timeline-${task.id}`,
    "aria-label": `${expanded ? "Collapse" : "Expand"} timeline for ${title}`,
  };
}
