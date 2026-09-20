// Keeping the arrow-key selection visible in the command palette.
//
// The palette caps its height and scrolls, so once the matches run past the
// visible rows the selection walked off-screen and the highlight was invisible
// (archie-core-ifay). Browsers have scrollIntoView, but the decision is pure
// arithmetic and worth testing on its own, so the effect only applies what
// this returns.

/** The palette's scroll geometry and the selected option's position in it. */
export interface CommandScrollGeometry {
  scrollTop: number;
  viewportHeight: number;
  optionTop: number;
  optionHeight: number;
}

/**
 * commandScrollTop returns the scrollTop that brings the selected option fully
 * into view, or the current scrollTop when it is already visible. Mirrors
 * `scrollIntoView({ block: "nearest" })`: it moves the minimum distance, and
 * scrolls up for an option above the viewport, down for one below.
 */
export function commandScrollTop({
  scrollTop,
  viewportHeight,
  optionTop,
  optionHeight,
}: CommandScrollGeometry): number {
  // An option taller than the viewport cannot be shown whole. Align its top,
  // which holds the command itself; aligning the bottom would scroll the name
  // out of sight and leave only the tail of its description.
  if (optionHeight >= viewportHeight) return optionTop;

  if (optionTop < scrollTop) return optionTop;

  const optionBottom = optionTop + optionHeight;
  const viewportBottom = scrollTop + viewportHeight;
  if (optionBottom > viewportBottom) return optionBottom - viewportHeight;

  return scrollTop;
}
