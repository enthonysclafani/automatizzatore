import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// The build goes to internal/webui/dist, which the Go server embeds and
// serves at "/" with SPA fallback.
// In development, API calls go to the Go server on :8080.
const backend = "http://localhost:8080";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: {
      "/api": { target: backend, changeOrigin: false },
      "/healthz": { target: backend, changeOrigin: false },
      "/hooks": { target: backend, changeOrigin: false },
    },
  },
  build: {
    outDir: "../internal/webui/dist",
    // .gitkeep comes from public/, so the embed folder keeps it after a build.
    emptyOutDir: true,
    sourcemap: false,
  },
});
