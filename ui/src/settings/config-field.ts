/** The empty marker. A value the server did not send is shown as an em dash,
 * which is a fact about the running config, not a missing render. */
export function valueText(value: unknown): string {
  if (value === null || value === undefined || value === "") return "—";
  return String(value);
}

