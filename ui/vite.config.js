import { defineConfig } from "vite";

// The dashboard is served from the archied binary, so the build output is a
// committed dist/ that ui/embed.go embeds. Hashed asset names are avoided:
// dist/ lives in git, and hashed filenames would leave a new orphan on every
// rebuild rather than overwriting the previous one.
export default defineConfig({
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
    // `npm run dev` proxies the API to a locally running archied so the
    // dashboard can be developed without rebuilding the Go binary.
    proxy: {
      "/api": "http://127.0.0.1:8484",
      "/events": { target: "http://127.0.0.1:8484", ws: true },
    },
  },
});
