/**
 * How a capture may dispatch: signed by its source's secret, taken on a
 * source approved for unsigned events, or neither -- in which case nothing
 * starts from it.
 */
export interface CaptureSignature {
  label: "Signed" | "Unsigned" | "Unverified";
  kind: "ok" | "warn" | "idle";
}

export function captureSignature(capture: {
  authenticated?: boolean;
  unsigned?: boolean;
}): CaptureSignature {
  if (capture.unsigned) return { label: "Unsigned", kind: "warn" };
  if (capture.authenticated) return { label: "Signed", kind: "ok" };
  return { label: "Unverified", kind: "idle" };
}
