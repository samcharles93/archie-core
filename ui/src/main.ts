import "./legacy-hash-redirect";

import { createApp } from "vue";

import App from "./App.vue";
import router from "./router";
// One stylesheet: the design tokens, and the element defaults that have no
// component to live in (the canvas, the focus floor, the motion preference).
// Everything else is Tailwind on the component that owns it.
import "./style.css";

createApp(App).use(router).mount("#app");
