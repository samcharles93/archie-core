// Binds the dashboard's WebMCP tool list to the dashboard's own HTTP client.
//
// Nothing here reaches into a component: the tools call the same `api` methods
// the UI calls, so the agent and the interface exercise one set of endpoints,
// one CSRF header, and one error shape.
import { api } from "./api";
import { createWebMcpRegistrar } from "./webmcp";
import { archieWebMcpTools } from "./webmcp-tools";

/** Registers the dashboard's tools, replacing any earlier registration. */
export const registerArchieWebMcpTools = createWebMcpRegistrar(archieWebMcpTools(api));
