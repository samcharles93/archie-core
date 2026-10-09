import assert from "node:assert/strict";
import test from "node:test";

const { parseWorkflowYaml, yamlLines } =
  await import("../src/workflows/workflow-yaml.ts");

/** A definition in the shape the server writes: id, then typed steps. */
const DEFINITION = `id: bootstrap
steps:
  - type: bootstrap.prepare
  - type: bootstrap.apply
    settings:
      mode: fast
`;

test("a definition parses into its id and its steps, in order", () => {
  const parsed = parseWorkflowYaml(DEFINITION);
  assert.equal(parsed.ok, true);
  if (!parsed.ok) return;
  assert.equal(parsed.id, "bootstrap");
  assert.deepEqual(
    parsed.steps.map((step) => [step.index, step.type]),
    [
      [1, "bootstrap.prepare"],
      [2, "bootstrap.apply"],
    ],
  );
});

test("the preview reads the YAML as typed, not a regex over it", () => {
  const parsed = parseWorkflowYaml(`id: x
steps:
  - type: a
    settings:
      nested:
        - type: not-a-step
  - type: b
`);
  assert.equal(parsed.ok, true);
  if (!parsed.ok) return;
  assert.deepEqual(
    parsed.steps.map((step) => step.type),
    ["a", "b"],
  );
});

test("invalid workflow structures identify the field that prevents saving", () => {
  for (const [source, message] of [
    ["steps:\n  - type: a\n", /id/],
    ["id: x\n", /steps/],
    ["id: x\nsteps:\n  - type: a\n  - settings: {}\n", /step 2/],
    ["- a\n- b\n", /mapping/],
    ['"just a string"\n', /mapping/],
    ["id: x\nsteps: 3\n", /steps/],
  ] as const) {
    const parsed = parseWorkflowYaml(source);
    assert.equal(parsed.ok, false, source);
    if (!parsed.ok) assert.match(parsed.message, message, source);
  }
});

test("a YAML syntax error is reported with the line it is on", () => {
  const parsed = parseWorkflowYaml("id: x\nsteps: [unclosed\n");
  assert.equal(parsed.ok, false);
  if (parsed.ok) return;
  assert.ok(parsed.message.length > 0, "an error with no words says nothing");
  assert.ok(
    typeof parsed.line === "number" && parsed.line >= 1,
    "an error that cannot be pointed at cannot be fixed",
  );
});

test("every character of the source survives highlighting", () => {
  const sources = [
    DEFINITION,
    "id: x\n\n# a comment\nsteps:\n  - type: a\n",
    "id: x\nsteps: []\n",
    "",
    "no trailing newline",
  ];
  for (const source of sources) {
    const lines = yamlLines(source);
    assert.equal(
      lines.map((line) => line.text).join("\n"),
      source,
      "highlighting rewrote the text it was given",
    );
    for (const line of lines) {
      assert.equal(
        line.tokens.map((token) => token.text).join(""),
        line.text,
        "a line's tokens do not add back up to the line",
      );
    }
  }
});

test("lines are numbered from one, and an empty editor still has a first line", () => {
  assert.deepEqual(
    yamlLines("a\nb").map((line) => line.number),
    [1, 2],
  );
  assert.deepEqual(
    yamlLines("").map((line) => [line.number, line.text]),
    [[1, ""]],
  );
});

test("keys and comments take their own colour, and a # inside a value does not", () => {
  const [keyLine, commentLine, valueLine] = yamlLines(
    'id: bootstrap\n# a note\nmessage: "a # b"\n',
  );
  assert.equal(keyLine.tokens[0]?.kind, "key");
  assert.equal(keyLine.tokens[0]?.text, "id");
  assert.deepEqual(
    commentLine.tokens.filter((token) => token.kind === "comment").map((t) => t.text),
    ["# a note"],
  );
  assert.equal(
    valueLine.tokens.some((token) => token.kind === "comment"),
    false,
    "a # inside a quoted value was read as a comment",
  );
  assert.equal(
    valueLine.tokens.some((token) => token.kind === "string"),
    true,
    "a quoted value was not coloured",
  );
});

test("a list marker and a key are not the same thing", () => {
  const [line] = yamlLines("  - type: a\n");
  assert.equal(line.tokens[0]?.text, "  ");
  assert.equal(line.tokens[1]?.text, "- ");
  assert.equal(line.tokens[1]?.kind, "punct");
  assert.equal(line.tokens[2]?.text, "type");
  assert.equal(line.tokens[2]?.kind, "key");
});
