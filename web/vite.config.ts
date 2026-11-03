import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";

// `npm run dev` proxies the API to a Plexodus server on this machine.
// `npm run dev:tv` (mode "tv", see .env.tv) listens on the LAN and proxies to
// the seedbox, so a TV app pointed at http://<this PC>:5173 runs the UI
// straight from the source tree and hot-reloads on every save.
// PLEXODUS_SERVER in the environment overrides either.
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), "");
  const target = process.env.PLEXODUS_SERVER || env.PLEXODUS_SERVER || "http://localhost:8484";
  const proxy = { target, changeOrigin: true };
  return {
    plugins: [react()],
    server: {
      port: 5173,
      strictPort: true,
      // The HMR client connects back to whatever host served the page, so a
      // TV that loaded http://192.168.0.x:5173 reconnects there; nothing to set.
      proxy: { "/api": proxy, "/art": proxy },
    },
    build: { outDir: "dist", emptyOutDir: false, chunkSizeWarningLimit: 800 },
  };
});
