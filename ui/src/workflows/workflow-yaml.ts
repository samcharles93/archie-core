import { parseDocument } from "yaml";

/**
 * The workflow YAML editor's reading of what is typed.
 *
 * The server is the authority on a definition. The page checks what it can
 * from the served step vocabulary: the YAML parses, carries an id, names at
 * least one step, every step type is registered, and a workflow whose
 * repository is not required uses only steps that run without one. Step
 * settings stay the server's to check, so a save it refuses still reports the
 * server's own reason.
 */

/** A JSON Schema node, as the control plane derives it from Go types. */
export interface SettingsSchema {
  type?: string;
  title?: string;
  description?: string;
  enum?: string[];
  properties?: Record<string, SettingsSchema>;
  items?: SettingsSchema;
  additionalProperties?: SettingsSchema;
}

/** One registered step type, as the control-plane catalog serves it. */
export interface StepTypeInfo {
  name: string;
  needs_repository: boolean;
  /** Absent for a step that takes no settings. */
  settings?: SettingsSchema;
}

/** One chip in the read-only Steps preview. */
export interface WorkflowStepChip {
  /** 1-based, the position the server's own errors use. */
  index: number;
  type: string;
}

export type WorkflowParse =
  | { ok: true; id: string; steps: WorkflowStepChip[] }
  | { ok: false; message: string; line?: number };

/** What the editor's indicator reads. */
export function validationLabel(parsed: WorkflowParse): string {
  if (parsed.ok) return "Valid";
  return parsed.line ? `Line ${parsed.line}: ${parsed.message}` : parsed.message;
}

export function parseWorkflowYaml(
  source: string,
  vocabulary: StepTypeInfo[] = [],
): WorkflowParse {
  const document = parseDocument(source);
  const problem = document.errors[0];
  if (problem) {
    return {
      ok: false,
      message: firstLine(problem.message),
      line: problem.linePos?.[0]?.line,
    };
  }

  const value: unknown = document.toJS();
  if (!isMapping(value)) {
    return { ok: false, message: "A workflow is a mapping with an id and steps." };
  }

  const id = typeof value.id === "string" ? value.id.trim() : "";
  if (!id) return { ok: false, message: "workflow id is required" };

  const steps = value.steps;
  if (steps === undefined || steps === null || steps === "")
    return { ok: false, message: `workflow ${id} has no steps` };
  if (!Array.isArray(steps))
    return { ok: false, message: `workflow ${id}: steps must be a list` };
  if (!steps.length)
    return { ok: false, message: `workflow ${id} has no steps` };

  const known = new Map(vocabulary.map((info) => [info.name, info]));
  const repository =
    typeof value.repository === "string" && value.repository.trim()
      ? value.repository.trim()
      : "required";
  const chips: WorkflowStepChip[] = [];
  const declared = new Set<string>();
  for (const [i, step] of steps.entries()) {
    const where = `workflow ${id} step ${i + 1}`;
    if (isMapping(step) && isMapping(step.parallel)) {
      for (const [branch, branchSteps] of Object.entries(step.parallel)) {
        if (!Array.isArray(branchSteps) || !branchSteps.length)
          return { ok: false, message: `${where}: branch ${branch} needs a list of steps` };
        const visible = new Set(declared);
        for (const [j, branchStep] of branchSteps.entries()) {
          const branchWhere = `${where} branch ${branch} step ${j + 1}`;
          const problem =
            checkStepType(branchStep, branchWhere, known, repository) ||
            laterReference(branchStep, branchWhere, visible);
          if (problem) return { ok: false, message: problem };
          declareID(branchStep, visible);
        }
        for (const branchStep of branchSteps) declareID(branchStep, declared);
      }
      declareID(step, declared);
      chips.push({ index: i + 1, type: "parallel" });
      continue;
    }
    const problem = checkStepType(step, where, known, repository) || laterReference(step, where, declared);
    if (problem) return { ok: false, message: problem };
    declareID(step, declared);
    chips.push({ index: i + 1, type: (step as { type: string }).type.trim() });
  }
  return { ok: true, id, steps: chips };
}

const STEP_REFERENCE = /steps\.([A-Za-z0-9_-]+)/g;

/** A reference to a step that has not run yet: the server refuses it, so the
 * page says so before save. */
function laterReference(step: unknown, where: string, declared: Set<string>): string {
  if (!isMapping(step)) return "";
  const text = JSON.stringify(step.settings ?? {}) + " " + (typeof step.when === "string" ? step.when : "");
  for (const [, ref] of text.matchAll(STEP_REFERENCE))
    if (!declared.has(ref)) return `${where}: no earlier step has id "${ref}"`;
  return "";
}

function declareID(step: unknown, declared: Set<string>): void {
  if (isMapping(step) && typeof step.id === "string") declared.add(step.id);
}

/** What is wrong with one typed step, or "" when nothing the page can see. */
function checkStepType(
  step: unknown,
  where: string,
  known: Map<string, StepTypeInfo>,
  repository: string,
): string {
  if (!isMapping(step) || !isNonEmptyString(step.type)) return `${where} needs a type`;
  const type = step.type.trim();
  const info = known.get(type);
  if (known.size && !info) return `${where}: unknown step type "${type}"`;
  if (info?.needs_repository && repository !== "required")
    return `${where}: "${type}" needs a repository, but the workflow's repository is ${repository}`;
  if (!info || step.settings === undefined || step.settings === null) return "";
  if (!info.settings) return `${where}: "${type}" takes no settings`;
  return settingsProblem(step.settings, info.settings, `${where} settings`);
}

