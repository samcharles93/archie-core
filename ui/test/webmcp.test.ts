import assert from "node:assert/strict";
import test from "node:test";

// The model context is read at registration time, so each test installs its own
// fake `document` before calling. The tool list takes its API client as a
// parameter, so these tests never load the real fetch client.
const { registerWebMcpTools, createWebMcpRegistrar } =
  await import("../src/lib/webmcp.ts");
const { archieWebMcpTools } = await import("../src/lib/webmcp-tools.ts");

interface FakeRegistered {
  name: string;
  description: string;
  inputSchema: unknown;
  annotations: Record<string, unknown> | undefined;
  signal: AbortSignal | undefined;
  execute: (input: Record<string, unknown>) => unknown;
}

function installModelContext(): { registered: FakeRegistered[] } {
  const registered: FakeRegistered[] = [];
  Object.defineProperty(globalThis, "document", {
    configurable: true,
    value: {
      modelContext: {
        registerTool: async (
          tool: Omit<FakeRegistered, "signal">,
          options?: { signal?: AbortSignal },
        ) => {
          registered.push({ ...tool, signal: options?.signal });
        },
      },
    },
  });
  return { registered };
}

function installNoModelContext(): void {
  Object.defineProperty(globalThis, "document", {
    configurable: true,
    value: {},
  });
}

interface Call {
  method: string;
  args: unknown[];
}

function fakeClient(): {
  calls: Call[];
  client: Parameters<typeof archieWebMcpTools>[0];
} {
  const calls: Call[] = [];
  const reply = (method: string, args: unknown[]) => {
    calls.push({ method, args });
    return { ok: true, method, args };
  };
  return {
    calls,
    client: {
      health: async () => reply("health", []),
      tasks: async () => reply("tasks", []),
      task: async (id: string) => reply("task", [id]),
      captures: async (limit?: number) => reply("captures", [limit]),
      taskAction: async (id: string, action: string) =>
        reply("taskAction", [id, action]),
    },
  };
}

function toolNamed(
  client: Parameters<typeof archieWebMcpTools>[0],
  name: string,
) {
  const tool = archieWebMcpTools(client).find(
    (candidate) => candidate.name === name,
  );
  assert.ok(tool, `no tool named ${name}`);
  return tool;
}

test("registration no-ops without document.modelContext instead of throwing", async () => {
  installNoModelContext();
  const registration = await registerWebMcpTools(
    archieWebMcpTools(fakeClient().client),
  );
  assert.equal(registration.registered, false);
  assert.doesNotThrow(() => registration.dispose());
});

test("registration passes each tool's schema and annotations, and disposes via the signal", async () => {
  const { registered } = installModelContext();
  const tools = archieWebMcpTools(fakeClient().client);
  const registration = await registerWebMcpTools(tools);

  assert.equal(registration.registered, true);
  assert.deepEqual(
    registered.map((tool) => tool.name),
    tools.map((tool) => tool.name),
  );
  for (const tool of registered) {
    assert.ok(tool.description.length > 0, `${tool.name} has no description`);
    assert.equal(typeof tool.inputSchema, "object");
    assert.ok(
      tool.signal instanceof AbortSignal,
      `${tool.name} has no abort signal`,
    );
    assert.equal(tool.signal.aborted, false);
  }

  registration.dispose();
  for (const tool of registered) {
    assert.equal(
      tool.signal?.aborted,
      true,
      `${tool.name} was not unregistered`,
    );
  }
});

test("read-only tools are read-only; recovery tools are consequential", () => {
  const annotations = new Map(
    archieWebMcpTools(fakeClient().client).map((tool) => [
      tool.name,
      tool.annotations,
    ]),
  );

  for (const name of [
    "list_tasks",
    "get_task",
    "recent_events",
    "daemon_health",
  ]) {
    assert.equal(
      annotations.get(name)?.readOnlyHint,
      true,
      `${name} should be read-only`,
    );
    assert.notEqual(
      annotations.get(name)?.consequentialHint,
      true,
      `${name} must not be consequential`,
    );
  }
  for (const name of ["retry_task", "stop_task", "cancel_task"]) {
    assert.equal(
      annotations.get(name)?.consequentialHint,
      true,
      `${name} needs a confirmation`,
    );
    assert.notEqual(
      annotations.get(name)?.readOnlyHint,
      true,
      `${name} must not be read-only`,
    );
  }
});

