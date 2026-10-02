import type { ConfigView } from "./types";

/** The two pr-review dials as the control-plane resource carries them. */
export interface ReviewDials {
  precision_gate: boolean;
  approve_before_post: boolean;
}

export type ReviewDial = keyof ReviewDials;

/** One dial's resolved state: what is in force (from the published config
 * projection) beside the unsaved draft the page is editing. `inForce` is
 * undefined until the projection arrives, so the page shows "unknown" rather
 * than inventing a value; `pending` reports a draft edit the save bar has not
 * written yet. */
export interface ReviewDialStatus {
  inForce: boolean | undefined;
  draft: boolean;
  pending: boolean;
}

const DIALS: ReviewDial[] = ["precision_gate", "approve_before_post"];

/**
 * reviewDialStatus pairs each dial's effective value with its draft, so the
 * page can show what is in force beside the control and whether saving would
 * change it. The projection's `review` block is the layered cfg.Review the
 * daemon published; the draft is the review-settings resource document.
 */
export function reviewDialStatus(
  view: ConfigView | null | undefined,
  draft: ReviewDials,
): Record<ReviewDial, ReviewDialStatus> {
  const inForce = view?.review;
  const statuses = {} as Record<ReviewDial, ReviewDialStatus>;
  for (const dial of DIALS) {
    const effective = inForce?.[dial];
    statuses[dial] = {
      inForce: effective,
      draft: draft[dial],
      pending: effective !== undefined && effective !== draft[dial],
    };
  }
  return statuses;
}

/** inForceLabel renders one dial's effective value for the line beside it. */
export function inForceLabel(value: boolean | undefined): string {
  if (value === undefined) return "In force: unknown";
  return value ? "In force: on" : "In force: off";
}
