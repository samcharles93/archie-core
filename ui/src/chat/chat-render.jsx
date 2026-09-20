/**
 * Chat presentation helpers that are not components.
 *
 * The bubble row markup that used to live here is gone with the imperative DOM
 * layer: `assistantBubble`/`chatBubble` were a parallel, unreachable renderer
 * (nothing in `src/` called them except the equally unreachable chat-tools
 * helpers), and chat.jsx builds those rows as JSX itself.
 */

export function sessionTitle(session) {
  return session.title || session.branch_name || `Conversation ${session.session_id.slice(0, 8)}`;
}

/**
 * panelTitle names the launcher panel's header. The panel is small and the
 * session switcher sits below it, so the header says which conversation is
 * open rather than repeating the product name.
 */
export function panelTitle(sessions, currentSession) {
  if (!currentSession) return "Archie";
  const session = (sessions || []).find((item) => item.session_id === currentSession);
  return session ? sessionTitle(session) : "Archie";
}
