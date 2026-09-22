import "./legacy-hash-redirect";

import { createPinia } from "pinia";
import { createApp } from "vue";

import App from "./App.vue";
import { registerArchieWebMcpTools } from "./lib/archie-webmcp";
import router from "./router";
import { useAppearanceStore } from "./stores/appearance";
import { useLiveUpdatesStore } from "./stores/live-updates";
// One stylesheet: the design tokens, and the element defaults that have no
// component to live in (the canvas, the focus floor, the motion preference).
// Everything else is Tailwind on the component that owns it.
import "./style.css";

const app = createApp(App);
const pinia = createPinia();

app.use(pinia).use(router);
useAppearanceStore(pinia).initialize();
useLiveUpdatesStore(pinia).initialize();
app.mount("#app");

// Published after the shell paints: an in-page agent's tools are not worth
// delaying the dashboard for, and the browser no-ops when modelContext is
// absent (which it is outside the origin trial).
void registerArchieWebMcpTools();
