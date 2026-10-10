import assert from "node:assert/strict";
import test from "node:test";

import { availableProviders, aliasModelOptions } from "../src/settings/model-choices.ts";

const catalog = [
  { id: "anthropic", name: "Anthropic", class: "anthropic", api_key_env: "ANTHROPIC_API_KEY", models: ["claude-a"] },
  { id: "openai", name: "OpenAI", class: "openai", api_key_env: "OPENAI_API_KEY", models: ["gpt-a", "gpt-b"] },
  { id: "groq", name: "Groq", class: "groq", models: ["llama"] },
];

test("role models come only from configured providers", () => {
  assert.deepEqual(aliasModelOptions(catalog, ["openai", "deepseek"]), ["openai/gpt-a", "openai/gpt-b"]);
  assert.deepEqual(aliasModelOptions(catalog, []), []);
});

test("available providers are the catalog's ones not yet configured", () => {
  assert.deepEqual(
    availableProviders(catalog, ["openai"]).map((p) => p.id),
    ["anthropic", "groq"],
  );
});
