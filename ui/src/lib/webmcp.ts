// WebMCP registration for the dashboard.
//
// WebMCP is the browser API an in-page agent uses to discover and call a site's
// own tools. Registration lives here rather than in a component: the tool layer
// talks to the dashboard's HTTP client, so it survives whatever renders it.
//
// The receiver is `document.modelContext` (Chromium 150+). The earlier
// `navigator.modelContext.provideContext({tools})` shape registers nothing in
// shipping Chrome. WebMCP is an origin trial, so an absent modelContext is
// normal rather than an error: registration no-ops and the dashboard is
// unaffected.
//
// Three shapes in this API are JSON strings where an object is expected, and
// each failure looks like something else:
//   - `getTools()` returns each tool's `inputSchema` as a JSON string; parse it
//     before reading `.properties`, or the schema reads as empty.
//   - `executeTool(tool, args)` takes its arguments as a JSON string, not an
//     object (a plain object fails with "Failed to parse input arguments"), and
//     its first argument must be the RegisteredTool object from `getTools()`,
//     not a name (a name fails with a TypeError).
//   - `executeTool` returns the tool's result as a JSON string that parses to
//     `{content:[{type:"text",text}]}`, not as the envelope object itself.
// A thrown `execute` error is also flattened by `executeTool` into a generic
// UnknownError, so its text cannot tell a rejected call from a failed one; a
// guard inside `execute` has to be pinned by a unit test, not by a browser run.
//
// Two deployment conditions make the tools silently absent, with no error:
//   - `document.modelContext` needs a secure context. A dashboard served over
//     plain HTTP (a LAN address, say) never exposes it, even with the
//     origin-trial flag: `isSecureContext` is false and the API is undefined.
//     Production terminates TLS in front of the dashboard; a plain-HTTP
//     deployment gets no tools and no message saying so.
//   - archie-ui embeds the built ui/dist into its binary, so rebuilding the
//     dist is not deploying it. A running process keeps serving the bundle it
//     embedded at build time, and a check against that stale process reports
//     the tools as missing. Verify against a freshly built bundle, or restart
//     the process.
//
// What the browser does not enforce, it should not appear to: see the
// annotation note in webmcp-tools.ts.

/** The annotation hints a WebMCP host reads. */
export interface WebMcpAnnotations {
  readOnlyHint?: boolean;
  untrustedContentHint?: boolean;
  consequentialHint?: boolean;
}

/** One dashboard tool as registered with the browser. */
export interface WebMcpTool {
  name: string;
  description: string;
  inputSchema: Record<string, unknown>;
  annotations: WebMcpAnnotations;
  execute: (input: Record<string, unknown>) => Promise<unknown> | unknown;
}

/** The part of the ModelContext interface these tools use. */
interface ModelContextLike {
  registerTool: (tool: WebMcpTool, options?: { signal?: AbortSignal }) => Promise<void>;
}

/** The outcome of registration: whether it took, and how to undo it. */
export interface WebMcpRegistration {
  registered: boolean;
  dispose: () => void;
}

function modelContext(): ModelContextLike | undefined {
  if (typeof document === "undefined") return undefined;
  const context = (document as Document & { modelContext?: ModelContextLike }).modelContext;
  return typeof context?.registerTool === "function" ? context : undefined;
}

/**
 * Registers tools with the page's model context, returning a registration whose
 * `dispose` aborts the shared signal (WebMCP has no unregister method).
 *
 * A host that rejects one tool does not lose the others; a host that is absent
 * is a no-op. Neither is worth failing the dashboard over.
 */
export async function registerWebMcpTools(tools: WebMcpTool[]): Promise<WebMcpRegistration> {
  const context = modelContext();
  if (!context) return { registered: false, dispose: () => {} };

  const controller = new AbortController();
  for (const tool of tools) {
    try {
      await context.registerTool(tool, { signal: controller.signal });
    } catch (err) {
      console.warn(`webmcp: registering ${tool.name} failed`, err);
    }
  }
  return { registered: true, dispose: () => controller.abort() };
}

/**
 * A register function that replaces its previous registration on each call.
 * Tool modules re-run under hot reload, and WebMCP has no way to list and
 * unregister tools: without this, a reload publishes a second copy of every
 * tool.
 */
export function createWebMcpRegistrar(tools: WebMcpTool[]): () => Promise<WebMcpRegistration> {
  let current: WebMcpRegistration | undefined;
  return async () => {
    current?.dispose();
    current = await registerWebMcpTools(tools);
    return current;
  };
}
