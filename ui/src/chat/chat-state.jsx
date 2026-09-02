import { randomUUID } from "../base/uuid.jsx";

const CHANNEL_KEY = "archie.web.chat.channel";

export function channelID() {
  let id = localStorage.getItem(CHANNEL_KEY);
  if (!id) {
    id = randomUUID();
    localStorage.setItem(CHANNEL_KEY, id);
  }
  return id;
}
