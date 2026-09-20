import type { ConfigFieldType } from "./types";

/** The empty marker. A value the server did not send is shown as an em dash,
 * which is a fact about the running config, not a missing render. */
export function valueText(value: unknown): string {
  if (value === null || value === undefined || value === "") return "—";
  return String(value);
}

/** parseEdit turns the editor's text back into the value the wire expects, or
 * says why it cannot. Types without a numeric or boolean form are sent as the
 * text as typed, which is what the daemon's own parser then validates. */
export function parseEdit(type: ConfigFieldType, text: string): { value?: unknown; error?: string } {
  switch (type) {
    case "int": {
      const n = parseInt(text, 10);
      if (Number.isNaN(n)) return { error: "Enter a whole number" };
      return { value: n };
    }
    case "bool":
      return { value: text === "true" };
    default:
      return { value: text };
  }
}
