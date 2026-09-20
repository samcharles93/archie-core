import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [vue(), tailwindcss()],
  resolve: {
    alias: {
      "@": new URL("./src", import.meta.url).pathname,
    },
  },
  base: "/",
  build: {
    outDir: "dist",
    emptyOutDir: true,
    rollupOptions: {
      output: {
        entryFileNames: "assets/[name].js",
        chunkFileNames: "assets/[name].js",
        assetFileNames: "assets/[name].[ext]",
      },
    },
  },
  server: {
    // Proxies the API to a locally running archie-ui (the process that serves
    // the dashboard) so the frontend can be developed without rebuilding Go.
    proxy: {
      "/api": "http://127.0.0.1:8484",
      "/events": { target: "http://127.0.0.1:8484", ws: true },
    },
  },
});
