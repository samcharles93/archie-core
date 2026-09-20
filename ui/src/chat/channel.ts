import { randomUUID } from "@/lib/uuid";

const CHANNEL_KEY = "archie.web.chat.channel";

/**
 * The browser's chat identity. The server keys a conversation on the channel
 * alone (the browser has no threading), so this is persisted: a reload rejoins
 * the conversation instead of starting a second one beside it.
 */
export function channelID(): string {
  let id = localStorage.getItem(CHANNEL_KEY);
  if (!id) {
    id = randomUUID();
    localStorage.setItem(CHANNEL_KEY, id);
  }
  return id;
}
