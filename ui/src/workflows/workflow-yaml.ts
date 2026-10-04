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

/** One registered step type, as the control-plane catalog serves it. */
export interface StepTypeInfo {
  name: string;
  needs_repository: boolean;
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
  for (const [i, step] of steps.entries()) {
    if (!isMapping(step) || !isNonEmptyString(step.type)) {
      return {
        ok: false,
        message: `workflow ${id} step ${i + 1} needs a type`,
      };
    }
    const type = step.type.trim();
    const info = known.get(type);
    if (known.size && !info) {
      return {
        ok: false,
        message: `workflow ${id} step ${i + 1}: unknown step type "${type}"`,
      };
    }
    if (info?.needs_repository && repository !== "required") {
      return {
        ok: false,
        message: `workflow ${id} step ${i + 1}: "${type}" needs a repository, but the workflow's repository is ${repository}`,
      };
    }
    chips.push({ index: i + 1, type });
  }
  return { ok: true, id, steps: chips };
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
