const CHANNEL_KEY = "archie.web.chat.channel";

export function channelID() {
  let id = localStorage.getItem(CHANNEL_KEY);
  if (!id) {
    id = crypto.randomUUID();
    localStorage.setItem(CHANNEL_KEY, id);
  }
  return id;
}
