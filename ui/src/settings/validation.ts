import { parseGoDuration } from "../components/ui/duration-input/duration.ts";

/**
 * Client-side validation for the settings drafts.
 *
 * The server validates every replace, so this is not the only line of
 * defence: it exists so Save can be refused before the round-trip and the
 * operator sees the reason beside the field. The rules mirror the server's
 * (internal/app/controlplane), and the same map is read by the store, the
 * save bar and the pages' inline messages, so a rule is written once.
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

/** The runtime resolves a role as provider/model, so a value with no "/" is
 * one the server will refuse. */
const ROLE_MODEL = /^[^/\s]+\/\S+$/;

/** validateModelRoles mirrors validateModelRoles: every role names a model as
 * provider/model. */
export function validateModelRoles(value: unknown): FieldIssue[] {
  const roles = (value ?? {}) as Record<string, string>;
  return Object.entries(roles)
    .filter(([, model]) => model && !ROLE_MODEL.test(model))
    .map(([role]) => ({ path: role, label: role, message: "Use provider/model." }));
}

/** The rules the store applies to a dirty draft, keyed by resource kind. */
export const draftValidators: Record<string, DraftValidator> = {
  "scheduling-policy": validateSchedulingPolicy,
  schedules: validateSchedules,
  "model-role-assignments": validateModelRoles,
};
