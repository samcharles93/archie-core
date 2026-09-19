import { useRef } from "preact/hooks";

// A tab bar for the run detail page. There is no other consumer yet, so it
// stays in tasks/ rather than in base (organisation.md extracts on the second
// distinct consumer).
//
// Keyboard behaviour follows the WAI-ARIA tabs pattern with automatic
// activation: the tab list is one stop in the page's tab order (roving
// tabindex), Left/Right move between tabs and select as they go, Home/End jump
// to the ends. Automatic activation is the right choice here because every
// panel is a local render, not a fetch: there is no cost to switching.

export function tabId(prefix, id) {
  return `${prefix}-tab-${id}`;
}

export function panelId(prefix, id) {
  return `${prefix}-panel-${id}`;
}

export function TabBar({ tabs, active, onSelect, prefix = "run", label = "Run detail views" }) {
  const refs = useRef([]);

  const select = (index) => {
    const tab = tabs[index];
    if (!tab) return;
    onSelect?.(tab.id);
    refs.current[index]?.focus?.();
  };

  const onKeyDown = (event) => {
    const current = tabs.findIndex((tab) => tab.id === active);
    if (current < 0) return;
    let next;
    if (event.key === "ArrowRight") next = (current + 1) % tabs.length;
    else if (event.key === "ArrowLeft") next = (current - 1 + tabs.length) % tabs.length;
    else if (event.key === "Home") next = 0;
    else if (event.key === "End") next = tabs.length - 1;
    else return;
    event.preventDefault();
    select(next);
  };

  return (
    <div className="tab-bar" role="tablist" aria-label={label} onKeyDown={onKeyDown}>
      {tabs.map((tab, index) => {
        const selected = tab.id === active;
        return (
          <button
            key={tab.id}
            ref={(node) => {
              refs.current[index] = node;
            }}
            id={tabId(prefix, tab.id)}
            className={`tab${selected ? " is-selected" : ""}`}
            type="button"
            role="tab"
            aria-selected={selected}
            aria-controls={panelId(prefix, tab.id)}
            tabIndex={selected ? 0 : -1}
            onClick={() => onSelect?.(tab.id)}
          >
            {tab.label}
          </button>
        );
      })}
    </div>
  );
}
