import type { ChatSession } from "./state";

export function sessionTitle(session: ChatSession): string {
  return session.title || session.branch_name || `Conversation ${session.session_id.slice(0, 8)}`;
}

/**
 * panelTitle names the launcher panel's header. The panel is small and the
 * session switcher sits below it, so the header says which conversation is
 * open rather than repeating the product name.
 */
export function panelTitle(sessions: ChatSession[], currentSession: string): string {
  if (!currentSession) return "Archie";
  const session = (sessions || []).find((item) => item.session_id === currentSession);
  return session ? sessionTitle(session) : "Archie";
}
