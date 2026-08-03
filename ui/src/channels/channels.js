import "./channels.css";
import { api } from "../base/api.js";
import { el, empty, mount, pill } from "../base/dom.js";

/**
 * How you reach Archie. Backed by GET /api/channels, which derives from
 * [chat] in config.toml -- see internal/webui/api_config.go's
 * handleChannels. Each channel is a card with a connected/not-configured
 * pill and one line explaining what turning it on gets you, because
 * "token_env unset" means nothing to someone who hasn't read config.toml.
 */
export function channelsPage() {
  const root = el("div");
  const cardGrid = el("div.grid.grid-2");

  render();
  load();

  function render() {
    mount(
      root,
      el(
        "div.page-head",
        el(
          "div",
          el("h1.page-title", "Channels"),
          el("p.page-sub", "The conversational front-ends Archie can be reached through."),
        ),
        el("div.page-actions", el("button.btn", { onclick: load }, "Refresh")),
      ),
      cardGrid,
    );
  }

  async function load() {
    try {
      const res = await api.channels();
      renderCards(res?.channels || []);
    } catch (err) {
      mount(cardGrid, empty("Cannot reach archied", String(err.message || err)));
    }
  }

  function renderCards(channels) {
    if (!channels.length) {
      mount(
        cardGrid,
        empty(
          "No channels configured",
          "Configure [chat.telegram] or [chat.webhook_addr] in config.toml to talk to Archie outside the dashboard.",
        ),
      );
      return;
    }
    mount(cardGrid, ...channels.map(channelCard));
  }

  function channelCard(channel) {
    return el(
      "div.card.channel-card",
      el(
        "div.card-head",
        el("h3.card-title", channel.name),
        channel.configured ? pill("connected", "ok") : pill("not configured", "idle"),
      ),
      el("p.channel-desc", channel.description),
      el("p.channel-detail", channel.detail),
    );
  }

  return root;
}