/** The first way a settings value breaks its schema, or "". A string holding a
 * {{ reference }} is resolved at run time, so it is not held to an enum. */
export function settingsProblem(value: unknown, schema: SettingsSchema, path: string): string {
  const actual = Array.isArray(value) ? "array" : value === null ? "null" : typeof value;
  switch (schema.type) {
    case "object": {
      if (!isMapping(value)) return `${path} must be a mapping`;
      for (const [key, child] of Object.entries(value)) {
        const childSchema = schema.properties?.[key] ?? schema.additionalProperties;
        if (!childSchema) return `${path}: unknown setting "${key}"`;
        const problem = settingsProblem(child, childSchema, `${path}.${key}`);
        if (problem) return problem;
      }
      return "";
    }
    case "array": {
      if (!Array.isArray(value)) return `${path} must be a list`;
      for (const [i, item] of value.entries()) {
        const problem = schema.items ? settingsProblem(item, schema.items, `${path}[${i}]`) : "";
        if (problem) return problem;
      }
      return "";
    }
    case "string":
      if (actual !== "string") return `${path} must be text`;
      if (schema.enum && !schema.enum.includes(value as string) && !(value as string).includes("{{"))
        return `${path} must be one of ${schema.enum.join(", ")}`;
      return "";
    case "boolean":
      return actual === "boolean" ? "" : `${path} must be true or false`;
    case "integer":
    case "number":
      return actual === "number" ? "" : `${path} must be a number`;
  }
  return "";
}

// --- the editor's own rendering ---------------------------------------------
//
// Light syntax colouring, not a parse: every character of the source comes back
// exactly once, so the highlighted layer behind the textarea can never drift
// out of step with the text in it.

export type YamlTokenKind = "key" | "string" | "comment" | "punct" | "plain";

export interface YamlToken {
  text: string;
  kind: YamlTokenKind;
}

export interface YamlLine {
  /** 1-based, as the gutter prints it. */
  number: number;
  text: string;
  tokens: YamlToken[];
}

/** The token's colour, from the design tokens only. */
export function yamlTokenClass(kind: YamlTokenKind): string {
  switch (kind) {
    case "key":
      return "text-fg-muted";
    case "string":
      return "text-ok";
    case "comment":
    case "punct":
      return "text-fg-subtle";
    default:
      return "text-foreground";
  }
}

export function yamlLines(source: string): YamlLine[] {
  return source
    .split("\n")
    .map((text, i) => ({ number: i + 1, text, tokens: lineTokens(text) }));
}

// A list marker and any indentation that precedes it, then whatever follows.
const LINE_PREFIX = /^(\s*)(-\s+)?/;
// A mapping entry: a key, its colon, and the scalar after it. A key cannot
// contain a colon, which is what keeps `https://…` a value and not a key.
const MAPPING_ENTRY = /^([^:#\s][^:#]*?)(:)(\s*)(.*)$/;

function lineTokens(text: string): YamlToken[] {
  const { code, comment } = splitComment(text);
  const tokens = codeTokens(code);
  if (comment) tokens.push({ text: comment, kind: "comment" });
  return tokens;
}

/** A `#` opens a comment at the start of a line or after a space, and only
 * outside a quoted scalar: `"a # b"` is a value, not a value and a comment. */
function splitComment(text: string): { code: string; comment: string } {
  let quote = "";
  for (let i = 0; i < text.length; i++) {
    const char = text[i];
    if (quote) {
      if (char === quote) quote = "";
      continue;
    }
    if (char === '"' || char === "'") {
      quote = char;
      continue;
    }
    if (char === "#" && (i === 0 || /\s/.test(text[i - 1] ?? ""))) {
      return { code: text.slice(0, i), comment: text.slice(i) };
    }
  }
  return { code: text, comment: "" };
}

function codeTokens(code: string): YamlToken[] {
  if (!code) return [];
  const tokens: YamlToken[] = [];
  const prefix = LINE_PREFIX.exec(code);
  if (prefix?.[1]) tokens.push({ text: prefix[1], kind: "plain" });
  if (prefix?.[2]) tokens.push({ text: prefix[2], kind: "punct" });

  const rest = code.slice(prefix?.[0].length ?? 0);
  if (!rest) return tokens;

  const entry = MAPPING_ENTRY.exec(rest);
  if (!entry) return [...tokens, ...scalarTokens(rest)];

  tokens.push({ text: entry[1] ?? "", kind: "key" });
  tokens.push({ text: entry[2] ?? ":", kind: "punct" });
  if (entry[3]) tokens.push({ text: entry[3], kind: "plain" });
  return [...tokens, ...scalarTokens(entry[4] ?? "")];
}

function scalarTokens(value: string): YamlToken[] {
  if (!value) return [];
  const quoted = /^("[^"]*"?|'[^']*'?)([\s\S]*)$/.exec(value);
  if (!quoted) return [{ text: value, kind: "plain" }];
  const tokens: YamlToken[] = [{ text: quoted[1] ?? "", kind: "string" }];
  if (quoted[2]) tokens.push({ text: quoted[2], kind: "plain" });
  return tokens;
}

function firstLine(message: string): string {
  return message.split("\n")[0]?.trim() || "invalid YAML";
}

function isMapping(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isNonEmptyString(value: unknown): value is string {
  return typeof value === "string" && value.trim() !== "";
}
