import { parseGoDuration } from "../components/ui/duration-input/duration.ts";

/**
 * Client-side validation for the settings drafts.
 *
 * The server validates every replace, so this is not the only line of
 * defence: it exists so Save can be refused before the round-trip and the
 * operator sees the reason beside the field. The rules mirror the server's
 * (internal/app/controlplane), and the same map is read by the store, the
 * save bar and the pages' inline messages, so a rule is written once.
 *
 * A hand-written mirror drifts, which is why every rule here is pinned to the
 * server's by internal/app/controlplane/testdata/dashboard-validators.json:
 * a Go test runs the server validators over that fixture and this file's
 * tests run these validators over the same cases, so changing one side alone
 * fails a test (archie-core-ui-dashboard-3). Add a case to the fixture, not to
 * one side only.
 */

/** One field that must be fixed before its section can be saved. `path` is the
 * dotted leaf path diffValues reports, so an issue lines up with its change. */
export interface FieldIssue {
  path: string;
  label: string;
  message: string;
}

/** A section's rule: the draft value in, everything blocking a save out. */
export type DraftValidator = (value: unknown) => FieldIssue[];

/** An issue with the section it was found in. */
export interface BlockingField extends FieldIssue {
  kind: string;
}

/**
 * collectIssues runs each dirty section's validator against its draft. It is
 * driven by the dirty list on purpose: a section with no changes is not
 * blocked, so a value the server already accepted can never stop an unrelated
 * save.
 */
export function collectIssues(
  dirtyKinds: readonly string[],
  draftValue: (kind: string) => unknown,
  validators: Record<string, DraftValidator | undefined>,
): BlockingField[] {
  return dirtyKinds.flatMap((kind) => {
    const validate = validators[kind];
    if (!validate) return [];
    return validate(draftValue(kind)).map((issue) => ({ ...issue, kind }));
  });
}

interface SchedulingDraft {
  poll_interval?: string;
  label?: string | null;
  dispatch?: { trigger?: string };
}

/** validateSchedulingPolicy mirrors validateScheduling: a positive interval
 * and the label a label/either trigger requires. */
export function validateSchedulingPolicy(value: unknown): FieldIssue[] {
  const policy = (value ?? {}) as SchedulingDraft;
  const issues: FieldIssue[] = [];
  const ms = parseGoDuration(policy.poll_interval ?? "");
  if (ms === null || ms <= 0)
    issues.push({
      path: "poll_interval",
      label: "Poll interval",
      message: "Must be longer than zero.",
    });
  const trigger = policy.dispatch?.trigger;
  if (trigger && trigger !== "assignee" && !policy.label?.trim())
    issues.push({
      path: "label",
      label: "Label",
      message: "Required when the trigger is label or either.",
    });
  return issues;
}

interface ScheduleDraft {
  id?: string;
}

/** validateSchedules mirrors validateSchedules: an ID on every job, and no two
 * jobs sharing one. */
export function validateSchedules(value: unknown): FieldIssue[] {
  const jobs = Array.isArray(value) ? (value as ScheduleDraft[]) : [];
  const issues: FieldIssue[] = [];
  const seen = new Set<string>();
  jobs.forEach((job, i) => {
    const id = job.id?.trim() ?? "";
    if (!id) {
      issues.push({ path: `${i}.id`, label: `Schedule ${i + 1}`, message: "ID is required." });
      return;
    }
    if (seen.has(id))
      issues.push({ path: `${i}.id`, label: `Schedule ${i + 1}`, message: `Duplicate schedule "${id}".` });
    seen.add(id);
  });
  return issues;
}

const MODEL_ALIAS_NAME = /^[a-z0-9][a-z0-9._-]*$/;

/** validateModelAliases mirrors the server's validateModelAliases: alias
 * names are lowercase tokens, each value splits on its first slash into a
 * non-empty provider and model, and a non-empty table has a default alias. */
export function validateModelAliases(value: unknown): FieldIssue[] {
  const aliases = (value ?? {}) as Record<string, string>;
  const issues: FieldIssue[] = Object.entries(aliases).flatMap(([alias, model]): FieldIssue[] => {
    if (!MODEL_ALIAS_NAME.test(alias))
      return [{ path: alias, label: alias || "Alias", message: "Use lowercase letters, digits, '.', '_' or '-'." }];
    const slash = String(model).indexOf("/");
    if (slash <= 0 || slash === String(model).length - 1)
      return [{ path: alias, label: alias, message: "Use provider/model." }];
    return [];
  });
  if (Object.keys(aliases).length > 0 && !("default" in aliases))
    issues.push({ path: "default", label: "default", message: "The default alias is required." });
  return issues;
}

/** The rules the store applies to a dirty draft, keyed by resource kind. */
export const draftValidators: Record<string, DraftValidator> = {
  "scheduling-policy": validateSchedulingPolicy,
  schedules: validateSchedules,
  "model-aliases": validateModelAliases,
};
