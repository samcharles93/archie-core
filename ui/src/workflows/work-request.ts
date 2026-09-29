/**
 * The work-request form's input vocabulary: what the chosen workflow declares,
 * and how one field's text becomes the typed value the daemon checks.
 *
 * A workflow is startable only by naming what it declares. pr-review requires
 * pr_number, so a request that does not set it is refused by the handler
 * (archie-core-06nq) rather than admitted and then parked at dispatch. The
 * declaration comes from the definitions endpoint, so a workflow that starts
 * declaring inputs needs no change here.
 */

import type { WorkflowDefinition } from "./workflow-rows";

/** One input a workflow declares, as the form renders it. */
export interface DeclaredInput {
  name: string;
  /** The declared type: string, number, bool, object, array or any. */
  type: string;
  required: boolean;
}

/**
 * declaredInputs lists the inputs a workflow declares, by name. Sorted, so the
 * fields keep their places while the operator types and across re-renders.
 */
export function declaredInputs(
  definition?: WorkflowDefinition,
): DeclaredInput[] {
  return Object.entries(definition?.inputs ?? {})
    .map(([name, spec]) => ({
      name,
      type: spec.type,
      required: spec.required === true,
    }))
    .sort((a, b) => a.name.localeCompare(b.name));
}

/**
 * missingRequiredInputs names the declared inputs that are required and still
 * empty, so the form disables Start instead of earning the server's refusal.
 */
export function missingRequiredInputs(
  declared: DeclaredInput[],
  text: Record<string, string>,
): string[] {
  return declared
    .filter((input) => input.required && !(text[input.name] ?? "").trim())
    .map((input) => input.name);
}

/**
 * inputValue is the typed value one field's text stands for. Text that does not
 * read as the declared type travels as it was typed, so the server's check
 * names the mismatch rather than this guessing at a value nobody typed.
 */
export function inputValue(text: string, type: string): unknown {
  switch (type) {
    case "number": {
      const trimmed = text.trim();
      return trimmed !== "" && !Number.isNaN(Number(trimmed))
        ? Number(trimmed)
        : text;
    }
    case "bool":
      return text === "true" ? true : text === "false" ? false : text;
    case "string":
      return text;
    default:
      // object, array and any: the field's text is JSON.
      try {
        return JSON.parse(text);
      } catch {
        return text;
      }
  }
}

/**
 * inputValues builds the `inputs` object the request carries: the declared
 * inputs the operator filled in. An input left empty is absent rather than an
 * empty string, so an optional input is simply unset and a required one is
 * caught by missingRequiredInputs before the request is sent.
 */
export function inputValues(
  declared: DeclaredInput[],
  text: Record<string, string>,
): Record<string, unknown> {
  const inputs: Record<string, unknown> = {};
  for (const input of declared) {
    const value = text[input.name];
    if (value === undefined || value === "") continue;
    inputs[input.name] = inputValue(value, input.type);
  }
  return inputs;
}
