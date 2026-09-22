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