test("tools returning agent, operator or inbound content carry untrustedContentHint", () => {
  const annotations = new Map(
    archieWebMcpTools(fakeClient().client).map((tool) => [
      tool.name,
      tool.annotations,
    ]),
  );
  for (const name of ["list_tasks", "get_task", "recent_events"]) {
    assert.equal(
      annotations.get(name)?.untrustedContentHint,
      true,
      `${name} returns untrusted content`,
    );
  }
  assert.equal(annotations.get("daemon_health")?.untrustedContentHint, false);
  assert.equal(annotations.get("list_tasks")?.consequentialHint, undefined);
});

test("execute returns the envelope and calls the dashboard's own client methods", async () => {
  const { calls, client } = fakeClient();

  const listed = (await toolNamed(client, "list_tasks").execute({})) as {
    content: { type: string; text: string }[];
  };
  assert.equal(calls[0].method, "tasks");
  assert.equal(listed.content[0].type, "text");
  assert.deepEqual(JSON.parse(listed.content[0].text), {
    ok: true,
    method: "tasks",
    args: [],
  });

  await toolNamed(client, "get_task").execute({ task_id: "42" });
  assert.deepEqual(calls[1], { method: "task", args: ["42"] });

  await toolNamed(client, "recent_events").execute({ limit: 10 });
  assert.deepEqual(calls[2], { method: "captures", args: [10] });

  await toolNamed(client, "daemon_health").execute({});
  assert.deepEqual(calls[3], { method: "health", args: [] });
});

test("recovery tools post the dashboard's own action verbs", async () => {
  const { calls, client } = fakeClient();
  await toolNamed(client, "retry_task").execute({
    task_id: "9",
    confirm: true,
  });
  await toolNamed(client, "stop_task").execute({ task_id: "9", confirm: true });
  await toolNamed(client, "cancel_task").execute({
    task_id: "9",
    confirm: true,
  });

  assert.deepEqual(calls, [
    { method: "taskAction", args: ["9", "retry"] },
    { method: "taskAction", args: ["9", "stop"] },
    { method: "taskAction", args: ["9", "cancel"] },
  ]);
});

test("recovery tools require an explicit confirm flag before touching the API", async () => {
  const { calls, client } = fakeClient();
  const retry = toolNamed(client, "retry_task");

  await assert.rejects(
    () => Promise.resolve(retry.execute({ task_id: "9" })),
    /confirm must be true/,
  );
  await assert.rejects(
    () => Promise.resolve(retry.execute({ task_id: "9", confirm: false })),
    /confirm must be true/,
  );
  assert.equal(calls.length, 0, "a call without confirm reached the API");

  await retry.execute({ task_id: "9", confirm: true });
  assert.deepEqual(calls, [{ method: "taskAction", args: ["9", "retry"] }]);
});

test("the recovery schema requires both task_id and confirm", () => {
  const schema = archieWebMcpTools(fakeClient().client).find(
    (tool) => tool.name === "stop_task",
  )!.inputSchema as {
    required?: string[];
  };
  assert.deepEqual(schema.required, ["task_id", "confirm"]);
});

test("a recovery tool without a task id fails before touching the API", async () => {
  const { calls, client } = fakeClient();
  await assert.rejects(
    () => Promise.resolve(toolNamed(client, "retry_task").execute({})),
    /task_id is required/,
  );
  assert.equal(calls.length, 0);
});

test("re-registering replaces the previous registration", async () => {
  const { registered } = installModelContext();
  const register = createWebMcpRegistrar(
    archieWebMcpTools(fakeClient().client),
  );
  const count = archieWebMcpTools(fakeClient().client).length;

  const first = await register();
  const second = await register();

  assert.notEqual(first, second);
  assert.equal(registered.length, count * 2);
  for (const tool of registered.slice(0, count)) {
    assert.equal(
      tool.signal?.aborted,
      true,
      "the first registration was left live",
    );
  }
});
