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
