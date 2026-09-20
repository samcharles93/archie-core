import path from "node:path";
import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";
import tailwindcss from "@tailwindcss/vite";

// The dashboard is served from the archie-ui binary, and ui/embed.go embeds
// the committed dist/. Two things below are load-bearing for that and are not
// part of the stock shadcn-vue Vite setup:
//
//   base: "./"            the bundle is served from a path the build does not
//                         know, so asset URLs must stay relative.
//   unhashed asset names  dist/ lives in git; hashed filenames would leave a
//                         new orphan file behind on every rebuild instead of
//                         overwriting the previous one.
export default defineConfig({
  plugins: [vue(), tailwindcss()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  base: "./",
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
